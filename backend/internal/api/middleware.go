package api

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/auth"
	"moneyfly/internal/db"
)

// errorHandler renders every handler error as {"error": "..."} — the single
// shape the frontend parses. Handlers therefore return fiber.NewError(code, msg)
// rather than writing error bodies themselves.
func errorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	msg := "internal server error"

	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code
		msg = fe.Message
	} else {
		// Only unexpected errors are logged: a 404 or a validation 400 is normal
		// traffic, not an incident.
		slog.ErrorContext(c.Context(), "unhandled error", "error", err, "path", c.Path())
	}
	return c.Status(code).JSON(fiber.Map{"error": msg})
}

// Keys under which the authenticated user and session are stashed on the
// request. Typed to their own string type so nothing else can collide with them.
type ctxKey string

const (
	userKey    ctxKey = "user"
	sessionKey ctxKey = "session"
)

// authMW requires a valid session cookie or API token and puts the account on
// the request.
//
// The cookie is checked first, and its absence is what falls through to
// Bearer rather than an invalid cookie doing so: a browser always sends its
// cookie automatically, so a request carrying one is a browser, and a stale
// or forged cookie there should say so plainly rather than quietly try a
// second credential.
func (s *Server) authMW(c fiber.Ctx) error {
	if token := c.Cookies(auth.CookieName); token != "" {
		user, session, err := s.conn().SessionUser(auth.HashToken(token))
		if errors.Is(err, db.ErrNotFound) {
			// The cookie is stale or forged. Clear it so the browser stops
			// sending it and the SPA settles on the sign-in screen instead of
			// looping.
			s.clearSessionCookie(c)
			return fiber.NewError(fiber.StatusUnauthorized, "session expired")
		}
		if err != nil {
			return err
		}
		c.Locals(userKey, user)
		c.Locals(sessionKey, session)
		return c.Next()
	}

	if token, ok := bearerToken(c); ok {
		user, err := s.conn().APITokenUser(auth.HashToken(token))
		if errors.Is(err, db.ErrNotFound) {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid token")
		}
		if err != nil {
			return err
		}
		c.Locals(userKey, user)
		// No session behind an API token — sessionLocal already answers with a
		// zero value when none was set, which is the honest answer here: a
		// token has no device to report.
		return c.Next()
	}

	return fiber.NewError(fiber.StatusUnauthorized, "not signed in")
}

// bearerToken reads the token out of "Authorization: Bearer <token>".
func bearerToken(c fiber.Ctx) (string, bool) {
	const prefix = "Bearer "
	h := c.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

// userLocal returns the account put on the request by authMW. It is only valid
// behind that middleware, and panics otherwise rather than returning a nil user
// that would read as "anonymous" somewhere deep in a handler.
func userLocal(c fiber.Ctx) *db.User {
	u, ok := c.Locals(userKey).(*db.User)
	if !ok {
		panic("api: userLocal called on a route without authMW")
	}
	return u
}

// sessionLocal returns the current session, or a zero value when the route is
// unauthenticated — callers use it for the device id, which is optional.
func sessionLocal(c fiber.Ctx) *db.Session {
	s, ok := c.Locals(sessionKey).(*db.Session)
	if !ok {
		return &db.Session{}
	}
	return s
}

// currentUser resolves the session on a route that does not require one.
func (s *Server) currentUser(c fiber.Ctx) *db.User {
	token := c.Cookies(auth.CookieName)
	if token == "" {
		return nil
	}
	user, _, err := s.conn().SessionUser(auth.HashToken(token))
	if err != nil {
		return nil
	}
	return user
}

// requestLogger logs one line per request. It deliberately logs the route path
// rather than the raw URL so query strings (which may carry filters) stay out of
// the logs.
func (s *Server) requestLogger(c fiber.Ctx) error {
	start := time.Now()
	err := c.Next()
	slog.DebugContext(c.Context(), "request",
		"method", c.Method(),
		"path", c.Path(),
		"status", c.Response().StatusCode(),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return err
}
