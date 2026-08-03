package rest

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
)

// HeaderRequestID is echoed on every response and appears on every log line, so
// a 500's opaque body can be tied to the server-side detail.
const HeaderRequestID = "X-Request-Id"

const requestIDKey = "request_id"

// requestIDMiddleware accepts an inbound request id or mints one.
func requestIDMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get(HeaderRequestID)
		if id == "" || len(id) > 64 {
			id = newRequestID()
		}
		c.Locals(requestIDKey, id)
		c.Set(HeaderRequestID, id)
		return c.Next()
	}
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A request id is diagnostic, not a security boundary; a timestamp is a
		// fine fallback and must not fail the request.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

func requestID(c *fiber.Ctx) string {
	if v, ok := c.Locals(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// loggingMiddleware emits one structured line per request. The logger it is given
// already masks secrets, which matters because paths and query strings are logged.
func loggingMiddleware(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		status := c.Response().StatusCode()
		attrs := []any{
			"request_id", requestID(c),
			"method", c.Method(),
			"path", c.Path(),
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.IP(),
		}
		if q := string(c.Request().URI().QueryString()); q != "" {
			attrs = append(attrs, "query", q)
		}
		switch {
		case status >= 500:
			log.Error("request", attrs...)
		case status >= 400:
			log.Warn("request", attrs...)
		default:
			log.Info("request", attrs...)
		}
		return err
	}
}

// securityHeaders applies the posture from docs/10-deployment-ci.md §10.11. The
// CSP is self-only because every asset is embedded — there are no CDNs to allow.
func securityHeaders() fiber.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self' data:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
	return func(c *fiber.Ctx) error {
		c.Set("Content-Security-Policy", csp)
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("Cross-Origin-Opener-Policy", "same-origin")
		return c.Next()
	}
}
