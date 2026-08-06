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

// ListUsers returns every account on the instance, oldest first — the order
// they claimed it in, which is also admin-first on a normal instance since
// the first account created is always the admin.
func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.Query(userSelect + ` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName,
			&u.BaseCurrency, &u.IsAdmin, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ErrLastAdmin is returned when an action would leave the instance with no
// admin at all — deleting the last admin, or demoting them. There is no
// support desk on a self-hosted instance to recover from that, so it is
// refused rather than allowed and left as a support burden.
var ErrLastAdmin = errors.New("db: cannot remove the last admin")

// adminCount is shared by DeleteUser and SetAdmin, both of which only need to
// ask it when the account in question is itself an admin.
func (d *DB) adminCount() (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM users WHERE is_admin = 1`).Scan(&n)
	return n, err
}

// DeleteUser removes an account and, through the foreign keys every synced
// table carries back to `users` (docs/ARCHITECTURE.md §1), every row it
// owns — sessions, devices, identities and the full domain ledger. There is
// no separate cleanup step because there is nothing left for one to do.
func (d *DB) DeleteUser(id string) error {
	u, err := d.UserByID(id)
	if err != nil {
		return err
	}
	if u.IsAdmin {
		n, err := d.adminCount()
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	_, err = d.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// SetAdmin promotes or demotes an account. Demoting the last admin is
// refused for the same reason deleting them is.
func (d *DB) SetAdmin(id string, isAdmin bool) error {
	u, err := d.UserByID(id)
	if err != nil {
		return err
	}
	if u.IsAdmin && !isAdmin {
		n, err := d.adminCount()
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	_, err = d.Exec(`UPDATE users SET is_admin = ? WHERE id = ?`, isAdmin, id)
	return err
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
