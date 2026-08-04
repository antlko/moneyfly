package db

import (
	"database/sql"
	"errors"
	"time"
)

// Session is a signed-in browser. Only the hash of the token is stored.
type Session struct {
	TokenHash  string
	UserID     string
	DeviceID   string
	UserAgent  string
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
}

// CreateSession stores a session for a token hash.
func (d *DB) CreateSession(tokenHash, userID, deviceID, userAgent string, ttl time.Duration) error {
	now := time.Now()
	_, err := d.Exec(`
		INSERT INTO sessions (token_hash, user_id, device_id, user_agent,
		                      created_at, expires_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, nullIfEmpty(deviceID), userAgent,
		now.Unix(), now.Add(ttl).Unix(), now.Unix())
	return err
}

// SessionUser resolves a token hash to its account, treating an expired session
// as absent. It also refreshes last_seen_at, but at most once an hour: this runs
// on every authenticated request, and SQLite has one writer.
func (d *DB) SessionUser(tokenHash string) (*User, *Session, error) {
	row := d.QueryRow(`
		SELECT token_hash, user_id, coalesce(device_id, ''), user_agent,
		       created_at, expires_at, last_seen_at
		FROM sessions WHERE token_hash = ?`, tokenHash)

	var s Session
	err := row.Scan(&s.TokenHash, &s.UserID, &s.DeviceID, &s.UserAgent,
		&s.CreatedAt, &s.ExpiresAt, &s.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().Unix()
	if s.ExpiresAt <= now {
		return nil, nil, ErrNotFound
	}
	if now-s.LastSeenAt > 3600 {
		if _, err := d.Exec(`UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`,
			now, tokenHash); err != nil {
			return nil, nil, err
		}
		s.LastSeenAt = now
	}

	u, err := d.UserByID(s.UserID)
	if err != nil {
		return nil, nil, err
	}
	return u, &s, nil
}

// DeleteSession signs one browser out.
func (d *DB) DeleteSession(tokenHash string) error {
	_, err := d.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions signs an account out everywhere — used after a password
// change, so a stolen cookie stops working.
func (d *DB) DeleteUserSessions(userID string) error {
	_, err := d.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteExpiredSessions is called by the retention loop.
func (d *DB) DeleteExpiredSessions(now int64) error {
	_, err := d.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now)
	return err
}
