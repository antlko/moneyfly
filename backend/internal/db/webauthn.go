package db

import (
	"database/sql"
	"errors"
	"time"
)

// The two kinds of in-flight WebAuthn ceremony. Kept as Go constants rather
// than a SQL CHECK, matching how app.registration's own two values are
// validated in Go and not in the schema.
const (
	WebAuthnPurposeRegistration = "registration"
	WebAuthnPurposeLogin        = "login"
)

// ErrCredentialTaken is returned when a credential id is already stored. In
// practice unreachable except by a replayed registration: an authenticator
// mints a fresh random credential id every time, never reusing one.
var ErrCredentialTaken = errors.New("db: passkey already registered")

// WebAuthnCredential is one registered passkey.
//
// Data is the opaque JSON encoding of a go-webauthn Credential; nothing in this
// package looks inside it. CredentialID is that credential's own id, base64url
// encoded, kept as its own column because a sign-in has to find the row before
// there is anything to decode.
type WebAuthnCredential struct {
	ID           string
	UserID       string
	CredentialID string
	Name         string
	Data         string
	CreatedAt    int64
	LastUsedAt   int64
}

// CreateWebAuthnCredential stores a newly registered passkey.
func (d *DB) CreateWebAuthnCredential(id, userID, credentialID, name, data string) (*WebAuthnCredential, error) {
	c := &WebAuthnCredential{
		ID:           id,
		UserID:       userID,
		CredentialID: credentialID,
		Name:         name,
		Data:         data,
		CreatedAt:    time.Now().Unix(),
	}
	_, err := d.Exec(`
		INSERT INTO webauthn_credential (id, user_id, credential_id, name, data, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, 0)`,
		c.ID, c.UserID, c.CredentialID, c.Name, c.Data, c.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCredentialTaken
		}
		return nil, err
	}
	return c, nil
}

// WebAuthnCredentialsForUser lists an account's passkeys, oldest first. Also
// what the API layer decodes to build go-webauthn's view of the account.
func (d *DB) WebAuthnCredentialsForUser(userID string) ([]WebAuthnCredential, error) {
	rows, err := d.Query(`
		SELECT id, user_id, credential_id, name, data, created_at, last_used_at
		FROM webauthn_credential WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []WebAuthnCredential{}
	for rows.Next() {
		var c WebAuthnCredential
		if err := rows.Scan(&c.ID, &c.UserID, &c.CredentialID, &c.Name, &c.Data,
			&c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountWebAuthnCredentials is the cheap form of the above, for the places that
// only need to know whether any exist — the user DTO's hasPasskey flag and
// countSignInMethods (identities.go), neither of which decodes the blob.
func (d *DB) CountWebAuthnCredentials(userID string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM webauthn_credential WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// TouchWebAuthnCredential replaces a credential's stored blob after a sign-in.
//
// Not merely a timestamp bump: go-webauthn hands back an updated Credential
// whose signature counter has advanced, and that is the copy the *next*
// sign-in must be checked against — a counter that never moves is how a cloned
// authenticator goes unnoticed.
func (d *DB) TouchWebAuthnCredential(credentialID, data string) error {
	_, err := d.Exec(`
		UPDATE webauthn_credential SET data = ?, last_used_at = ? WHERE credential_id = ?`,
		data, time.Now().Unix(), credentialID)
	return err
}

// DeleteWebAuthnCredential removes a passkey.
//
// Refuses to remove the last way in, the same guard DeleteIdentity applies from
// the other side — see countSignInMethods in identities.go, which both share.
//
// Existence is checked *before* that guard, not after: an id that names no
// passkey of this account is a plain 404, and answering it with "you cannot
// remove your only sign-in method" would be actively misleading to someone who
// has no passkeys at all — which is exactly the case a password-only account
// deleting a stale id from another device hits.
func (d *DB) DeleteWebAuthnCredential(userID, id string) error {
	var exists int
	err := d.QueryRow(`SELECT count(*) FROM webauthn_credential WHERE id = ? AND user_id = ?`,
		id, userID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}

	u, err := d.UserByID(userID)
	if err != nil {
		return err
	}
	n, err := d.countSignInMethods(u)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastSignInMethod
	}

	_, err = d.Exec(`DELETE FROM webauthn_credential WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

// WebAuthnSession is an in-flight ceremony's server-side state.
//
// UserID is empty for a sign-in: a passkey sign-in is usernameless, so the
// account is not known until the browser's assertion names it.
type WebAuthnSession struct {
	ID        string
	UserID    string
	Purpose   string
	Data      string
	ExpiresAt int64
}

// SaveWebAuthnSession stores a pending ceremony.
func (d *DB) SaveWebAuthnSession(s WebAuthnSession, ttl time.Duration) error {
	now := time.Now()
	_, err := d.Exec(`
		INSERT INTO webauthn_session (id, user_id, purpose, data, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.ID, nullIfEmpty(s.UserID), s.Purpose, s.Data, now.Unix(), now.Add(ttl).Unix())
	return err
}

// TakeWebAuthnSession consumes a pending ceremony: deleted whether or not it
// had expired, so a challenge can never be replayed. Mirrors TakeOIDCState.
func (d *DB) TakeWebAuthnSession(id string) (*WebAuthnSession, error) {
	row := d.QueryRow(`
		SELECT id, coalesce(user_id, ''), purpose, data, expires_at
		FROM webauthn_session WHERE id = ?`, id)

	var s WebAuthnSession
	err := row.Scan(&s.ID, &s.UserID, &s.Purpose, &s.Data, &s.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if _, err := d.Exec(`DELETE FROM webauthn_session WHERE id = ?`, id); err != nil {
		return nil, err
	}
	if s.ExpiresAt <= time.Now().Unix() {
		return nil, ErrNotFound
	}
	return &s, nil
}

// DeleteExpiredWebAuthnSessions is called by the retention loop; a ceremony the
// user abandoned at the browser prompt would otherwise linger.
func (d *DB) DeleteExpiredWebAuthnSessions(now int64) error {
	_, err := d.Exec(`DELETE FROM webauthn_session WHERE expires_at <= ?`, now)
	return err
}
