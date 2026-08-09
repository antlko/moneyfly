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
//
// A rejected request (4xx/5xx) logs at a level visible under the *default*
// MONEYFLY_LOG_LEVEL (info) — everything else stays at debug, which is what
// keeps ordinary traffic quiet. Before this, every request logged at debug
// regardless of outcome, so a stock deployment (info by default) recorded
// nothing at all for a failed request — no line, no error, nothing — which
// read as "the logging is broken" when it was actually working exactly as
// configured, just not logging what anyone needed to see.
//
// If a request the client genuinely made produces *no* line here, not even at
// debug, it never reached this process: something in front of it — a reverse
// proxy, a tunnel, a WAF — answered first. That is the one failure mode this
// middleware cannot see or log, and it is worth checking for specifically
// when a report of a rejected request matches nothing in these logs at all.
func (s *Server) requestLogger(c fiber.Ctx) error {
	start := time.Now()
	requestBytes := len(c.Body())
	err := c.Next()

	status, message := errorHandlerWillRender(err)
	if err == nil {
		// The handler already wrote its own response directly (c.JSON, c.Send,
		// c.Status...), synchronously, before returning — so unlike the error
		// path below, the real status is already sitting on the response.
		status = c.Response().StatusCode()
	}

	attrs := []any{
		"method", c.Method(),
		"path", c.Path(),
		"status", status,
		"duration_ms", time.Since(start).Milliseconds(),
		"request_bytes", requestBytes,
	}
	if message != "" {
		attrs = append(attrs, "error", message)
	}

	switch level := levelFor(status); level {
	case slog.LevelError:
		slog.ErrorContext(c.Context(), "request failed", attrs...)
	case slog.LevelWarn:
		slog.WarnContext(c.Context(), "request rejected", attrs...)
	default:
		slog.DebugContext(c.Context(), "request", attrs...)
	}
	return err
}

// levelFor is the status->level rule on its own, so it can be checked without
// standing up a server: 5xx is Error, 4xx is Warn (both visible under the
// deployed default of MONEYFLY_LOG_LEVEL=info), everything else stays Debug.
func levelFor(status int) slog.Level {
	switch {
	case status >= fiber.StatusInternalServerError:
		return slog.LevelError
	case status >= fiber.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}

// errorHandlerWillRender predicts the status and message errorHandler is
// about to write for err, without waiting for it to actually happen.
//
// This has to duplicate errorHandler's own classification rather than read
// the rendered response, because of *when* requestLogger runs relative to it:
// errorHandler is invoked by Fiber's own dispatcher only once every
// app.Use()-registered middleware — this one included — has already returned
// from its own call to c.Next(). By that point c.Response() still holds
// whatever was on it *before* the error, not what the client is about to
// receive. Classifying err directly is what a middleware positioned here can
// actually see truthfully; querying the response object for it cannot.
func errorHandlerWillRender(err error) (status int, message string) {
	if err == nil {
		return 0, ""
	}
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code, fe.Message
	}
	// Mirrors errorHandler's own default for a non-fiber.Error — an unexpected
	// error never leaks its own text to the client, only to the server log
	// (via errorHandler's separate, detailed "unhandled error" line).
	return fiber.StatusInternalServerError, "internal server error"
}
