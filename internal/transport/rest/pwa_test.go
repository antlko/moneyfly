package rest

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestManifest_Served(t *testing.T) {
	ts := newTestServer(t, true)

	resp := ts.do(http.MethodGet, "/manifest.json", nil, nil)
	expectStatus(t, resp, http.StatusOK)

	var manifest struct {
		Name            string `json:"name"`
		ShortName       string `json:"short_name"`
		StartURL        string `json:"start_url"`
		Display         string `json:"display"`
		ThemeColor      string `json:"theme_color"`
		BackgroundColor string `json:"background_color"`
		Icons           []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}
	defer resp.Body.Close()

	if manifest.Name == "" || manifest.ShortName == "" {
		t.Error("name and short_name are required for an install prompt")
	}
	if manifest.Display != "standalone" {
		t.Errorf("display = %q, want standalone — no browser chrome", manifest.Display)
	}
	if manifest.StartURL != "/" {
		t.Errorf("start_url = %q, want /", manifest.StartURL)
	}
	if manifest.ThemeColor == "" || manifest.BackgroundColor == "" {
		t.Error("theme_color and background_color are what stop a white flash on launch")
	}

	sizes := map[string]bool{}
	maskable := 0
	for _, icon := range manifest.Icons {
		sizes[icon.Sizes] = true
		if icon.Purpose == "maskable" {
			maskable++
		}
	}
	if !sizes["192x192"] || !sizes["512x512"] {
		t.Errorf("icons = %v, want both 192 and 512", sizes)
	}
	if maskable == 0 {
		t.Error("no maskable icon; Android will letterbox the tile")
	}

	// And the icons themselves are actually served.
	for _, path := range []string{"/icon-192.png", "/icon-512.png", "/apple-touch-icon.png"} {
		iconResp := ts.do(http.MethodGet, path, nil, nil)
		if iconResp.StatusCode != http.StatusOK {
			t.Errorf("%s returned %d", path, iconResp.StatusCode)
		}
		iconResp.Body.Close()
	}
}

func TestServiceWorker_CachesShellOnly(t *testing.T) {
	ts := newTestServer(t, true)

	resp := ts.do(http.MethodGet, "/sw.js", nil, nil)
	expectStatus(t, resp, http.StatusOK)
	body := bodyString(t, resp)

	// The one rule that matters: a stale balance shown as current would be worse
	// than no balance at all.
	if !strings.Contains(body, "/api/") {
		t.Fatal("the worker does not mention the API path; it must exclude it explicitly")
	}
	if !strings.Contains(body, "isAPI") {
		t.Error("no API exclusion is visible in the worker")
	}
	// Old caches are cleaned on activate, so a deploy cannot leave two shells.
	if !strings.Contains(body, "caches.delete") {
		t.Error("the worker never deletes an old cache version")
	}
	// A navigation falls back to the shell, which is what makes the app open
	// offline at all.
	if !strings.Contains(body, "navigate") {
		t.Error("no navigation fallback")
	}
	// /version reports the running build. A cached copy would keep claiming the
	// old one after a deploy — which is the one question it exists to answer.
	if !strings.Contains(body, "/version") {
		t.Error("/version is not excluded from the cache")
	}
}

func TestSPA_ServesShellForClientRoutes(t *testing.T) {
	ts := newTestServer(t, true)

	for _, path := range []string{"/", "/budget", "/capital", "/import", "/settings"} {
		resp := ts.do(http.MethodGet, path, nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s returned %d", path, resp.StatusCode)
		}
		body := bodyString(t, resp)
		if !strings.Contains(body, "<div id=\"app\">") {
			t.Errorf("%s did not return the app shell", path)
		}
		if !strings.Contains(body, "manifest.json") {
			t.Errorf("%s does not link the manifest, so it cannot be installed", path)
		}
	}

	// An unknown API path is still JSON, never the shell.
	resp := ts.do(http.MethodGet, APIPrefix+"/nope", nil, nil)
	expectStatus(t, resp, http.StatusNotFound)
	if body := bodyString(t, resp); strings.Contains(body, "<div id=\"app\">") {
		t.Fatal("an API 404 returned HTML")
	}
}

