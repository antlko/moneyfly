package rest

import (
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// SessionCookie is the session cookie name.
const SessionCookie = "moneyapp_session"

const ctxKeyUser = "auth_user"

func (s *Server) registerAuthRoutes(api fiber.Router) {
	g := api.Group("/auth")
	g.Post("/login", s.handleLogin)
	g.Post("/logout", s.handleLogout)

	// Everything below needs a session. Password change is reachable while the
	// forced-change flag is set; nothing else is.
	authed := g.Use(s.requireSession())
	authed.Get("/me", s.handleMe)
	authed.Post("/password", s.handleChangePassword)
	authed.Get("/sessions", s.requirePasswordChanged(), s.handleListSessions)
	authed.Delete("/sessions/:id", s.requirePasswordChanged(), s.handleRevokeSession)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(c *fiber.Ctx) error {
	// Per-IP backoff. Per-account lockout is deliberately avoided: it is a
	// denial-of-service lever against a known email address.
	if retryAfter, blocked := s.limiter.blocked(c.IP()); blocked {
		return writeRateLimited(c, retryAfter)
	}

	var req loginRequest
	if err := bind(c, &req); err != nil {
		return err
	}
	ctx := auth.WithUserAgent(c.UserContext(), c.Get(fiber.HeaderUserAgent))
	token, err := s.deps.Auth.Login(ctx, req.Email, req.Password)
	if err != nil {
		s.limiter.fail(c.IP())
		return err
	}
	s.limiter.reset(c.IP())

	c.Cookie(&fiber.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  s.deps.Clock.Now().Add(s.deps.Config.Auth.SessionTTL),
		HTTPOnly: true,
		Secure:   strings.HasPrefix(s.deps.Config.Server.BaseURL, "https://"),
		SameSite: fiber.CookieSameSiteStrictMode,
	})
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) handleLogout(c *fiber.Ctx) error {
	if token := c.Cookies(SessionCookie); token != "" {
		if err := s.deps.Auth.Logout(c.UserContext(), token); err != nil {
			return err
		}
	}
	c.Cookie(&fiber.Cookie{
		Name: SessionCookie, Value: "", Path: "/",
		Expires: time.Unix(0, 0), HTTPOnly: true, SameSite: fiber.CookieSameSiteStrictMode,
	})
	return c.SendStatus(http.StatusNoContent)
}

// UserDTO is the /auth/me payload.
type UserDTO struct {
	ID                   int64   `json:"id"`
	Email                string  `json:"email"`
	DisplayName          *string `json:"display_name"`
	Role                 string  `json:"role"`
	BaseCurrency         string  `json:"base_currency"`
	Timezone             string  `json:"timezone"`
	FiscalYearStartMonth int     `json:"fiscal_year_start_month"`
	MustChangePassword   bool    `json:"must_change_password"`
}

func (s *Server) handleMe(c *fiber.Ctx) error {
	u := currentUser(c)
	return c.JSON(UserDTO{
		ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: u.Role,
		BaseCurrency: u.BaseCurrency, Timezone: u.Timezone,
		FiscalYearStartMonth: u.FiscalYearStartMonth, MustChangePassword: u.MustChangePassword,
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(c *fiber.Ctx) error {
	var req changePasswordRequest
	if err := bind(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	ctx := auth.WithToken(c.UserContext(), c.Cookies(SessionCookie))
	if err := s.deps.Auth.ChangePassword(ctx, u.ID, req.CurrentPassword, req.NewPassword); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

// SessionDTO is one entry of the session list.
type SessionDTO struct {
	ID        string `json:"id"`
	UserAgent string `json:"user_agent"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	Current   bool   `json:"current"`
}

func (s *Server) handleListSessions(c *fiber.Ctx) error {
	u := currentUser(c)
	ctx := auth.WithToken(c.UserContext(), c.Cookies(SessionCookie))
	sessions, err := s.deps.Auth.ListSessions(ctx, u.ID)
	if err != nil {
		return err
	}
	out := make([]SessionDTO, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, SessionDTO{
			ID:        sess.ID,
			UserAgent: sess.UserAgent,
			CreatedAt: sess.CreatedAt.Format(time.RFC3339),
			ExpiresAt: sess.ExpiresAt.Format(time.RFC3339),
			Current:   sess.Current,
		})
	}
	return c.JSON(out)
}

func (s *Server) handleRevokeSession(c *fiber.Ctx) error {
	u := currentUser(c)
	if err := s.deps.Auth.RevokeSession(c.UserContext(), u.ID, c.Params("id")); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

// requireSession resolves the session cookie to a user and puts it in the request
// context. It is the only place a user id enters the system (conventions §6).
func (s *Server) requireSession() fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Cookies(SessionCookie)
		if token == "" {
			return apperr.Unauthorizedf("no session cookie")
		}
		u, err := s.deps.Auth.Authenticate(c.UserContext(), token)
		if err != nil {
			return err
		}
		c.Locals(ctxKeyUser, u)
		ctx := auth.WithUser(c.UserContext(), u)
		ctx = auth.WithToken(ctx, token)
		c.SetUserContext(ctx)
		return c.Next()
	}
}

// requirePasswordChanged refuses everything except the password change itself
// while the bootstrap flag is set.
func (s *Server) requirePasswordChanged() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if u := currentUser(c); u != nil && u.MustChangePassword {
			return apperr.Forbiddenf("the initial password must be changed before using the application")
		}
		return c.Next()
	}
}

// requireWritable refuses mutations from a read-only role.
func (s *Server) requireWritable() fiber.Handler {
	return func(c *fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		if u := currentUser(c); u != nil && u.Role == auth.RoleReadonly {
			return apperr.Forbiddenf("this account is read-only")
		}
		return c.Next()
	}
}

func currentUser(c *fiber.Ctx) *auth.User {
	u, _ := c.Locals(ctxKeyUser).(*auth.User)
	return u
}
