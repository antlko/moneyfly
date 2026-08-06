package db

import (
	"database/sql"
	"errors"
	"time"
)

// APIToken is a named, long-lived credential. Only TokenHash's owner ever
// knows the plaintext value — this row is what remains once it has been
// shown to the person exactly once, at creation.
type APIToken struct {
	ID         string
	UserID     string
	Name       string
	TokenHash  string
	CreatedAt  int64
	LastUsedAt int64
}

// CreateAPIToken stores a new token, already hashed by the caller
// (auth.HashToken) — this package never sees the plaintext.
func (d *DB) CreateAPIToken(id, userID, name, tokenHash string) (APIToken, error) {
	now := time.Now().Unix()
	if _, err := d.Exec(`
		INSERT INTO api_token (id, user_id, name, token_hash, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, 0)`,
		id, userID, name, tokenHash, now); err != nil {
		return APIToken{}, err
	}
	return APIToken{ID: id, UserID: userID, Name: name, TokenHash: tokenHash, CreatedAt: now}, nil
}

// APITokenUser resolves a token hash to its account, the Bearer-auth
// equivalent of SessionUser. It also touches last_used_at, at most once an
// hour for the same reason SessionUser rate-limits its own write — this runs
// on every authenticated request, against a database with one writer.
func (d *DB) APITokenUser(tokenHash string) (*User, error) {
	var t APIToken
	err := d.QueryRow(`
		SELECT id, user_id, last_used_at FROM api_token WHERE token_hash = ?`, tokenHash).
		Scan(&t.ID, &t.UserID, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	if now-t.LastUsedAt > 3600 {
		if _, err := d.Exec(`UPDATE api_token SET last_used_at = ? WHERE id = ?`, now, t.ID); err != nil {
			return nil, err
		}
	}
	return d.UserByID(t.UserID)
}

// ListAPITokens returns a user's tokens, newest first. Never the hash — this
// is for a settings screen, not for authenticating anything.
func (d *DB) ListAPITokens(userID string) ([]APIToken, error) {
	rows, err := d.Query(`
		SELECT id, name, created_at, last_used_at FROM api_token
		WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []APIToken{}
	for rows.Next() {
		t := APIToken{UserID: userID}
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes a token. Scoped by user id in the WHERE clause, not
// just by primary key, so one account can never revoke another's — the same
// structural scoping the sync tables get from their (user_id, id) primary key.
func (d *DB) DeleteAPIToken(userID, id string) error {
	_, err := d.Exec(`DELETE FROM api_token WHERE id = ? AND user_id = ?`, id, userID)
	return err
}
