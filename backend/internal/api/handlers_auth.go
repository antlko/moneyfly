package api

import (
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/auth"
	"moneyfly/internal/config"
	"moneyfly/internal/db"
)

type credentials struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	DeviceID    string `json:"deviceId"`
	Platform    string `json:"platform"`
}

// handleRegister creates an account and signs the browser in.
//
// Registration is allowed when the instance is configured `open` **or** when no
// account exists yet. That second clause is what makes a `closed` instance
// installable at all: the operator sets closed in config, starts it, and the
// first visit still gets to claim it.
func (s *Server) handleRegister(c fiber.Ctx) error {
	var in credentials
	if err := decode(c, &in); err != nil {
		return err
	}

	allowed, err := s.registrationAllowed()
	if err != nil {
		return err
	}
	if !allowed {
		return fiber.NewError(fiber.StatusForbidden, "registration is closed on this instance")
	}

	email, err := parseEmail(in.Email)
	if err != nil {
		return err
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}

	user, err := s.conn().CreateUser(email, hash, name, s.config().App.DefaultCurrency)
	if errors.Is(err, db.ErrEmailTaken) {
		return fiber.NewError(fiber.StatusConflict, "that email is already registered")
	}
	if err != nil {
		return err
	}
	return s.startSession(c, user, in.DeviceID, in.Platform)
}

// handleLogin verifies a password and starts a session.
func (s *Server) handleLogin(c fiber.Ctx) error {
	var in credentials
	if err := decode(c, &in); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))

	if s.logins.blocked(email) {
		return fiber.NewError(fiber.StatusTooManyRequests,
			"too many failed attempts — try again in a few minutes")
	}

	// One message for "no such account" and "wrong password", so this endpoint
	// cannot be used to enumerate who has an account here.
	const badCreds = "incorrect email or password"

	user, err := s.conn().UserByEmail(email)
	if errors.Is(err, db.ErrNotFound) {
		s.logins.fail(email)
		return fiber.NewError(fiber.StatusUnauthorized, badCreds)
	}
	if err != nil {
		return err
	}
	if user.PasswordHash == "" {
		return fiber.NewError(fiber.StatusUnauthorized,
			"this account signs in through an identity provider")
	}
	if !auth.VerifyPassword(user.PasswordHash, in.Password) {
		s.logins.fail(email)
		return fiber.NewError(fiber.StatusUnauthorized, badCreds)
	}

	s.logins.succeed(email)
	return s.startSession(c, user, in.DeviceID, in.Platform)
}

// handleLogout ends the current session. It is deliberately not authenticated:
// signing out with an already-invalid cookie should succeed, not 401.
func (s *Server) handleLogout(c fiber.Ctx) error {
	if token := c.Cookies(auth.CookieName); token != "" {
		if err := s.conn().DeleteSession(auth.HashToken(token)); err != nil {
			return err
		}
	}
	s.clearSessionCookie(c)
	return c.SendStatus(fiber.StatusNoContent)
}

// handleMe returns the signed-in account.
func (s *Server) handleMe(c fiber.Ctx) error {
	user := userLocal(c)
	ids, err := s.conn().IdentitiesForUser(user.ID)
	if err != nil {
		return err
	}
	passkeys, err := s.conn().CountWebAuthnCredentials(user.ID)
	if err != nil {
		return err
	}
	return c.JSON(toUserDTO(user, ids, passkeys > 0))
}

// handleChangePassword sets or replaces the password.
//
// An account created through an identity provider has no password; it may set
// one without proving a current password, because the provider session is the
// proof. Every other session is dropped either way.
func (s *Server) handleChangePassword(c fiber.Ctx) error {
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	user := userLocal(c)

	if user.PasswordHash != "" && !auth.VerifyPassword(user.PasswordHash, in.CurrentPassword) {
		return fiber.NewError(fiber.StatusUnauthorized, "current password is incorrect")
	}
	if err := auth.ValidatePassword(in.NewPassword); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		return err
	}
	database := s.conn()
	if err := database.SetPassword(user.ID, hash); err != nil {
		return err
	}
	if err := database.DeleteUserSessions(user.ID); err != nil {
		return err
	}

	// The caller signed out along with everyone else; give them a fresh session
	// rather than bouncing them to the login screen.
	fresh, err := database.UserByID(user.ID)
	if err != nil {
		return err
	}
	return s.startSession(c, fresh, sessionLocal(c).DeviceID, "")
}

