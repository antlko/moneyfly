package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// AuthRepo stores users and sessions. It implements auth.Repo.
//
// `user` and `session` are identity tables, not user-owned data: a login looks a
// user up by email and a request looks a session up by token hash, neither of
// which can be scoped to a user that is not yet known. They are therefore outside
// the user_id rule enforced by TestNoUnscopedQueries, which documents the same.
type AuthRepo struct{ db *sql.DB }

// NewAuthRepo builds the repository.
func NewAuthRepo(db *sql.DB) *AuthRepo { return &AuthRepo{db: db} }

const userColumns = `id, email, display_name, role, base_currency, timezone,
	fiscal_year_start_month, must_change_password`

func scanUser(row interface{ Scan(...any) error }) (auth.User, error) {
	var (
		u           auth.User
		displayName sql.NullString
		mustChange  int
	)
	err := row.Scan(&u.ID, &u.Email, &displayName, &u.Role, &u.BaseCurrency,
		&u.Timezone, &u.FiscalYearStartMonth, &mustChange)
	if err != nil {
		return auth.User{}, err
	}
	u.DisplayName = scanNullString(displayName)
	u.MustChangePassword = mustChange == 1
	return u, nil
}

// CountUsers returns the number of live users, which is what the bootstrap check
// keys off.
func (r *AuthRepo) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM user WHERE deleted_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting users: %w", err)
	}
	return n, nil
}

// CreateUser inserts a user.
func (r *AuthRepo) CreateUser(ctx context.Context, u auth.User, passwordHash string, now time.Time) (auth.User, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO user (email, password_hash, display_name, role, base_currency, timezone,
		                  fiscal_year_start_month, must_change_password, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		u.Email, passwordHash, nullString(u.DisplayName), u.Role, u.BaseCurrency, u.Timezone,
		u.FiscalYearStartMonth, boolToInt(u.MustChangePassword), ts(now), ts(now))
	if err != nil {
		if isUniqueViolation(err) {
			return auth.User{}, apperr.Conflictf("a user with email %q already exists", u.Email)
		}
		return auth.User{}, fmt.Errorf("store: creating user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return auth.User{}, fmt.Errorf("store: creating user: %w", err)
	}
	u.ID = id
	return u, nil
}

// UserByEmail returns the user and their password hash.
func (r *AuthRepo) UserByEmail(ctx context.Context, email string) (auth.User, string, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+`, password_hash FROM user WHERE email = ? AND deleted_at IS NULL`, email)
	var (
		u           auth.User
		displayName sql.NullString
		mustChange  int
		hash        string
	)
	err := row.Scan(&u.ID, &u.Email, &displayName, &u.Role, &u.BaseCurrency, &u.Timezone,
		&u.FiscalYearStartMonth, &mustChange, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, "", apperr.NotFoundf("user %q", email)
	}
	if err != nil {
		return auth.User{}, "", fmt.Errorf("store: reading user by email: %w", err)
	}
	u.DisplayName = scanNullString(displayName)
	u.MustChangePassword = mustChange == 1
	return u, hash, nil
}

// UserByID returns one user.
func (r *AuthRepo) UserByID(ctx context.Context, id int64) (auth.User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM user WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, apperr.NotFoundf("user %d", id)
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("store: reading user %d: %w", id, err)
	}
	return u, nil
}

// ListUsers returns every user, for boot-time maintenance. There is no session
// to scope it to and no endpoint behind it.
//
// unscoped-query-ok: `user` is the identity table the scoping is derived from
func (r *AuthRepo) ListUsers(ctx context.Context) ([]auth.User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM user WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: listing users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []auth.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPassword replaces a password hash and the must-change flag.
func (r *AuthRepo) SetPassword(ctx context.Context, userID int64, passwordHash string, mustChange bool, now time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE user SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		passwordHash, boolToInt(mustChange), ts(now), userID)
	if err != nil {
		return fmt.Errorf("store: setting password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("user %d", userID)
	}
	return nil
}

// CreateSession stores a session by its token hash.
func (r *AuthRepo) CreateSession(ctx context.Context, s auth.Session, userAgent string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO session (token_hash, user_id, user_agent, created_at, expires_at) VALUES (?,?,?,?,?)`,
		s.TokenHash, s.UserID, nullString(&userAgent), ts(now), ts(s.ExpiresAt))
	if err != nil {
		return fmt.Errorf("store: creating session: %w", err)
	}
	return nil
}

// UserBySessionToken resolves a live session to its user. Expiry and revocation
// are checked in SQL, so a clock difference in Go cannot extend a session.
func (r *AuthRepo) UserBySessionToken(ctx context.Context, tokenHash string, now time.Time) (auth.User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.display_name, u.role, u.base_currency, u.timezone,
		       u.fiscal_year_start_month, u.must_change_password
		FROM session s
		JOIN user u ON u.id = s.user_id
		WHERE s.token_hash = ?
		  AND s.revoked_at IS NULL
		  AND s.expires_at > ?
		  AND u.deleted_at IS NULL`, tokenHash, ts(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, apperr.NotFoundf("session")
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("store: reading session: %w", err)
	}
	return u, nil
}

// RevokeSession kills one session by token hash.
func (r *AuthRepo) RevokeSession(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE session SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, ts(now), tokenHash)
	if err != nil {
		return fmt.Errorf("store: revoking session: %w", err)
	}
	return nil
}

// RevokeSessionByID kills one of the user's sessions.
func (r *AuthRepo) RevokeSessionByID(ctx context.Context, userID int64, id string, now time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE session SET revoked_at = ? WHERE user_id = ? AND token_hash = ? AND revoked_at IS NULL`,
		ts(now), userID, id)
	if err != nil {
		return fmt.Errorf("store: revoking session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("session %q", id)
	}
	return nil
}

// RevokeOtherSessions kills every session except the one being kept. Passing an
// empty keep hash revokes all of them, which is what a password reset does.
func (r *AuthRepo) RevokeOtherSessions(ctx context.Context, userID int64, keepTokenHash string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE session SET revoked_at = ? WHERE user_id = ? AND token_hash <> ? AND revoked_at IS NULL`,
		ts(now), userID, keepTokenHash)
	if err != nil {
		return fmt.Errorf("store: revoking other sessions: %w", err)
	}
	return nil
}

// ListSessions returns the user's live sessions, newest first.
func (r *AuthRepo) ListSessions(ctx context.Context, userID int64, now time.Time) ([]auth.SessionInfo, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT token_hash, COALESCE(user_agent, ''), created_at, expires_at
		FROM session
		WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
		ORDER BY created_at DESC`, userID, ts(now))
	if err != nil {
		return nil, fmt.Errorf("store: listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []auth.SessionInfo
	for rows.Next() {
		var (
			s                auth.SessionInfo
			created, expires string
		)
		if err := rows.Scan(&s.ID, &s.UserAgent, &created, &expires); err != nil {
			return nil, fmt.Errorf("store: scanning session: %w", err)
		}
		if s.CreatedAt, err = parseTS(created); err != nil {
			return nil, fmt.Errorf("store: session created_at: %w", err)
		}
		if s.ExpiresAt, err = parseTS(expires); err != nil {
			return nil, fmt.Errorf("store: session expires_at: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
