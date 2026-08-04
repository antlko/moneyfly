package db

import (
	"database/sql"
	"errors"
	"time"
)

// OIDCState is an in-flight authorisation: the CSRF state, the replay nonce and
// the PKCE code verifier, plus where to go afterwards.
type OIDCState struct {
	State        string
	Provider     string
	Nonce        string
	CodeVerifier string
	LinkUserID   string // non-empty when linking a provider to an existing account
	RedirectTo   string
	ExpiresAt    int64
}

// SaveOIDCState stores a pending authorisation.
func (d *DB) SaveOIDCState(s OIDCState, ttl time.Duration) error {
	now := time.Now()
	_, err := d.Exec(`
		INSERT INTO oidc_state (state, provider, nonce, code_verifier, link_user_id,
		                        redirect_to, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.State, s.Provider, s.Nonce, s.CodeVerifier, nullIfEmpty(s.LinkUserID),
		s.RedirectTo, now.Unix(), now.Add(ttl).Unix())
	return err
}

// TakeOIDCState consumes a pending authorisation: it is deleted whether or not
// it had expired, so a state value can never be replayed.
func (d *DB) TakeOIDCState(state string) (*OIDCState, error) {
	row := d.QueryRow(`
		SELECT state, provider, nonce, code_verifier, coalesce(link_user_id, ''),
		       redirect_to, expires_at
		FROM oidc_state WHERE state = ?`, state)

	var s OIDCState
	err := row.Scan(&s.State, &s.Provider, &s.Nonce, &s.CodeVerifier,
		&s.LinkUserID, &s.RedirectTo, &s.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if _, err := d.Exec(`DELETE FROM oidc_state WHERE state = ?`, state); err != nil {
		return nil, err
	}
	if s.ExpiresAt <= time.Now().Unix() {
		return nil, ErrNotFound
	}
	return &s, nil
}

// DeleteExpiredOIDCState is called by the retention loop; abandoned
// authorisations (the user closed the provider's page) would otherwise linger.
func (d *DB) DeleteExpiredOIDCState(now int64) error {
	_, err := d.Exec(`DELETE FROM oidc_state WHERE expires_at <= ?`, now)
	return err
}
