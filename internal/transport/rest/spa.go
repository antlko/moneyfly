package rest

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"

	"github.com/antlko/moneyapp/internal/transport/rest/assets"
)

// registerSPA serves the embedded Vue build, falling back to index.html so that
// client-side routes survive a page reload.
//
// It is registered last, after the API group, so /api/v1/* can never be answered
// with the HTML shell.
func (s *Server) registerSPA() {
	dist, err := assets.Dist()
	if err != nil {
		s.deps.Log.Error("embedded UI unavailable", "error", err.Error())
		return
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		s.deps.Log.Error("embedded UI has no index.html", "error", err.Error())
		return
	}
	serveFile := adaptor.HTTPHandler(http.FileServer(http.FS(dist)))

	s.app.Use(func(c *fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.Next()
		}
		if path := strings.TrimPrefix(c.Path(), "/"); path != "" && exists(dist, path) {
			// Vite emits content-hashed filenames under assets/, so those are
			// immutable. index.html never is.
			if strings.HasPrefix(path, "assets/") {
				c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
			}
			return serveFile(c)
		}
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		c.Set(fiber.HeaderCacheControl, "no-cache")
		return c.Status(http.StatusOK).Send(index)
	})
}

func exists(dir fs.FS, path string) bool {
	f, err := dir.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	return err == nil && !st.IsDir()
}