func (s *Server) handleListIdentities(c fiber.Ctx) error {
	ids, err := s.conn().IdentitiesForUser(userLocal(c).ID)
	if err != nil {
		return err
	}
	// hasPasskey is false rather than looked up: only .Identities is read out
	// of the result, so counting passkeys here would be a query for a field
	// this response never contains.
	return c.JSON(toUserDTO(userLocal(c), ids, false).Identities)
}

func (s *Server) handleDeleteIdentity(c fiber.Ctx) error {
	err := s.conn().DeleteIdentity(userLocal(c).ID, c.Params("id"))
	switch {
	case errors.Is(err, db.ErrLastSignInMethod):
		return fiber.NewError(fiber.StatusConflict,
			"set a password before removing your only sign-in method")
	case errors.Is(err, db.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "identity not found")
	case err != nil:
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) handleListDevices(c fiber.Ctx) error {
	devices, err := s.conn().DevicesForUser(userLocal(c).ID)
	if err != nil {
		return err
	}
	return c.JSON(toDeviceDTOs(devices, sessionLocal(c).DeviceID))
}

func (s *Server) handleDeleteDevice(c fiber.Ctx) error {
	err := s.conn().DeleteDevice(userLocal(c).ID, c.Params("id"))
	if errors.Is(err, db.ErrNotFound) {
		return fiber.NewError(fiber.StatusNotFound, "device not found")
	}
	if err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// --- helpers -------------------------------------------------------------------

func (s *Server) registrationAllowed() (bool, error) {
	if s.config().App.Registration == config.RegistrationOpen {
		return true, nil
	}
	n, err := s.conn().CountUsers()
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// startSession mints a token, stores its hash, sets the cookie and answers with
// the account.
func (s *Server) startSession(c fiber.Ctx, user *db.User, deviceID, platform string) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	ttl := time.Duration(s.config().App.SessionTTLDays) * 24 * time.Hour
	database := s.conn()

	if err := database.CreateSession(auth.HashToken(token), user.ID, deviceID,
		string(c.Request().Header.UserAgent()), ttl); err != nil {
		return err
	}
	if err := database.UpsertDevice(user.ID, deviceID, "", platform); err != nil {
		return err
	}
	s.setSessionCookie(c, token, ttl)

	ids, err := database.IdentitiesForUser(user.ID)
	if err != nil {
		return err
	}
	passkeys, err := database.CountWebAuthnCredentials(user.ID)
	if err != nil {
		return err
	}
	return c.JSON(toUserDTO(user, ids, passkeys > 0))
}

func (s *Server) setSessionCookie(c fiber.Ctx, token string, ttl time.Duration) {
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(ttl),
		HTTPOnly: true,
		Secure:   requestIsHTTPS(c),
		// Lax rather than Strict: the OIDC callback arrives as a top-level
		// navigation from the identity provider and must carry the cookie for
		// the "link a provider" flow to see who is signed in.
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Now().Add(-time.Hour),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   requestIsHTTPS(c),
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// requestIsHTTPS decides whether to set the Secure flag. Trusting
// X-Forwarded-Proto here is safe in a way it usually is not: a forged header can
// only make the cookie stricter, which costs the forger their own session.
func requestIsHTTPS(c fiber.Ctx) bool {
	return c.Scheme() == "https" ||
		strings.EqualFold(c.Get("X-Forwarded-Proto"), "https")
}

func parseEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if _, err := mail.ParseAddress(email); err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "that does not look like an email address")
	}
	return email, nil
}
