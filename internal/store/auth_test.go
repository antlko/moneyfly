package store

import (
	"errors"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/domain/seed"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

func TestLogin_WrongPassword_NoUserEnumeration(t *testing.T) {
	h := newHarness(t)
	h.user("owner@example.test")

	_, wrongPassword := h.AuthSvc.Login(h.ctx, "owner@example.test", "not-the-password")
	_, unknownEmail := h.AuthSvc.Login(h.ctx, "nobody@example.test", "not-the-password")

	if !errors.Is(wrongPassword, apperr.ErrUnauthorized) || !errors.Is(unknownEmail, apperr.ErrUnauthorized) {
		t.Fatalf("both paths must be unauthorized: %v / %v", wrongPassword, unknownEmail)
	}
	// The client-visible outcome is identical: same sentinel, so transport maps
	// both to a bare 401 with no detail.
	if wrongPassword.Error() == unknownEmail.Error() {
		t.Log("note: internal messages coincide, which is fine — they never reach the client")
	}

	// Both paths run a bcrypt comparison, so neither is materially faster. A
	// decoy hash exists precisely so the unknown-email path is not the quick one.
	timeOf := func(email string) time.Duration {
		start := time.Now()
		_, _ = h.AuthSvc.Login(h.ctx, email, "not-the-password")
		return time.Since(start)
	}
	known, unknown := timeOf("owner@example.test"), timeOf("nobody@example.test")
	ratio := float64(known) / float64(unknown)
	if ratio > 8 || ratio < 0.125 {
		t.Errorf("login timing differs by more than 8x (known %v, unknown %v): the unknown-email path must also hash", known, unknown)
	}
}

func TestLogin_Succeeds(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	token, err := h.AuthSvc.Login(h.ctx, "OWNER@Example.test", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token == "" {
		t.Fatal("no token returned")
	}
	got, err := h.AuthSvc.Authenticate(h.ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("authenticated as %d, want %d", got.ID, u.ID)
	}

	// Only the hash is stored: the raw token must not appear in the session table.
	var count int
	if err := h.Auth.db.QueryRow(`SELECT count(*) FROM session WHERE token_hash = ?`, token).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatal("the raw token is stored; only its SHA-256 may be")
	}
	if err := h.Auth.db.QueryRow(`SELECT count(*) FROM session WHERE token_hash = ?`,
		auth.HashToken(token)).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatal("the session was not stored under its hash")
	}
}

func TestSession_ExpiredRejected(t *testing.T) {
	h := newHarness(t)
	h.user("owner@example.test")

	stepping := clock.NewSteppable(fixedNow)
	provisioner := seed.NewProvisioner(h.Categories, h.Accounts, NewSettingRepo(h.db), stepping)
	svc, err := auth.NewService(h.Auth, stepping, 10, time.Hour, provisioner)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	token, err := svc.Login(h.ctx, "owner@example.test", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := svc.Authenticate(h.ctx, token); err != nil {
		t.Fatalf("a fresh session must authenticate: %v", err)
	}

	stepping.Advance(2 * time.Hour)
	if _, err := svc.Authenticate(h.ctx, token); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("an expired session must be rejected, got %v", err)
	}
}

func TestLogout_KillsSession(t *testing.T) {
	h := newHarness(t)
	h.user("owner@example.test")

	token, err := h.AuthSvc.Login(h.ctx, "owner@example.test", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := h.AuthSvc.Logout(h.ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.AuthSvc.Authenticate(h.ctx, token); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("a revoked session must be rejected, got %v", err)
	}
}

func TestChangePassword_RevokesOtherSessions(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	phone, err := h.AuthSvc.Login(h.ctx, "owner@example.test", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	laptop, err := h.AuthSvc.Login(h.ctx, "owner@example.test", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	ctx := auth.WithToken(h.ctx, laptop)
	if err := h.AuthSvc.ChangePassword(ctx, u.ID, "correct-horse", "battery-staple"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := h.AuthSvc.Authenticate(h.ctx, laptop); err != nil {
		t.Fatalf("the session that changed the password must stay alive: %v", err)
	}
	if _, err := h.AuthSvc.Authenticate(h.ctx, phone); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("the other session must be dead, got %v", err)
	}
	if _, err := h.AuthSvc.Login(h.ctx, "owner@example.test", "battery-staple"); err != nil {
		t.Fatalf("the new password must work: %v", err)
	}
	if _, err := h.AuthSvc.Login(h.ctx, "owner@example.test", "correct-horse"); err == nil {
		t.Fatal("the old password must stop working")
	}
}

func TestChangePassword_Validates(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	if err := h.AuthSvc.ChangePassword(h.ctx, u.ID, "wrong", "battery-staple"); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a wrong current password must be a validation error, got %v", err)
	}
	if err := h.AuthSvc.ChangePassword(h.ctx, u.ID, "correct-horse", "short"); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a short password must be rejected, got %v", err)
	}
	if err := h.AuthSvc.ChangePassword(h.ctx, u.ID, "correct-horse", "correct-horse"); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("reusing the current password must be rejected, got %v", err)
	}
}

