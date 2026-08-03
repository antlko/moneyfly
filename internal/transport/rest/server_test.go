package rest

import (
	"context"
	"encoding/json"

	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/config"
	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/seed"
	"github.com/antlko/moneyapp/internal/domain/setting"
	telegramdomain "github.com/antlko/moneyapp/internal/domain/telegram"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/provider"
	"github.com/antlko/moneyapp/internal/store"
)

var testNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

// testServer is the whole stack over a real migrated SQLite file: real router, real
// services, real database (conventions §9 — no mocks for the database).
type testServer struct {
	t      *testing.T
	http   *httptest.Server
	client *http.Client
	deps   Deps
	auth   *auth.Service
	fx     *fx.Service
}

func newTestServer(t *testing.T, migrate bool) *testServer {
	t.Helper()

	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "moneyapp.db")
	cfg.Server.BaseURL = "http://localhost:8080"
	cfg.Auth.BcryptCost = 10
	cfg.Auth.LoginRateLimit = 3
	cfg.Auth.LoginRateWindow = time.Minute
	cfg.Imports.UploadDir = filepath.Join(t.TempDir(), "uploads")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}

	db, err := store.Open(cfg.Database)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if migrate {
		if err := store.MigrateUp(context.Background(), db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	clk := clock.Fixed(testNow)
	currencies := currency.NewService(store.NewCurrencyRepo(db))
	categoryRepo := store.NewCategoryRepo(db)
	accountRepo := store.NewAccountRepo(db)
	txnRepo := store.NewTransactionRepo(db)

	categories := category.NewService(categoryRepo, clk)
	accounts := account.NewService(accountRepo, currencies, clk)
	fxSvc := fx.NewService(store.NewFxRepo(db), currencies)
	transactions := transaction.NewService(txnRepo, accounts, categories, currencies, fxSvc, clk)
	budgets := budget.NewService(store.NewBudgetRepo(db), categories, currencies, clk)
	settingRepo := store.NewSettingRepo(db)

	authSvc, err := auth.NewService(store.NewAuthRepo(db), clk, cfg.Auth.BcryptCost,
		cfg.Auth.SessionTTL, seed.NewProvisioner(categoryRepo, accountRepo, settingRepo, clk))
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}

	imports := importer.NewService(
		store.NewImportRepo(db), importer.NewDirFileStore(cfg.Imports.UploadDir),
		importer.MonefyFactory(currencies), categories, accounts, fxSvc, clk,
		cfg.Reports.BaseCurrency, cfg.Imports.MaxUploadBytes,
	)

	deps := Deps{
		Config:       cfg,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:        clk,
		Build:        BuildInfo{Version: "test", Commit: "abc1234"},
		Readiness:    store.NewReadiness(db),
		Auth:         authSvc,
		Categories:   categories,
		Accounts:     accounts,
		Transactions: transactions,
		Budgets:      budgets,
		FX:           fxSvc,
		Currencies:   currencies,
		Imports:      imports,
		Metrics:      metrics.NewService(store.NewMetricsRepo(db), categories),
		Capital:      capital.NewService(store.NewSnapshotRepo(db), accounts, fxSvc, currencies, clk),
		// No reachable provider: the test server exercises the outage path, which
		// is the one that must never break a render.
		Settings: setting.NewService(settingRepo,
			fx.NewSettingFetcher(
				fx.NewRefresher(fxSvc, []fx.Provider{
					provider.NewOpenErAPI(provider.NewClient(), "http://127.0.0.1:1/"),
				}, 0.15, []string{"USD", "HUF", "UAH"}),
				fxSvc, clk),
			settingRepo, clk),
		Telegram:    telegramdomain.NewService(store.NewTelegramRepo(db), clk),
		Actuals:     txnRepo,
		Idempotency: store.NewIdempotencyRepo(db),
	}

	srv := httptest.NewServer(New(deps).Handler())
	t.Cleanup(srv.Close)

	jar := &cookieJar{cookies: map[string]string{}}
	return &testServer{
		t: t, http: srv, deps: deps, auth: authSvc, fx: fxSvc,
		client: &http.Client{Jar: jar, Timeout: 10 * time.Second},
	}
}

// seedRate stores one EUR-based rate. There is no endpoint for writing rates
// until stage 07, so the tests go through the domain service.
func (ts *testServer) seedRate(quote, value, asOf string) error {
	rate, err := fx.ParseRate(value)
	if err != nil {
		return err
	}
	on, err := time.Parse("2006-01-02", asOf)
	if err != nil {
		return err
	}
	_, err = ts.fx.Upsert(context.Background(), fx.Rate{
		AsOf: on.UTC(), Base: fx.StorageBase, Quote: quote, Rate: rate, Source: "test",
	})
	return err
}

// cookieJar keeps the session cookie across requests. net/http/cookiejar refuses
// to store host-only cookies for "127.0.0.1" the way the real browser would, so a
// minimal jar is clearer than working around it.
type cookieJar struct{ cookies map[string]string }

func (j *cookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	for _, c := range cookies {
		if c.Value == "" {
			delete(j.cookies, c.Name)
			continue
		}
		j.cookies[c.Name] = c.Value
	}
}

func (j *cookieJar) Cookies(_ *url.URL) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(j.cookies))
	for name, value := range j.cookies {
		out = append(out, &http.Cookie{Name: name, Value: value})
	}
	return out
}