func TestMetricsAPI_ValidateAndEvaluate(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	// A valid expression over registered identifiers.
	resp := ts.do(http.MethodPost, APIPrefix+"/metrics/validate",
		MetricExpressionDTO{Expression: "ready_for_usage / possible_minimum"}, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// An unknown identifier is a field error naming it, not a 500.
	resp = ts.do(http.MethodPost, APIPrefix+"/metrics/validate",
		MetricExpressionDTO{Expression: "mystery / 2"}, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	if body := bodyString(t, resp); !strings.Contains(body, "mystery") {
		t.Fatalf("body = %s, want the offending identifier named", body)
	}

	// Anything outside the grammar is refused.
	for _, expression := range []string{"exec('rm -rf /')", "1 +", "$(whoami)"} {
		resp = ts.do(http.MethodPost, APIPrefix+"/metrics/validate",
			MetricExpressionDTO{Expression: expression}, nil)
		expectStatus(t, resp, http.StatusUnprocessableEntity)
		resp.Body.Close()
	}

	// Evaluation, including the division-by-zero rule.
	spend, income := 1200.0, 0.0
	var out struct {
		Value *float64 `json:"value"`
	}
	decode(t, ts.do(http.MethodPost, APIPrefix+"/metrics/evaluate",
		MetricExpressionDTO{
			Expression: "spend_total / income",
			Values:     map[string]*float64{"spend_total": &spend, "income": &income},
		}, nil), &out)
	if out.Value != nil {
		t.Fatalf("value = %v, want null for a division by zero", *out.Value)
	}
}

func TestMetricsAPI_RegistryIsPublished(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var registry MetricRegistryDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/metrics", nil, nil), &registry)
	if len(registry.Identifiers) == 0 || len(registry.Functions) == 0 {
		t.Fatal("the registry is empty; the editor has nothing to offer")
	}
	for _, want := range []string{"min", "max", "abs", "mean", "count"} {
		found := false
		for _, got := range registry.Functions {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("function %q is missing from the registry", want)
		}
	}
}

func TestExport_TransactionsCSV(t *testing.T) {
	ts := newTestServer(t, true)
	u := ts.login("owner@example.test")
	seedYear(t, ts, u)

	resp := ts.do(http.MethodGet, APIPrefix+"/export/transactions.csv", nil, nil)
	expectStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("content type = %q, want text/csv", got)
	}
	body := bodyString(t, resp)

	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("rows = %d, want a header and the seeded transactions", len(rows))
	}
	if rows[0][0] != "date" || rows[0][1] != "account" {
		t.Fatalf("header = %v, want the raw columns", rows[0])
	}
	// Five transactions were seeded.
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6 (header plus five transactions)", len(rows))
	}
}

// TestI18n_CyrillicRenders guards the real data: the imported history contains
// Cyrillic descriptions, and they must survive every hop regardless of UI
// language.
func TestI18n_CyrillicRenders(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var batch BatchDTO
	decode(t, ts.upload("bom-crlf.csv"), &batch)

	var rows []ImportRowDTO
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/imports/"+itoa(batch.ID)+"/rows", nil, nil), &rows)

	found := false
	for _, row := range rows {
		if row.Description != nil && strings.Contains(*row.Description, "Продукты") {
			found = true
			// The trailing space is part of the natural key and must survive too.
			if *row.Description != "Продукты " {
				t.Fatalf("description = %q, want %q with its trailing space",
					*row.Description, "Продукты ")
			}
		}
	}
	if !found {
		t.Fatal("the Cyrillic description did not survive the round trip")
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// TestParityPage_ListsAllDeviations is the cutover criterion made visible: the
// workbook is retired when this is clean, and every divergence has to be one of
// the documented ones.
func TestParityPage_ListsAllDeviations(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var report ParityReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/parity", nil, nil), &report)

	if !report.Matched {
		t.Fatal("the parity gate is not clean")
	}
	if report.VerifiedBy == "" {
		t.Error("the report must say what verified it")
	}

	byID := map[string]DeviationDTO{}
	for _, d := range report.Deviations {
		byID[d.ID] = d
	}
	for i := 1; i <= 11; i++ {
		id := "D" + strconv.Itoa(i)
		d, ok := byID[id]
		if !ok {
			t.Errorf("%s is missing from the report", id)
			continue
		}
		if d.Sheet == "" || d.Corrected == "" || d.Evidence == "" {
			t.Errorf("%s is listed without an explanation: %+v", id, d)
		}
	}
	if len(report.Deviations) != 11 {
		t.Fatalf("deviations = %d, want the 11 documented ones", len(report.Deviations))
	}
}
