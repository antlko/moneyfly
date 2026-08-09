package db

import (
	"database/sql"
	"errors"
	"time"
)

// Identity is one way of signing in to an account: a provider plus that
// provider's stable subject id.
type Identity struct {
	ID        string
	UserID    string
	Provider  string
	Subject   string
	Email     string
	CreatedAt int64
}

// CreateIdentity links a provider subject to an account.
func (d *DB) CreateIdentity(userID, provider, subject, email string) (*Identity, error) {
	id := &Identity{
		ID:        NewID(),
		UserID:    userID,
		Provider:  provider,
		Subject:   subject,
		Email:     email,
		CreatedAt: time.Now().Unix(),
	}
	_, err := d.Exec(`
		INSERT INTO auth_identity (id, user_id, provider, subject, email, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id.ID, id.UserID, id.Provider, id.Subject, id.Email, id.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrIdentityTaken
		}
		return nil, err
	}
	return id, nil
}

// ErrIdentityTaken is returned when a provider subject is already linked to some
// account — possibly a different one, which is why the caller must not treat it
// as "already done".
var ErrIdentityTaken = errors.New("db: identity already linked")

// IdentityBySubject finds the account a provider subject belongs to.
func (d *DB) IdentityBySubject(provider, subject string) (*Identity, error) {
	row := d.QueryRow(`
		SELECT id, user_id, provider, subject, email, created_at
		FROM auth_identity WHERE provider = ? AND subject = ?`, provider, subject)

	var i Identity
	err := row.Scan(&i.ID, &i.UserID, &i.Provider, &i.Subject, &i.Email, &i.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// IdentitiesForUser lists an account's linked providers.
func (d *DB) IdentitiesForUser(userID string) ([]Identity, error) {
	rows, err := d.Query(`
		SELECT id, user_id, provider, subject, email, created_at
		FROM auth_identity WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Identity
	for rows.Next() {
		var i Identity
		if err := rows.Scan(&i.ID, &i.UserID, &i.Provider, &i.Subject, &i.Email, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// DeleteIdentity unlinks a provider from an account.
//
// It refuses to remove the last way in: an account with no password, no
// identities and no passkeys would be permanently unreachable, and there is no
// support desk to recover it.
func (d *DB) DeleteIdentity(userID, identityID string) error {
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

	res, err := d.Exec(`DELETE FROM auth_identity WHERE id = ? AND user_id = ?`, identityID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// countSignInMethods reports how many independent ways u has to sign in: a
// password counts as one, plus one per linked identity, plus one per registered
// passkey.
//
// Shared by DeleteIdentity above and DeleteWebAuthnCredential (webauthn.go),
// which both refuse to go below one. It lives here, spanning three tables,
// because the guard is only correct when it can see all of them at once — when
// it only knew about passwords and identities, adding passkeys would have let
// someone delete their only identity while holding a passkey (fine) *and* also
// delete their only passkey while holding no identity (not fine), each check
// blind to the other. Any future sign-in method has to be counted here too, or
// its own last-row deletion will not be refused.
func (d *DB) countSignInMethods(u *User) (int, error) {
	n := 0
	if u.PasswordHash != "" {
		n++
	}
	ids, err := d.IdentitiesForUser(u.ID)
	if err != nil {
		return 0, err
	}
	n += len(ids)

	passkeys, err := d.CountWebAuthnCredentials(u.ID)
	if err != nil {
		return 0, err
	}
	return n + passkeys, nil
}

// ErrLastSignInMethod is returned when unlinking would lock the account out.
var ErrLastSignInMethod = errors.New("db: cannot remove the only sign-in method")
