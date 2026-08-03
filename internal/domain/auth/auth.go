// Package auth owns login, sessions and password changes.
//
// Invite-only: there is no registration path. The first admin is bootstrapped
// from configuration and must change the password at first login
// (docs/adr/0010-invite-only-auth.md).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

// MinPasswordLength is the shortest password accepted.
const MinPasswordLength = 8

// TokenBytes is the session token length, from crypto/rand.
const TokenBytes = 32

// Roles.
const (
	RoleAdmin    = "admin"
	RoleUser     = "user"
	RoleReadonly = "readonly"
)

// User is an authenticated principal.
type User struct {
	ID                   int64
	Email                string
	DisplayName          *string
	Role                 string
	BaseCurrency         string
	Timezone             string
	FiscalYearStartMonth int
	MustChangePassword   bool
}

// Session is a server-side session. Only the hash is ever stored, so a stolen
// database yields no usable tokens.
type Session struct {
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
}

// SessionInfo is a session as listed for the user.
type SessionInfo struct {
	ID        string // the token hash; it is not a credential
	UserAgent string
	CreatedAt time.Time
	ExpiresAt time.Time
	Current   bool
}

// Repo is the storage contract.
type Repo interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u User, passwordHash string, now time.Time) (User, error)
	UserByEmail(ctx context.Context, email string) (User, string, error)
	UserByID(ctx context.Context, id int64) (User, error)
	// ListUsers returns every user. It exists for boot-time maintenance —
	// backfilling seed rows an older account never received — and has no
	// user-facing endpoint.
	ListUsers(ctx context.Context) ([]User, error)
	SetPassword(ctx context.Context, userID int64, passwordHash string, mustChange bool, now time.Time) error

	CreateSession(ctx context.Context, s Session, userAgent string, now time.Time) error
	UserBySessionToken(ctx context.Context, tokenHash string, now time.Time) (User, error)
	RevokeSession(ctx context.Context, tokenHash string, now time.Time) error
	RevokeSessionByID(ctx context.Context, userID int64, id string, now time.Time) error
	RevokeOtherSessions(ctx context.Context, userID int64, keepTokenHash string, now time.Time) error
	ListSessions(ctx context.Context, userID int64, now time.Time) ([]SessionInfo, error)
}

// Provisioner seeds a new user's taxonomy. It is an interface so that auth does
// not depend on the category and account packages.
type Provisioner interface {
	Provision(ctx context.Context, userID int64) error
}

// Service is the auth use-case layer.
type Service struct {
	repo        Repo
	clock       clock.Clock
	bcryptCost  int
	sessionTTL  time.Duration
	provisioner Provisioner

	// decoyHash is compared against when an email is unknown, so a wrong email
	// and a wrong password cost the same time. Without it, response latency
	// enumerates accounts.
	decoyHash []byte
}

// NewService builds the service. cost must already have passed config validation
// (minimum 10).
func NewService(repo Repo, clk clock.Clock, bcryptCost int, sessionTTL time.Duration, p Provisioner) (*Service, error) {
	decoy, err := bcrypt.GenerateFromPassword([]byte("moneyapp-decoy-"+randomString(16)), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("auth: preparing decoy hash: %w", err)
	}
	return &Service{
		repo: repo, clock: clk, bcryptCost: bcryptCost,
		sessionTTL: sessionTTL, provisioner: p, decoyHash: decoy,
	}, nil
}

// Login verifies credentials and returns an opaque session token.
//
// The response is identical for an unknown email and a wrong password, and both
// paths run one bcrypt comparison, so neither reveals whether an account exists.
func (s *Service) Login(ctx context.Context, email, password string) (string, error) {
	email = normaliseEmail(email)
	user, hash, err := s.repo.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			_ = bcrypt.CompareHashAndPassword(s.decoyHash, []byte(password))
			return "", apperr.Unauthorizedf("no such user")
		}
		return "", err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return "", apperr.Unauthorizedf("password mismatch for user %d", user.ID)
	}

	token, tokenHash, err := newToken()
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	session := Session{UserID: user.ID, TokenHash: tokenHash, ExpiresAt: now.Add(s.sessionTTL)}
	if err := s.repo.CreateSession(ctx, session, userAgentFrom(ctx), now); err != nil {
		return "", err
	}
	return token, nil
}

// Authenticate resolves a token to a user. Expiry is checked in SQL, not here.
func (s *Service) Authenticate(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, apperr.Unauthorizedf("no session token")
	}
	u, err := s.repo.UserBySessionToken(ctx, HashToken(token), s.clock.Now())
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.Unauthorizedf("session not found or expired")
		}
		return nil, err
	}
	return &u, nil
}

// Logout revokes the current session.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repo.RevokeSession(ctx, HashToken(token), s.clock.Now())
}

