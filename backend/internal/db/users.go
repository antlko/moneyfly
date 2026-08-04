package db

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("db: not found")

// ErrEmailTaken is returned when an email already belongs to another account.
var ErrEmailTaken = errors.New("db: email already registered")

// User is an account. PasswordHash is empty for an account that signs in only
// through an identity provider.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	DisplayName  string
	BaseCurrency string
	IsAdmin      bool
	CreatedAt    int64
}

// NewID mints a UUIDv7: time-ordered, so ids sort by creation and index well,
// and generatable on a client with no server round-trip.
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 only fails if the system entropy source does, which is not a
		// condition this process can meaningfully continue through.
		panic(err)
	}
	return id.String()
}

// CountUsers returns how many accounts exist. Zero means the instance is
// unclaimed, which is what unlocks registration regardless of config.
func (d *DB) CountUsers() (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts an account. passwordHash may be empty for OIDC-only users.
// The first account created is the admin.
func (d *DB) CreateUser(email, passwordHash, displayName, baseCurrency string) (*User, error) {
	email = normalizeEmail(email)
	n, err := d.CountUsers()
	if err != nil {
		return nil, err
	}

	u := &User{
		ID:           NewID(),
		Email:        email,
		PasswordHash: passwordHash,
		DisplayName:  displayName,
		BaseCurrency: baseCurrency,
		IsAdmin:      n == 0,
		CreatedAt:    time.Now().Unix(),
	}
	_, err = d.Exec(`
		INSERT INTO users (id, email, password_hash, display_name, base_currency, is_admin, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, nullIfEmpty(u.PasswordHash), u.DisplayName, u.BaseCurrency, u.IsAdmin, u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return u, nil
}

// UserByEmail looks an account up case-insensitively.
func (d *DB) UserByEmail(email string) (*User, error) {
	return d.scanUser(d.QueryRow(userSelect+` WHERE lower(email) = ?`, normalizeEmail(email)))
}

// UserByID looks an account up by id.
func (d *DB) UserByID(id string) (*User, error) {
	return d.scanUser(d.QueryRow(userSelect+` WHERE id = ?`, id))
}

// SetPassword replaces (or, for an OIDC-only account, sets) the password hash.
func (d *DB) SetPassword(userID, passwordHash string) error {
	_, err := d.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`,
		nullIfEmpty(passwordHash), userID)
	return err
}

const userSelect = `
	SELECT id, email, coalesce(password_hash, ''), display_name, base_currency, is_admin, created_at
	FROM users`

func (d *DB) scanUser(row *sql.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.BaseCurrency, &u.IsAdmin, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUniqueViolation reports whether err is a SQLite uniqueness failure. The
// modernc driver does not export a typed error for this, so the message is what
// there is to match on.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
