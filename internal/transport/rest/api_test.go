package rest

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/domain/seed"
)

func TestLogin_SetsHardenedCookie(t *testing.T) {
	ts := newTestServer(t, true)
	if _, err := ts.auth.CreateUser(context.Background(),
		auth.User{Email: "owner@example.test"}, "correct-horse", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	resp := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "owner@example.test", "password": "correct-horse"}, nil)
	expectStatus(t, resp, http.StatusNoContent)

	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			session = c
		}
	}
	resp.Body.Close()
	if session == nil {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly {
		t.Error("the session cookie must be HttpOnly")
	}
	if session.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", session.SameSite)
	}
	if session.Path != "/" {
		t.Errorf("path = %q", session.Path)
	}
	// Secure is set when the base URL is https; this test server is plain http, so
	// it would make the cookie unusable.
	if session.Secure {
		t.Error("Secure must follow the configured base URL scheme")
	}
}

func TestLogin_WrongPasswordAnd429(t *testing.T) {
	ts := newTestServer(t, true)
	if _, err := ts.auth.CreateUser(context.Background(),
		auth.User{Email: "owner@example.test"}, "correct-horse", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// An unknown email and a wrong password are indistinguishable to the client.
	wrong := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "owner@example.test", "password": "nope"}, nil)
	unknown := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "nobody@example.test", "password": "nope"}, nil)
	if wrong.StatusCode != http.StatusUnauthorized || unknown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("statuses %d / %d, want 401 for both", wrong.StatusCode, unknown.StatusCode)
	}
	wrongBody, unknownBody := bodyString(t, wrong), bodyString(t, unknown)
	// Bodies differ only in the request id.
	if strip := func(s string) string {
		if i := strings.Index(s, `"request_id"`); i > 0 {
			return s[:i]
		}
		return s
	}; strip(wrongBody) != strip(unknownBody) {
		t.Fatalf("responses must be identical, got:\n%s\n%s", wrongBody, unknownBody)
	}

	// Per-IP backoff: the limit is 3 in the test config.
	var limited *http.Response
	for i := 0; i < 8; i++ {
		resp := ts.do(http.MethodPost, "/api/v1/auth/login",
			map[string]string{"email": "owner@example.test", "password": "nope"}, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = resp
			break
		}
		resp.Body.Close()
	}
	if limited == nil {
		t.Fatal("repeated failures must eventually return 429")
	}
	if limited.Header.Get("Retry-After") == "" {
		t.Error("a 429 must carry Retry-After")
	}
	if n, err := strconv.Atoi(limited.Header.Get("Retry-After")); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", limited.Header.Get("Retry-After"))
	}
	limited.Body.Close()
}

func TestBootstrap_ForcesPasswordChangeOverHTTP(t *testing.T) {
	ts := newTestServer(t, true)
	if _, err := ts.auth.Bootstrap(context.Background(), "admin@example.test", "bootstrap-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	resp := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "admin@example.test", "password": "bootstrap-pass"}, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// /auth/me works, so the client can discover why it is being refused.
	var me UserDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/auth/me", nil, nil), &me)
	if !me.MustChangePassword {
		t.Fatal("must_change_password must be visible to the client")
	}

	// Everything else is 403 until the password changes.
	for _, path := range []string{
		"/api/v1/categories", "/api/v1/accounts", "/api/v1/transactions",
		"/api/v1/budgets?period=2026-07", "/api/v1/auth/sessions",
	} {
		resp := ts.do(http.MethodGet, path, nil, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s returned %d, want 403 before the password change", path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// The password change itself is allowed.
	resp = ts.do(http.MethodPost, "/api/v1/auth/password", map[string]string{
		"current_password": "bootstrap-pass", "new_password": "battery-staple",
	}, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// And now the application opens up.
	resp = ts.do(http.MethodGet, "/api/v1/categories", nil, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestLogout_EndsSession(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.do(http.MethodPost, "/api/v1/auth/logout", nil, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	resp = ts.do(http.MethodGet, "/api/v1/auth/me", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout /auth/me returned %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCategories_SeededSetOverHTTP(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var cats []CategoryDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories", nil, nil), &cats)
	if len(cats) != seed.CategoryCount {
		t.Fatalf("%d categories, want %d", len(cats), seed.CategoryCount)
	}
	essential := 0
	for _, c := range cats {
		if c.IsEssential {
			essential++
		}
	}
	if essential != seed.EssentialCount {
		t.Fatalf("%d essential categories, want %d", essential, seed.EssentialCount)
	}
	// Icons and colours survive the round trip, so the UI has something to draw.
	if cats[0].Icon == nil || cats[0].Color == nil {
		t.Errorf("the first category has no icon or colour: %+v", cats[0])
	}
}

func TestCategories_CRUDAndArchive(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var created CategoryDTO
	resp := ts.do(http.MethodPost, "/api/v1/categories", map[string]any{
		"name": "Pets", "kind": "expense", "is_essential": true, "icon": "🐈", "color": "#8899AA",
	}, nil)
	expectStatus(t, resp, http.StatusCreated)
	decode(t, resp, &created)
	if created.ID == 0 || created.Name != "Pets" || !created.IsEssential {
		t.Fatalf("created = %+v", created)
	}

	// A duplicate active name is a 409.
	resp = ts.do(http.MethodPost, "/api/v1/categories",
		map[string]any{"name": "Pets", "kind": "expense"}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate name returned %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()

	// Rename keeps identity.
	var renamed CategoryDTO
	resp = ts.do(http.MethodPatch, "/api/v1/categories/"+strconv.FormatInt(created.ID, 10),
		map[string]any{"name": "Animals", "kind": "expense"}, nil)
	expectStatus(t, resp, http.StatusOK)
	decode(t, resp, &renamed)
	if renamed.ID != created.ID || renamed.Name != "Animals" {
		t.Fatalf("renamed = %+v", renamed)
	}

	// Archiving removes it from the list but not from the database.
	resp = ts.do(http.MethodDelete, "/api/v1/categories/"+strconv.FormatInt(created.ID, 10), nil, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	var active []CategoryDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories", nil, nil), &active)
	for _, c := range active {
		if c.ID == created.ID {
			t.Fatal("an archived category must leave the default list")
		}
	}
	var all []CategoryDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories?include_archived=true", nil, nil), &all)
	found := false
	for _, c := range all {
		if c.ID == created.ID {
			found = true
			if c.ArchivedAt == nil {
				t.Error("archived_at must be reported")
			}
		}
	}
	if !found {
		t.Fatal("include_archived must return the archived row")
	}
}

func TestCategories_ValidationIsFieldLevel(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.do(http.MethodPost, "/api/v1/categories", map[string]any{"name": "", "kind": "nonsense"}, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	var problem Problem
	decode(t, resp, &problem)
	if problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", problem.Status)
	}
	if len(problem.Errors) < 2 {
		t.Fatalf("both fields must be reported, got %+v", problem.Errors)
	}
	fields := map[string]bool{}
	for _, e := range problem.Errors {
		fields[e.Field] = true
	}
	if !fields["name"] || !fields["kind"] {
		t.Fatalf("expected name and kind, got %+v", problem.Errors)
	}
}

func TestUnknownJSONFieldRejected(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	// A client typo must surface immediately rather than being silently ignored.
	resp := ts.raw(http.MethodPost, "/api/v1/categories",
		`{"name":"Pets","kind":"expense","is_esential":true}`)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	if body := bodyString(t, resp); !strings.Contains(body, "is_esential") {
		t.Fatalf("the response should name the offending field: %s", body)
	}
}

func TestAliases_ListAndCreate(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var aliases []AliasDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories/aliases", nil, nil), &aliases)
	if len(aliases) < 25 {
		t.Fatalf("%d seeded category aliases, expected the full §4.7 table", len(aliases))
	}
	byName := map[string]string{}
	for _, a := range aliases {
		byName[a.SourceName] = a.TargetName
	}
	for source, want := range map[string]string{
		"HotelTrip": "Hotel/Trip", "Clouth": "Clothes", "Счета": "Bills", "Studing": "Studying",
	} {
		if byName[source] != want {
			t.Errorf("alias %q -> %q, want %q", source, byName[source], want)
		}
	}

	// A new mapping, as the import mapping screen would create it.
	var cats []CategoryDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories", nil, nil), &cats)
	resp := ts.do(http.MethodPost, "/api/v1/categories/aliases", map[string]any{
		"category_id": cats[0].ID, "source_name": "Жилье",
	}, nil)
	expectStatus(t, resp, http.StatusCreated)
	var alias AliasDTO
	decode(t, resp, &alias)
	if alias.SourceName != "Жилье" || alias.TargetID != cats[0].ID {
		t.Fatalf("alias = %+v", alias)
	}

	resp = ts.do(http.MethodDelete, "/api/v1/categories/aliases/"+strconv.FormatInt(alias.ID, 10), nil, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestAccounts_SeededAndCreate(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var accounts []AccountDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/accounts", nil, nil), &accounts)
	if len(accounts) != len(seed.Accounts()) {
		t.Fatalf("%d accounts, want %d", len(accounts), len(seed.Accounts()))
	}
	byName := map[string]AccountDTO{}
	for _, a := range accounts {
		byName[a.Name] = a
	}
	if !byName["Cash"].IsComputed {
		t.Error("Cash is a computed parent and must be reported as one")
	}
	if byName["Cash EUR"].IsComputed {
		t.Error("a leaf must not be reported as computed")
	}
	if byName["Gold"].PriceTicker == nil || *byName["Gold"].PriceTicker != "XAU" {
		t.Errorf("Gold ticker = %+v, want XAU", byName["Gold"].PriceTicker)
	}
	if byName["Cash HUF"].Currency != "HUF" {
		t.Errorf("Cash HUF currency = %q", byName["Cash HUF"].Currency)
	}

	// Creating the account from the stage-02 demo.
	resp := ts.do(http.MethodPost, "/api/v1/accounts", map[string]any{
		"name": "Cash PLN", "asset_class": "cash", "currency": "EUR",
		"is_liquid": true, "counts_toward_net_worth": true,
	}, nil)
	expectStatus(t, resp, http.StatusCreated)
	var created AccountDTO
	decode(t, resp, &created)
	if created.ID == 0 || !created.IsLiquid {
		t.Fatalf("created = %+v", created)
	}
}

func TestUserIsolation_OverHTTP(t *testing.T) {
	ts := newTestServer(t, true)

	// User B owns some data.
	b := ts.login("b@example.test")
	var bCats []CategoryDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories", nil, nil), &bCats)
	var bAccounts []AccountDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/accounts", nil, nil), &bAccounts)
	bCategory := bCats[0]
	var bAccount AccountDTO
	for _, a := range bAccounts {
		if a.Name == "Cash EUR" {
			bAccount = a
		}
	}
	resp := ts.do(http.MethodPost, "/api/v1/transactions", map[string]any{
		"account_id": bAccount.ID, "category_id": bCategory.ID, "occurred_on": "2026-07-15",
		"kind": "expense", "amount": map[string]any{"amount_minor": 1250, "currency": "EUR", "exponent": 2},
	}, nil)
	expectStatus(t, resp, http.StatusCreated)
	var bTxn TransactionDTO
	decode(t, resp, &bTxn)

	// Now user A tries to reach it, on every endpoint that takes an id.
	ts.forget()
	ts.login("a@example.test")

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/transactions/" + strconv.FormatInt(bTxn.ID, 10), nil},
		{http.MethodDelete, "/api/v1/transactions/" + strconv.FormatInt(bTxn.ID, 10), nil},
		{http.MethodPatch, "/api/v1/transactions/" + strconv.FormatInt(bTxn.ID, 10),
			map[string]any{"description": "hijacked"}},
		{http.MethodPatch, "/api/v1/categories/" + strconv.FormatInt(bCategory.ID, 10),
			map[string]any{"name": "Hijacked", "kind": "expense"}},
		{http.MethodDelete, "/api/v1/categories/" + strconv.FormatInt(bCategory.ID, 10), nil},
		{http.MethodPatch, "/api/v1/accounts/" + strconv.FormatInt(bAccount.ID, 10),
			map[string]any{"name": "Hijacked", "asset_class": "cash", "currency": "EUR"}},
		{http.MethodDelete, "/api/v1/accounts/" + strconv.FormatInt(bAccount.ID, 10), nil},
		{http.MethodPut, "/api/v1/budgets/" + strconv.FormatInt(bCategory.ID, 10) + "/2026-07",
			map[string]any{"planned": map[string]any{"amount_minor": 1, "currency": "EUR", "exponent": 2}}},
		{http.MethodPost, "/api/v1/categories/" + strconv.FormatInt(bCategory.ID, 10) + "/merge",
			map[string]any{"into_id": bCategory.ID}},
	}
	for _, c := range cases {
		resp := ts.do(c.method, c.path, c.body, nil)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s %s returned %d; user A must not reach user B's row", c.method, c.path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// A's own lists are untouched by B's data.
	var aTxns map[string]any
	decode(t, ts.do(http.MethodGet, "/api/v1/transactions", nil, nil), &aTxns)
	if items, _ := aTxns["items"].([]any); len(items) != 0 {
		t.Fatalf("user A sees %d of user B's transactions", len(items))
	}

	// And B's transaction survived every attempt.
	ts.forget()
	resp = ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "b@example.test", "password": "correct-horse"}, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	var stillThere TransactionDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/transactions/"+strconv.FormatInt(bTxn.ID, 10), nil, nil), &stillThere)
	if stillThere.ID != bTxn.ID || stillThere.Amount.AmountMinor != 1250 {
		t.Fatalf("user B's transaction was damaged: %+v", stillThere)
	}
	_ = b
}