// forget drops the session cookie, simulating a client with no session.
func (ts *testServer) forget() {
	ts.client.Jar = &cookieJar{cookies: map[string]string{}}
}

// login creates a provisioned user and authenticates the client as them.
func (ts *testServer) login(email string) auth.User {
	ts.t.Helper()
	u, err := ts.auth.CreateUser(context.Background(),
		auth.User{Email: email, Role: auth.RoleUser}, "correct-horse", false)
	if err != nil {
		ts.t.Fatalf("creating user: %v", err)
	}
	resp := ts.do(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": email, "password": "correct-horse",
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		ts.t.Fatalf("login returned %d", resp.StatusCode)
	}
	return u
}

// do performs a request against the test server. body may be nil.
func (ts *testServer) do(method, path string, body any, headers map[string]string) *http.Response {
	ts.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			ts.t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequest(method, ts.http.URL+path, reader)
	if err != nil {
		ts.t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ts.client.Do(req)
	if err != nil {
		ts.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// raw performs a request with a literal string body, for malformed-JSON cases.
func (ts *testServer) raw(method, path, body string) *http.Response {
	ts.t.Helper()
	req, err := http.NewRequest(method, ts.http.URL+path, strings.NewReader(body))
	if err != nil {
		ts.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.client.Do(req)
	if err != nil {
		ts.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// decode reads a JSON response body into dst and closes it.
func decode(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decoding %s: %v", resp.Request.URL.Path, err)
	}
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(buf)
}

func expectStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s %s returned %d, want %d: %s",
			resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, bodyString(t, resp))
	}
}

func TestHealthz(t *testing.T) {
	ts := newTestServer(t, true)
	resp := ts.do(http.MethodGet, "/healthz", nil, nil)
	expectStatus(t, resp, http.StatusOK)
	var got map[string]string
	decode(t, ts.do(http.MethodGet, "/healthz", nil, nil), &got)
	if got["status"] != "ok" {
		t.Fatalf("healthz = %+v", got)
	}
}

func TestVersion(t *testing.T) {
	ts := newTestServer(t, true)
	var got BuildInfo
	decode(t, ts.do(http.MethodGet, "/version", nil, nil), &got)
	if got.Version != "test" || got.Commit != "abc1234" {
		t.Fatalf("version = %+v", got)
	}
}

func TestReadyz_UnmigratedReturns503(t *testing.T) {
	ts := newTestServer(t, false)

	resp := ts.do(http.MethodGet, "/readyz", nil, nil)
	expectStatus(t, resp, http.StatusServiceUnavailable)

	// The body says what to do about it.
	var problem map[string]any
	decode(t, ts.do(http.MethodGet, "/readyz", nil, nil), &problem)
	if detail, _ := problem["detail"].(string); !strings.Contains(detail, "migrate up") {
		t.Fatalf("the 503 must name the remedy, got %q", detail)
	}

	// Liveness is independent of readiness: the process is up either way.
	expectStatus(t, ts.do(http.MethodGet, "/healthz", nil, nil), http.StatusOK)
}

func TestReadyz_MigratedReturnsOK(t *testing.T) {
	ts := newTestServer(t, true)
	var got map[string]any
	decode(t, ts.do(http.MethodGet, "/readyz", nil, nil), &got)
	if got["status"] != "ready" {
		t.Fatalf("readyz = %+v", got)
	}
	if v, ok := got["schema_version"].(float64); !ok || int64(v) != store.ExpectedSchemaVersion() {
		t.Fatalf("schema_version = %v, want %d", got["schema_version"], store.ExpectedSchemaVersion())
	}
}

func TestSPAFallback(t *testing.T) {
	ts := newTestServer(t, true)

	// A client-side route serves the shell, so a reload on /budget works.
	resp := ts.do(http.MethodGet, "/some/deep/route", nil, nil)
	expectStatus(t, resp, http.StatusOK)
	body := bodyString(t, resp)
	if !strings.Contains(body, "<!doctype html") && !strings.Contains(body, "<!DOCTYPE html") {
		t.Fatalf("expected the SPA shell, got: %.120s", body)
	}

	// An unknown API path is a JSON 404, never HTML with a 200.
	resp = ts.do(http.MethodGet, "/api/v1/nope", nil, nil)
	expectStatus(t, resp, http.StatusNotFound)
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "problem+json") {
		t.Fatalf("content type = %q, want problem+json", ct)
	}
	var problem Problem
	decode(t, ts.do(http.MethodGet, "/api/v1/nope", nil, nil), &problem)
	if problem.Status != http.StatusNotFound || problem.Type == "" {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestSecurityHeadersAndRequestID(t *testing.T) {
	ts := newTestServer(t, true)
	resp := ts.do(http.MethodGet, "/healthz", nil, nil)
	defer resp.Body.Close()

	if resp.Header.Get(HeaderRequestID) == "" {
		t.Error("every response must carry a request id")
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	// Self-only CSP: every asset is embedded, so there is no CDN to allow.
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestUnauthenticated_Returns401(t *testing.T) {
	ts := newTestServer(t, true)
	for _, path := range []string{
		"/api/v1/categories", "/api/v1/accounts", "/api/v1/transactions",
		"/api/v1/budgets?period=2026-07", "/api/v1/reports/budget?period=2026-07",
		"/api/v1/auth/me",
	} {
		resp := ts.do(http.MethodGet, path, nil, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session returned %d, want 401", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
