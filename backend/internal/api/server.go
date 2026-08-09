// Package api wires the HTTP server (Fiber): routing, middleware and handlers.
// The Server owns the loaded config, the database and the OIDC registry, and
// serves both the JSON API and the embedded SPA.
package api

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
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

	app   *fiber.App
	webFS fs.FS
	// indexETag is the app shell's entity tag, fixed for the process lifetime
	// because the embedded FS is compiled into the binary.
	indexETag string
	logins    *loginLimiter
	events    *broker
	stop      chan struct{}
	// Signals the FX loop to look for newly needed currencies. Buffered by one:
	// the signal means "something changed", so a second one while the first is
	// pending would say nothing new.
	fxWake chan struct{}
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
		indexETag: indexETag(web.FS()),
		logins:    newLoginLimiter(),
		events:    newBroker(),
		stop:      make(chan struct{}),
		fxWake:    make(chan struct{}, 1),
	}
	s.app = fiber.New(fiber.Config{
		AppName:      "moneyfly",
		BodyLimit:    maxBodyBytes,
		ErrorHandler: errorHandler,
	})
	s.app.Use(recovermw.New())
	s.app.Use(s.requestLogger)
	// Compression is worth more here than anywhere else in the app: the SPA's
	// critical path is ~360 KB of JS and CSS that gzips to ~120 KB, and it is
	// paid on every cold load. It also covers /api/sync/snapshot and
	// /api/sync/pull, which are the largest JSON the app ever moves — the whole
	// cost of a new device bootstrapping.
	s.app.Use(compress.New(compress.Config{Next: isEventStream}))
	s.routes()

	go s.retentionLoop()
	go s.fxLoop()
	go s.recurringLoop()
	return s, nil
}

// eventStreamPath is the one route that must never be compressed.
const eventStreamPath = "/api/sync/events"

// isEventStream keeps the compressor off the sync event stream.
//
// SSE is a response that never ends, delivered a few bytes at a time. A
// compressor buffers to find something worth compressing, so those bytes stop
// arriving when they happen rather than when the server sent them — devices
// then look offline while the server is answering perfectly well. The dev proxy
// already carries a `no-transform` header for the same reason
// (web-ui/vite.config.ts).
//
// Matched on the request path rather than the response content type: the
// middleware decides before the handler has set a single header, so the type is
// not knowable yet.
//
// Split in two so the rule itself can be tested. Driving a real request through
// the middleware is not an option for this one: an authenticated event stream
// never completes, so the test harness waits for a response that is never
// coming.
func isEventStream(c fiber.Ctx) bool { return skipCompression(c.Path()) }

func skipCompression(path string) bool { return path == eventStreamPath }

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
	// Writing one is the exception to "read-only": a provider chain cannot
	// publish every currency anyone holds, and a rate nobody can supply leaves
	// those records outside every total. Not per-user for the same reason the
	// reads are not — a rate is a fact about the world, not about an account.
	app.Put("/api/fx/rates", authed, s.handleSetRate)
	app.Get("/api/fx/currencies", authed, s.handleCurrencies)

	// Sync.
	app.Post("/api/sync/push", authed, s.handlePush)
	app.Get("/api/sync/pull", authed, s.handlePull)
	app.Get("/api/sync/snapshot", authed, s.handleSnapshot)
	// Registered via the same constant the compression middleware skips on, so
	// renaming the route cannot silently start buffering the stream.
	app.Get(eventStreamPath, authed, s.handleEvents)

	// Import. Preview never writes; commit re-parses rather than trusting
	// anything the client remembers from the preview response — see
	// handlers_import.go.
	app.Post("/api/import/monefy/preview", authed, s.handleImportPreview)
	app.Post("/api/import/monefy/commit", authed, s.handleImportCommit)

	// Integrations. Tokens and webhooks are never synced — both are
	// integration secrets, same as fx_rate and OIDC client secrets
	// (docs/ARCHITECTURE.md §1).
	app.Get("/api/tokens", authed, s.handleListAPITokens)
	app.Post("/api/tokens", authed, s.handleCreateAPIToken)
	app.Delete("/api/tokens/:id", authed, s.handleDeleteAPIToken)
	app.Get("/api/webhooks", authed, s.handleListWebhooks)
	app.Post("/api/webhooks", authed, s.handleCreateWebhook)
	app.Delete("/api/webhooks/:id", authed, s.handleDeleteWebhook)

	// Export.
	app.Get("/api/export/transactions.csv", authed, s.handleExportCSV)

	// Admin. adminMW must run after authed — it reads userLocal, which only
	// authed populates.
	app.Get("/api/admin/users", authed, s.adminMW, s.handleListUsers)
	app.Post("/api/admin/users", authed, s.adminMW, s.handleAdminCreateUser)
	app.Delete("/api/admin/users/:id", authed, s.adminMW, s.handleAdminDeleteUser)
	app.Put("/api/admin/users/:id", authed, s.adminMW, s.handleSetAdmin)
	// Instance-wide settings (app/sync/fx) — never server.* or oidc.*, which
	// stay config.yaml/env-only. See internal/config's package doc.
	app.Get("/api/admin/settings", authed, s.adminMW, s.handleGetSettings)
	app.Put("/api/admin/settings", authed, s.adminMW, s.handleUpdateSettings)

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

// indexETag is the app shell's entity tag, computed once at startup.
//
// index.html is `no-cache`, which means *revalidate*, not "do not store" — but
// with nothing to revalidate against, every launch re-downloads the whole file.
// A tag turns that into a 304 with no body. Computed here rather than per
// request because the embedded FS cannot change while the process is running:
// its contents are compiled into the binary.
func indexETag(webFS fs.FS) string {
	data, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		return "" // No shell to serve; sendIndex reports it properly.
	}
	return fmt.Sprintf("%q", fmt.Sprintf("%x", sha256.Sum256(data)))
}

func (s *Server) sendIndex(c fiber.Ctx) error {
	data, err := fs.ReadFile(s.webFS, "index.html")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "web UI not built")
	}
	// `no-cache` stays: the shell names the hashed asset chunks, so serving a
	// stale one from cache pins the app to an old build. It has to be checked
	// every launch — but checking can cost a 304 instead of the whole file.
	c.Set("Cache-Control", "no-cache")
	if s.indexETag != "" {
		c.Set("ETag", s.indexETag)
		if match := c.Get("If-None-Match"); match != "" && strings.Contains(match, s.indexETag) {
			return c.SendStatus(fiber.StatusNotModified)
		}
	}
	c.Type("html")
	return c.Send(data)
}

// --- Concurrency-safe accessors ------------------------------------------------

func (s *Server) config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// setConfig swaps in a config a settings change has already validated and
// persisted (config.UpdateSettings) — this only ever updates the in-memory
// copy every request and background loop reads through s.config().
func (s *Server) setConfig(cfg *config.Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
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
