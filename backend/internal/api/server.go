// Package api wires the HTTP server (Fiber): routing, middleware and handlers.
// The Server owns the loaded config, the database and the OIDC registry, and
// serves both the JSON API and the embedded SPA.
package api

import (
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	recovermw "github.com/gofiber/fiber/v3/middleware/recover"

	"moneyfly/internal/auth"
	"moneyfly/internal/config"
	"moneyfly/internal/db"
	"moneyfly/internal/web"
)

// maxBodyBytes bounds request bodies. A Monefy CSV export of a decade of daily
// spending is a few hundred KiB, so this is generous.
const maxBodyBytes = 32 << 20

// Server holds all runtime state and serves the HTTP API + embedded SPA.
type Server struct {
	mu        sync.RWMutex
	configDir string
	cfg       *config.Config
	database  *db.DB
	oidc      *auth.OIDCRegistry

	app    *fiber.App
	webFS  fs.FS
	logins *loginLimiter
	events *broker
	stop   chan struct{}
}

// New loads the config for configDir, opens the database, builds the Fiber app
// and starts background work.
func New(configDir string) (*Server, error) {
	configDir = filepath.Clean(configDir)
	if err := config.EnsureDir(configDir); err != nil {
		return nil, err
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		return nil, err
	}
	database, err := db.Open(config.DBPath(configDir))
	if err != nil {
		return nil, err
	}

	s := &Server{
		configDir: configDir,
		cfg:       cfg,
		database:  database,
		oidc:      newOIDCRegistry(cfg),
		webFS:     web.FS(),
		logins:    newLoginLimiter(),
		events:    newBroker(),
		stop:      make(chan struct{}),
	}
	s.app = fiber.New(fiber.Config{
		AppName:      "moneyfly",
		BodyLimit:    maxBodyBytes,
		ErrorHandler: errorHandler,
	})
	s.app.Use(recovermw.New())
	s.app.Use(s.requestLogger)
	s.routes()

	go s.retentionLoop()
	go s.fxLoop()
	return s, nil
}

func newOIDCRegistry(cfg *config.Config) *auth.OIDCRegistry {
	providers := make([]auth.ProviderConfig, 0, len(cfg.OIDC))
	for _, p := range cfg.OIDC {
		providers = append(providers, auth.ProviderConfig{
			ID:           p.ID,
			Name:         p.Name,
			Issuer:       p.Issuer,
			ClientID:     p.ClientID,
			ClientSecret: p.ClientSecret,
			Scopes:       p.Scopes,
		})
	}
	return auth.NewOIDCRegistry(cfg.Server.BaseURL, providers)
}

// App returns the Fiber app (for Listen / graceful shutdown in main).
func (s *Server) App() *fiber.App { return s.app }

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.config().Server.Addr }

// Close stops background work and closes the database.
func (s *Server) Close() {
	close(s.stop)
	s.mu.Lock()
	s.database.Close()
	s.mu.Unlock()
}

// routes registers every endpoint. Auth is per-route middleware so each route's
// requirements are explicit at the call site.
func (s *Server) routes() {
	app := s.app
	authed := s.authMW

	// Public.
	app.Get("/api/health", s.handleHealth)
	app.Post("/api/auth/register", s.handleRegister)
	app.Post("/api/auth/login", s.handleLogin)
	app.Post("/api/auth/logout", s.handleLogout)
	app.Get("/api/auth/oidc/:provider/start", s.handleOIDCStart)
	app.Get("/api/auth/oidc/:provider/callback", s.handleOIDCCallback)

	// Authenticated.
	app.Get("/api/auth/me", authed, s.handleMe)
	app.Put("/api/auth/password", authed, s.handleChangePassword)
	app.Get("/api/auth/identities", authed, s.handleListIdentities)
	app.Delete("/api/auth/identities/:id", authed, s.handleDeleteIdentity)
	app.Get("/api/devices", authed, s.handleListDevices)
	app.Delete("/api/devices/:id", authed, s.handleDeleteDevice)

	// Exchange rates. Read-only and not per-user — a rate is a fact about the
	// world — but still behind auth, because an unauthenticated instance should
	// answer nothing but /api/health.
	app.Get("/api/fx/latest", authed, s.handleLatestRates)
	app.Get("/api/fx/rates", authed, s.handleRateHistory)
	app.Get("/api/fx/currencies", authed, s.handleCurrencies)

	// Sync.
	app.Post("/api/sync/push", authed, s.handlePush)
	app.Get("/api/sync/pull", authed, s.handlePull)
	app.Get("/api/sync/snapshot", authed, s.handleSnapshot)
	app.Get("/api/sync/events", authed, s.handleEvents)

	// SPA fallback — must be registered last.
	app.Use(s.serveSPA)
}

// serveSPA serves the embedded SPA, falling back to index.html for unknown
// client-side routes. Unmatched /api paths return a JSON 404 instead, so a
// typo'd endpoint never yields HTML the client would try to parse as JSON.
func (s *Server) serveSPA(c fiber.Ctx) error {
	if strings.HasPrefix(c.Path(), "/api/") {
		return fiber.NewError(fiber.StatusNotFound, "not found")
	}
	p := strings.TrimPrefix(path.Clean(c.Path()), "/")
	if p == "" {
		return s.sendIndex(c)
	}
	data, err := fs.ReadFile(s.webFS, p)
	if err != nil {
		// A path that names a *file* and is not there must 404, not fall back to
		// the app shell. Returning index.html with a 200 for `/sw.js` hands the
		// browser an HTML document to register as a service worker, and for any
		// missing asset it turns a broken build into a page that half-works.
		// Client-side routes have no extension, so this cannot swallow one.
		if path.Ext(p) != "" {
			return fiber.NewError(fiber.StatusNotFound, "not found")
		}
		return s.sendIndex(c)
	}
	if strings.HasPrefix(p, "assets/") {
		c.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if ext := strings.TrimPrefix(path.Ext(p), "."); ext != "" {
		// Fiber's table does not know .webmanifest, and the fallback of
		// application/octet-stream makes browsers ignore the manifest — which
		// silently costs installability, the one thing the file is for.
		if ext == "webmanifest" {
			c.Set("Content-Type", "application/manifest+json; charset=utf-8")
		} else {
			c.Type(ext)
		}
	}
	return c.Send(data)
}

func (s *Server) sendIndex(c fiber.Ctx) error {
	data, err := fs.ReadFile(s.webFS, "index.html")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "web UI not built")
	}
	c.Set("Cache-Control", "no-cache")
	c.Type("html")
	return c.Send(data)
}

// --- Concurrency-safe accessors ------------------------------------------------

func (s *Server) config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Server) conn() *db.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.database
}

func (s *Server) providers() *auth.OIDCRegistry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.oidc
}

// --- Background ----------------------------------------------------------------

// retentionLoop sweeps expired sessions and abandoned OIDC authorisations.
func (s *Server) retentionLoop() {
	s.runRetention()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.runRetention()
		}
	}
}

func (s *Server) runRetention() {
	now := time.Now().Unix()
	database := s.conn()
	if err := database.DeleteExpiredSessions(now); err != nil {
		slog.Error("retention: delete sessions", "error", err)
	}
	if err := database.DeleteExpiredOIDCState(now); err != nil {
		slog.Error("retention: delete oidc state", "error", err)
	}

	// Trimming the journal costs a long-absent device a full re-bootstrap, never
	// any data: the domain rows are untouched.
	days := s.config().Sync.ChangeLogRetentionDays
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	if n, err := database.TrimChangeLog(cutoff); err != nil {
		slog.Error("retention: trim change log", "error", err)
	} else if n > 0 {
		slog.Info("retention: trimmed change log", "rows", n, "older_than_days", days)
	}
}