// ChangePassword verifies the old password, sets the new one and revokes every
// other session — a lost phone must not stay logged in.
func (s *Service) ChangePassword(ctx context.Context, userID int64, old, new string) error {
	user, hash, err := s.userWithHash(ctx, userID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(old)); err != nil {
		return apperr.Validation("current_password", "does not match")
	}
	if err := ValidatePassword(new); err != nil {
		return err
	}
	if old == new {
		return apperr.Validation("new_password", "must differ from the current password")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(new), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hashing password: %w", err)
	}
	now := s.clock.Now()
	if err := s.repo.SetPassword(ctx, user.ID, string(newHash), false, now); err != nil {
		return err
	}
	return s.repo.RevokeOtherSessions(ctx, user.ID, HashToken(TokenFromContext(ctx)), now)
}

// ResetPassword is the CLI escape hatch: no SMTP dependency and no reset-token
// attack surface (docs/adr/0010-invite-only-auth.md).
func (s *Service) ResetPassword(ctx context.Context, email, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	user, _, err := s.repo.UserByEmail(ctx, normaliseEmail(email))
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hashing password: %w", err)
	}
	now := s.clock.Now()
	if err := s.repo.SetPassword(ctx, user.ID, string(hash), true, now); err != nil {
		return err
	}
	// Every existing session dies: a reset is what you do when you have lost
	// control of the account.
	return s.repo.RevokeOtherSessions(ctx, user.ID, "", now)
}

// ListSessions returns the user's live sessions, marking the current one.
func (s *Service) ListSessions(ctx context.Context, userID int64) ([]SessionInfo, error) {
	sessions, err := s.repo.ListSessions(ctx, userID, s.clock.Now())
	if err != nil {
		return nil, err
	}
	current := HashToken(TokenFromContext(ctx))
	for i := range sessions {
		sessions[i].Current = sessions[i].ID == current
	}
	return sessions, nil
}

// RevokeSession kills one of the user's sessions by id.
func (s *Service) RevokeSession(ctx context.Context, userID int64, id string) error {
	return s.repo.RevokeSessionByID(ctx, userID, id, s.clock.Now())
}

// CreateUser adds a user and seeds their taxonomy. There is no HTTP route to it:
// users are created by an admin or the bootstrap path.
func (s *Service) CreateUser(ctx context.Context, u User, password string, mustChange bool) (User, error) {
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	u.Email = normaliseEmail(u.Email)
	if !strings.Contains(u.Email, "@") {
		return User{}, apperr.Validation("email", "must be an email address")
	}
	if u.Role == "" {
		u.Role = RoleUser
	}
	switch u.Role {
	case RoleAdmin, RoleUser, RoleReadonly:
	default:
		return User{}, apperr.Validation("role", "must be admin, user or readonly")
	}
	if u.BaseCurrency == "" {
		u.BaseCurrency = "EUR"
	}
	if u.Timezone == "" {
		u.Timezone = "UTC"
	}
	if u.FiscalYearStartMonth == 0 {
		u.FiscalYearStartMonth = 8
	}
	u.MustChangePassword = mustChange

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return User{}, fmt.Errorf("auth: hashing password: %w", err)
	}
	created, err := s.repo.CreateUser(ctx, u, string(hash), s.clock.Now())
	if err != nil {
		return User{}, err
	}
	if s.provisioner != nil {
		if err := s.provisioner.Provision(ctx, created.ID); err != nil {
			return User{}, fmt.Errorf("auth: seeding taxonomy for user %d: %w", created.ID, err)
		}
	}
	return created, nil
}

// BootstrapResult reports what Bootstrap did, for the startup log.
type BootstrapResult struct {
	Created bool
	Skipped string
	Email   string
}

// ListUsers returns every user, for boot-time maintenance.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.repo.ListUsers(ctx)
}

// Bootstrap creates the first admin on first boot. It is a no-op once any user
// exists, so restarting the container never creates a second admin.
func (s *Service) Bootstrap(ctx context.Context, email, password string) (BootstrapResult, error) {
	if email == "" {
		return BootstrapResult{Skipped: "auth.bootstrap_admin_email is not set"}, nil
	}
	n, err := s.repo.CountUsers(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	if n > 0 {
		return BootstrapResult{Skipped: "users already exist"}, nil
	}
	if password == "" {
		return BootstrapResult{Skipped: "MONEYAPP_BOOTSTRAP_PASSWORD is not set"}, nil
	}
	u, err := s.CreateUser(ctx, User{Email: email, Role: RoleAdmin}, password, true)
	if err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{Created: true, Email: u.Email}, nil
}

func (s *Service) userWithHash(ctx context.Context, userID int64) (User, string, error) {
	u, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return User{}, "", err
	}
	full, hash, err := s.repo.UserByEmail(ctx, u.Email)
	if err != nil {
		return User{}, "", err
	}
	return full, hash, nil
}

// ValidatePassword enforces the minimum policy.
func ValidatePassword(p string) error {
	if len([]rune(p)) < MinPasswordLength {
		return apperr.Validation("new_password", "must be at least %d characters", MinPasswordLength)
	}
	if strings.TrimSpace(p) == "" {
		return apperr.Validation("new_password", "must not be blank")
	}
	return nil
}

// HashToken is the one-way transform applied before a token touches storage.
func HashToken(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (token, hash string, err error) {
	buf := make([]byte, TokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: generating session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token), nil
}

func randomString(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(buf)
}

func normaliseEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }
