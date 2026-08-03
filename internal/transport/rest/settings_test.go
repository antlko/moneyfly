package rest

import (
	"net/http"
	"testing"
)

func settingsByKey(t *testing.T, ts *testServer) map[string]SettingDTO {
	t.Helper()
	var list []SettingDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/settings", nil, nil), &list)
	out := map[string]SettingDTO{}
	for _, s := range list {
		out[s.Key] = s
	}
	return out
}

func TestSettingsAPI_ListsSeededSettings(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	byKey := settingsByKey(t, ts)
	for _, key := range []string{
		"threshold.warn_percent", "threshold.over_multiplier",
		"report.base_currency", "fx.EUR_UAH", "price.XAU",
	} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("setting %q is not seeded", key)
		}
	}
	if got := byKey["threshold.warn_percent"]; got.EffectiveValue == nil || *got.EffectiveValue != "10" {
		t.Errorf("warn percent = %+v, want the workbook's 10", got.EffectiveValue)
	}
	// An auto setting that has never been fetched reads as stale, which is what
	// puts the badge on the screen.
	if got := byKey["fx.EUR_UAH"]; !got.Stale {
		t.Error("a never-fetched auto setting must read as stale")
	}
	// Gold stays manual until the quantity behind `Gold = 3000` is supplied.
	if got := byKey["price.XAU"]; got.Mode != "manual" {
		t.Errorf("price.XAU = %q, want manual", got.Mode)
	}
}

func TestSettingsAPI_ThresholdChangeAppliesWithoutRestart(t *testing.T) {
	ts := newTestServer(t, true)
	u := ts.login("owner@example.test")
	seedYear(t, ts, u)

	// Food: 200.00 spent against a 150.00 plan in February — over at any band.
	// January spent 100.00 against 150.00, which is 66% and well within.
	var report BudgetReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/budget?period=2026-01", nil, nil), &report)
	stateOf := func(r BudgetReportDTO) string {
		for _, row := range r.Categories {
			if row.Name == "Food" {
				return row.State
			}
		}
		return ""
	}
	if got := stateOf(report); got != "within" {
		t.Fatalf("state = %q at a 10%% band, want within", got)
	}

	// Widen the amber band far enough to catch 66%.
	wide := "40"
	resp := ts.do(http.MethodPatch, APIPrefix+"/settings/threshold.warn_percent",
		SettingInputDTO{ManualValue: &wide}, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/budget?period=2026-01", nil, nil), &report)
	if got := stateOf(report); got != "approaching" {
		t.Fatalf("state = %q after widening the band, want approaching — with no restart", got)
	}
}

func TestSettingsAPI_ManualOverrideAndReset(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	manual := "51.00"
	var updated SettingDTO
	decode(t, ts.do(http.MethodPatch, APIPrefix+"/settings/fx.EUR_UAH",
		SettingInputDTO{ManualValue: &manual}, nil), &updated)
	if updated.EffectiveValue == nil || *updated.EffectiveValue != "51.00" {
		t.Fatalf("effective = %+v, want the manual 51.00", updated.EffectiveValue)
	}

	decode(t, ts.do(http.MethodPatch, APIPrefix+"/settings/fx.EUR_UAH",
		SettingInputDTO{ClearManual: true}, nil), &updated)
	if updated.ManualValue != nil && *updated.ManualValue != "" {
		t.Fatalf("manual value = %+v, want it cleared", updated.ManualValue)
	}
}

func TestSettingsAPI_RefreshWithNoNetworkKeepsWorking(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	// The test server has no reachable provider, which is exactly the outage
	// case: the request succeeds, the error is recorded, and nothing breaks.
	var refreshed SettingDTO
	decode(t, ts.do(http.MethodPost, APIPrefix+"/settings/fx.EUR_UAH/refresh", nil, nil), &refreshed)
	if refreshed.LastError == nil || *refreshed.LastError == "" {
		t.Fatal("a failed fetch must be recorded")
	}
	if !refreshed.Stale {
		t.Error("a setting that has never fetched successfully is stale")
	}

	// And every report still renders.
	resp := ts.do(http.MethodGet, APIPrefix+"/reports/capital?period=2026-02", nil, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = ts.do(http.MethodGet, APIPrefix+"/reports/budget?period=2026-02", nil, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestSettingsAPI_RefreshingAManualSettingIsRefused(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.do(http.MethodPost, APIPrefix+"/settings/price.XAU/refresh", nil, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

func TestSettingsAPI_ListsProviders(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var providers []ProviderDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/providers", nil, nil), &providers)
	if len(providers) < 2 {
		t.Fatalf("providers = %d, want at least the two FX sources", len(providers))
	}
	for _, p := range providers {
		if p.Kind == "broker" {
			t.Fatal("a broker provider exists; Trading212 was dropped, not deferred")
		}
	}
}

func TestSettingsAPI_UnknownKeyIs404(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	value := "1"
	resp := ts.do(http.MethodPatch, APIPrefix+"/settings/nope.not.a.key",
		SettingInputDTO{ManualValue: &value}, nil)
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestSettingsAPI_UserIsolation(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	manual := "42"
	resp := ts.do(http.MethodPatch, APIPrefix+"/settings/threshold.warn_percent",
		SettingInputDTO{ManualValue: &manual}, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	ts.forget()
	ts.login("other@example.test")
	byKey := settingsByKey(t, ts)
	if got := byKey["threshold.warn_percent"]; got.EffectiveValue == nil || *got.EffectiveValue != "10" {
		t.Fatalf("the other user sees %+v, want their own seeded 10", got.EffectiveValue)
	}
}

func TestCapitalAPI_ValuationModes(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var contemporaneous, constant CapitalReportDTO
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/capital/series?from=2026-01&to=2026-02", nil, nil), &struct{}{})

	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/capital?period=2026-01&valuation=contemporaneous", nil, nil), &contemporaneous)
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/capital?period=2026-01&valuation=constant", nil, nil), &constant)

	if contemporaneous.Valuation != "contemporaneous" || constant.Valuation != "constant" {
		t.Fatalf("valuations = %q / %q", contemporaneous.Valuation, constant.Valuation)
	}
	// January holds 40,000 Ft. At January's rate that is 100.00 EUR; at
	// February's — which `constant` uses for a range ending in January — it is
	// still January's, so the two agree here and differ over a wider range.
	if contemporaneous.General == nil || constant.General == nil {
		t.Fatal("both modes must produce a figure")
	}

	resp := ts.do(http.MethodGet, APIPrefix+"/reports/capital?period=2026-01&valuation=vibes", nil, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

// TestValuation_ConstantMatchesWorkbook is the deviation D8 demonstration: over a
// range, revaluing everything at the latest rate — which is what the sheet's
// single undated rate cell did — gives a different history from valuing each
// month at its own rate.
func TestValuation_ConstantMatchesWorkbook(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var contemporaneous, constant CapitalSeriesDTO
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/capital/series?from=2026-01&to=2026-02&valuation=contemporaneous",
		nil, nil), &contemporaneous)
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/capital/series?from=2026-01&to=2026-02&valuation=constant",
		nil, nil), &constant)

	january := func(s CapitalSeriesDTO) int64 {
		for _, p := range s.Points {
			if p.Period == "2026-01" && p.General != nil {
				return p.General.AmountMinor
			}
		}
		t.Fatal("January is missing from the series")
		return 0
	}
	// January: 1000 EUR plus 40,000 Ft. At January's 400/EUR that is 1100.00; at
	// February's 500/EUR it is 1080.00.
	if got := january(contemporaneous); got != 110000 {
		t.Fatalf("contemporaneous January = %d, want 110000 at January's own rate", got)
	}
	if got := january(constant); got != 108000 {
		t.Fatalf("constant January = %d, want 108000 — revalued at February's rate, as the sheet did", got)
	}
}