func TestBootstrap_ForcesPasswordChange(t *testing.T) {
	h := newHarness(t)

	result, err := h.AuthSvc.Bootstrap(h.ctx, "admin@example.test", "bootstrap-password")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !result.Created {
		t.Fatalf("the first admin must be created: %+v", result)
	}
	token, err := h.AuthSvc.Login(h.ctx, "admin@example.test", "bootstrap-password")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	u, err := h.AuthSvc.Authenticate(h.ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !u.MustChangePassword {
		t.Fatal("the bootstrap admin must be flagged to change their password")
	}
	if u.Role != auth.RoleAdmin {
		t.Fatalf("role = %q, want admin", u.Role)
	}

	ctx := auth.WithToken(h.ctx, token)
	if err := h.AuthSvc.ChangePassword(ctx, u.ID, "bootstrap-password", "battery-staple"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	after, err := h.AuthSvc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if after.MustChangePassword {
		t.Fatal("the flag must clear once the password is changed")
	}
}

func TestBootstrap_SkippedWhenUsersExist(t *testing.T) {
	h := newHarness(t)
	h.user("owner@example.test")

	result, err := h.AuthSvc.Bootstrap(h.ctx, "admin@example.test", "bootstrap-password")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if result.Created {
		t.Fatal("a second admin must never be created")
	}
	if result.Skipped == "" {
		t.Fatal("the skip must be reported with a reason for the startup log")
	}
	if _, _, err := h.Auth.UserByEmail(h.ctx, "admin@example.test"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("no admin should exist, got %v", err)
	}
}

func TestBootstrap_SkippedWithoutPassword(t *testing.T) {
	h := newHarness(t)
	result, err := h.AuthSvc.Bootstrap(h.ctx, "admin@example.test", "")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if result.Created {
		t.Fatal("an admin must not be created without a password from the environment")
	}
}

func TestResetPassword_RevokesEverything(t *testing.T) {
	h := newHarness(t)
	h.user("owner@example.test")
	token, err := h.AuthSvc.Login(h.ctx, "owner@example.test", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if err := h.AuthSvc.ResetPassword(h.ctx, "owner@example.test", "reset-password-99"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, err := h.AuthSvc.Authenticate(h.ctx, token); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatal("a reset must revoke every existing session")
	}
	newToken, err := h.AuthSvc.Login(h.ctx, "owner@example.test", "reset-password-99")
	if err != nil {
		t.Fatalf("Login with the reset password: %v", err)
	}
	u, err := h.AuthSvc.Authenticate(h.ctx, newToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !u.MustChangePassword {
		t.Fatal("a CLI-reset password must be changed at next login")
	}
}

func TestSessions_ListAndRevoke(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	first, _ := h.AuthSvc.Login(h.ctx, "owner@example.test", "correct-horse")
	second, _ := h.AuthSvc.Login(h.ctx, "owner@example.test", "correct-horse")

	ctx := auth.WithToken(h.ctx, second)
	sessions, err := h.AuthSvc.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("%d sessions, want 2", len(sessions))
	}
	currents := 0
	var firstID string
	for _, s := range sessions {
		if s.Current {
			currents++
		}
		if s.ID == auth.HashToken(first) {
			firstID = s.ID
		}
	}
	if currents != 1 {
		t.Fatalf("%d sessions marked current, want exactly 1", currents)
	}
	if firstID == "" {
		t.Fatal("the other session is missing from the list")
	}

	if err := h.AuthSvc.RevokeSession(h.ctx, u.ID, firstID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := h.AuthSvc.Authenticate(h.ctx, first); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatal("the revoked session must be dead")
	}
	if _, err := h.AuthSvc.Authenticate(h.ctx, second); err != nil {
		t.Fatalf("the other session must survive: %v", err)
	}
}

func TestSessions_CannotRevokeAnotherUsers(t *testing.T) {
	h := newHarness(t)
	a := h.user("a@example.test")
	h.user("b@example.test")

	bToken, err := h.AuthSvc.Login(h.ctx, "b@example.test", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	err = h.AuthSvc.RevokeSession(h.ctx, a.ID, auth.HashToken(bToken))
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("user A must not be able to revoke user B's session, got %v", err)
	}
	if _, err := h.AuthSvc.Authenticate(h.ctx, bToken); err != nil {
		t.Fatalf("user B's session must be untouched: %v", err)
	}
}

func TestCreateUser_DuplicateEmailConflicts(t *testing.T) {
	h := newHarness(t)
	h.user("owner@example.test")
	_, err := h.AuthSvc.CreateUser(h.ctx, auth.User{Email: "Owner@example.test"}, "correct-horse", false)
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("a duplicate email must conflict, got %v", err)
	}
}
