// Package rest is the HTTP transport: handlers, DTOs and validation. It never
// imports internal/store — handlers call domain services (conventions §1).
package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/antlko/moneyapp/internal/config"
	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

// APIPrefix is the versioned API root.
const APIPrefix = "/api/v1"

// BuildInfo is what /version reports.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at,omitempty"`
}

// Deps is everything the transport layer needs, wired in cmd/moneyapp.
type Deps struct {
	Config config.Config
	Log    *slog.Logger
	Clock  clock.Clock
	Build  BuildInfo

	Readiness ReadinessChecker

	Auth         AuthService
	Categories   CategoryService
	Accounts     AccountService
	Transactions TransactionService
	Budgets      BudgetService
	FX           FXService
	Currencies   CurrencyService
	Imports      ImportService
	Metrics      MetricsService
	Capital      CapitalService
	Settings     SettingService
	Telegram     TelegramService

	// Actuals is the spend side of the budget report. It is a separate
	// collaborator because the report is a pure function over plan and actuals.
	Actuals     budget.ActualsSource
	Idempotency IdempotencyStore
}

// Server owns the Fiber app.
type Server struct {
	app     *fiber.App
	deps    Deps
	limiter *loginLimiter
}

// New builds the HTTP server: recover -> request id -> structured log ->
// security headers -> routes -> SPA fallback.
func New(deps Deps) *Server {
	app := fiber.New(fiber.Config{
		AppName:               "moneyapp",
		DisableStartupMessage: true,
		// Large enough for the multipart envelope around the biggest permitted
		// upload; the importer applies the exact cap and returns the 413 that
		// names it.
		BodyLimit:     bodyLimit(deps.Config.Imports.MaxUploadBytes),
		CaseSensitive: true,
		JSONEncoder:   json.Marshal,
		JSONDecoder:   json.Unmarshal,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			return writeProblem(c, deps.Log, err)
		},
	})

	app.Use(recover.New(recover.Config{EnableStackTrace: true}))
	app.Use(requestIDMiddleware())
	app.Use(loggingMiddleware(deps.Log))
	app.Use(securityHeaders())

	s := &Server{
		app:  app,
		deps: deps,
		limiter: newLoginLimiter(
			deps.Config.Auth.LoginRateLimit,
			deps.Config.Auth.LoginRateWindow,
			deps.Clock,
		),
	}
	s.routes()
	return s
}

// bodyLimit sizes fiber's request cap from the configured upload cap, leaving
// room for the multipart boundaries and headers.
func bodyLimit(maxUpload int64) int {
	const slack = 1 << 20
	const floor = 4 << 20
	limit := maxUpload + slack
	if limit < floor {
		limit = floor
	}
	return int(limit)
}

// App exposes the Fiber app for the listener in cmd/moneyapp.
func (s *Server) App() *fiber.App { return s.app }

// Handler adapts the app to net/http, which is what httptest drives.
func (s *Server) Handler() http.Handler { return adaptor.FiberApp(s.app) }

func (s *Server) routes() {
	// Operational endpoints are unauthenticated and outside /api/v1, so a probe
	// never depends on a session.
	s.app.Get("/healthz", s.handleHealthz)
	s.app.Get("/readyz", s.handleReadyz)
	s.app.Get("/version", s.handleVersion)

	api := s.app.Group(APIPrefix)
	s.registerAuthRoutes(api)
	s.registerTaxonomyRoutes(api)
	s.registerTransactionRoutes(api)
	s.registerBudgetRoutes(api)
	s.registerFXRoutes(api)
	if s.deps.Imports != nil {
		s.registerImportRoutes(api)
	}
	if s.deps.Metrics != nil {
		s.registerReportRoutes(api)
	}
	if s.deps.Capital != nil && s.deps.Metrics != nil {
		s.registerCapitalRoutes(api)
	}
	if s.deps.Settings != nil {
		s.registerSettingRoutes(api)
	}
	if s.deps.Telegram != nil {
		s.registerTelegramRoutes(api)
	}
	s.registerMetricRoutes(api)
	s.registerParityRoutes(api)

	// An unmatched API path is a JSON 404, never the SPA shell: a client asking
	// for a missing endpoint must not receive HTML with a 200.
	s.app.All(APIPrefix, s.handleAPINotFound)
	s.app.All(APIPrefix+"/*", s.handleAPINotFound)

	s.registerSPA()
}

func (s *Server) handleAPINotFound(c *fiber.Ctx) error {
	return sendProblem(c, http.StatusNotFound, Problem{
		Type:      ProblemBase + "not-found",
		Title:     "Not found",
		Status:    http.StatusNotFound,
		Detail:    "No such endpoint: " + c.Method() + " " + c.Path(),
		RequestID: requestID(c),
	})
}

func (s *Server) handleHealthz(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// handleReadyz fails closed while the schema is behind the binary, so a container
// started against an unmigrated database refuses traffic rather than writing
// against a schema it does not understand (docs/adr/0011-explicit-migrations.md).
func (s *Server) handleReadyz(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
	defer cancel()

	if s.deps.Readiness == nil {
		return s.notReady(c, "readiness check not configured", 0, 0)
	}
	current, expected, err := s.deps.Readiness.Ready(ctx)
	if err != nil {
		s.deps.Log.Error("readiness check failed", "request_id", requestID(c), "error", err.Error())
		return s.notReady(c, "database unreachable", current, expected)
	}
	if current < expected {
		return s.notReady(c, "migrations pending: run `moneyapp migrate up`", current, expected)
	}
	return c.JSON(fiber.Map{"status": "ready", "schema_version": current})
}

func (s *Server) notReady(c *fiber.Ctx, detail string, current, expected int64) error {
	return sendProblem(c, http.StatusServiceUnavailable, fiber.Map{
		"type":            ProblemBase + "not-ready",
		"title":           "Not ready",
		"status":          http.StatusServiceUnavailable,
		"detail":          detail,
		"schema_version":  current,
		"expected_schema": expected,
		"request_id":      requestID(c),
	})
}

func (s *Server) handleVersion(c *fiber.Ctx) error {
	return c.JSON(s.deps.Build)
}

// exponents loads the currency exponents for rendering a response. The set is
// cached in the currency service, so this is not a query per request.
func (s *Server) exponents(ctx context.Context) (map[string]int, error) {
	if s.deps.Currencies == nil {
		return map[string]int{}, nil
	}
	return s.deps.Currencies.Exponents(ctx)
}
