package rest

import (
	"math"
	"net/http"
	"strconv"
	"testing"
)

// seedCapital records two months of balances across three currencies, so the
// roll-ups, the allocation and the real/FX split all have something to say.
func seedCapital(t *testing.T, ts *testServer) map[string]int64 {
	t.Helper()

	var accounts []AccountDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/accounts", nil, nil), &accounts)
	byName := map[string]int64{}
	for _, a := range accounts {
		byName[a.Name] = a.ID
	}

	// The rate moves between the two months, which is what the split is for.
	for period, huf := range map[string]string{"2026-01": "400", "2026-02": "500"} {
		last := map[string]string{"2026-01": "2026-01-31", "2026-02": "2026-02-28"}[period]
		if err := ts.seedRate("HUF", huf, last); err != nil {
			t.Fatalf("seeding rate: %v", err)
		}
	}

	post := func(period string, items []map[string]any) {
		resp := ts.do(http.MethodPost, APIPrefix+"/snapshots/bulk", map[string]any{
			"period": period, "items": items,
		}, nil)
		expectStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	post("2026-01", []map[string]any{
		{"account_id": byName["Cash EUR"], "amount": map[string]any{
			"amount_minor": 100000, "currency": "EUR", "exponent": 2}},
		{"account_id": byName["Cash HUF"], "amount": map[string]any{
			"amount_minor": 40000, "currency": "HUF", "exponent": 0}},
	})
	post("2026-02", []map[string]any{
		{"account_id": byName["Cash EUR"], "amount": map[string]any{
			"amount_minor": 120000, "currency": "EUR", "exponent": 2}},
		{"account_id": byName["Cash HUF"], "amount": map[string]any{
			"amount_minor": 40000, "currency": "HUF", "exponent": 0}},
	})
	return byName
}

func TestCapitalAPI_NetWorthAndAllocation(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var report CapitalReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/capital?period=2026-02", nil, nil), &report)

	// 1200.00 EUR + 40,000 Ft at 500/EUR = 80.00 EUR.
	if report.General == nil || report.General.AmountMinor != 128000 {
		t.Fatalf("general = %+v, want 1280.00", report.General)
	}
	if report.ReadyForUsage == nil || report.ReadyForUsage.AmountMinor != 128000 {
		t.Fatalf("ready for usage = %+v, want 1280.00 — both accounts are liquid", report.ReadyForUsage)
	}

	sum := 0.0
	for _, share := range report.Allocation {
		sum += share.Share
		if share.Name == "Cash" {
			t.Error("the allocation includes the computed parent Cash")
		}
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Fatalf("allocation sums to %.12f, want exactly 1.0", sum)
	}

	// 80 of 1280 is 6.25%. Dividing raw forint by euros, as the sheet did, would
	// give 3125%.
	for _, share := range report.Allocation {
		if share.Name != "Cash HUF" {
			continue
		}
		if math.Abs(share.Share-0.0625) > 1e-9 {
			t.Fatalf("Cash HUF share = %.6f, want 0.0625", share.Share)
		}
	}
}

func TestCapitalAPI_ChangeSplitsRealAndFX(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var report CapitalReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/capital?period=2026-02", nil, nil), &report)

	if !report.Change.Recorded {
		t.Fatal("February has a January to compare against")
	}
	// January: 1000 + (40,000 / 400 = 100) = 1100. February: 1200 + 80 = 1280.
	if report.Change.Total == nil || report.Change.Total.AmountMinor != 18000 {
		t.Fatalf("total change = %+v, want 180.00", report.Change.Total)
	}
	// The 200 EUR added is real; the forint holding lost 20 EUR to the rate.
	if report.Change.Real == nil || report.Change.Real.AmountMinor != 20000 {
		t.Fatalf("real change = %+v, want 200.00 saved", report.Change.Real)
	}
	if report.Change.FX == nil || report.Change.FX.AmountMinor != -2000 {
		t.Fatalf("fx change = %+v, want -20.00", report.Change.FX)
	}
}

func TestCapitalAPI_SnapshotsPrefillFromLastMonth(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var rows []SnapshotDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/snapshots?period=2026-03", nil, nil), &rows)

	var cashEUR *SnapshotDTO
	for i := range rows {
		if rows[i].AccountName == "Cash EUR" {
			cashEUR = &rows[i]
		}
	}
	if cashEUR == nil {
		t.Fatal("Cash EUR is missing from the snapshot screen")
	}
	if cashEUR.Amount != nil {
		t.Fatalf("March has no figure yet, got %+v", cashEUR.Amount)
	}
	if cashEUR.PreviousAmount == nil || cashEUR.PreviousAmount.AmountMinor != 120000 {
		t.Fatalf("previous amount = %+v, want February's 1200.00", cashEUR.PreviousAmount)
	}

	// The computed parent is listed but marked as such.
	for _, row := range rows {
		if row.AccountName == "Cash" && !row.Computed {
			t.Fatal("Cash is a computed parent and must say so")
		}
	}
}

func TestCapitalAPI_ParentRejectsDirectWrite(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	byName := seedCapital(t, ts)

	resp := ts.do(http.MethodPut,
		APIPrefix+"/snapshots/"+strconv.FormatInt(byName["Cash"], 10)+"/2026-02",
		map[string]any{"amount": map[string]any{
			"amount_minor": 100000, "currency": "EUR", "exponent": 2}}, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

func TestCapitalAPI_BurnModeSwitches(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var report CapitalReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/capital?period=2026-02", nil, nil), &report)
	if report.BurnMode != "actual_trailing_3" {
		t.Fatalf("default burn mode = %q, want actual_trailing_3", report.BurnMode)
	}

	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/capital?period=2026-02&burn_mode=legacy_blend", nil, nil), &report)
	if report.BurnMode != "legacy_blend" {
		t.Fatalf("burn mode = %q, want legacy_blend", report.BurnMode)
	}

	resp := ts.do(http.MethodGet,
		APIPrefix+"/reports/capital?period=2026-02&burn_mode=vibes", nil, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

func TestCapitalAPI_NetWorthInAnotherCurrency(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var body struct {
		Currency string    `json:"currency"`
		General  *MoneyDTO `json:"general"`
		Value    *MoneyDTO `json:"value"`
	}
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/net-worth-in/huf?period=2026-02", nil, nil), &body)

	if body.Currency != "HUF" {
		t.Fatalf("currency = %q, want HUF", body.Currency)
	}
	// 1280.00 EUR at 500 Ft/EUR is 640,000 Ft — and HUF has no decimals.
	if body.Value == nil || body.Value.AmountMinor != 640000 {
		t.Fatalf("value = %+v, want 640000 forint", body.Value)
	}
	if body.Value.Exponent != 0 {
		t.Fatalf("exponent = %d, want 0 for HUF", body.Value.Exponent)
	}

	resp := ts.do(http.MethodGet, APIPrefix+"/reports/net-worth-in/zzz?period=2026-02", nil, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

func TestCapitalAPI_ReconciliationReportsDrift(t *testing.T) {
	ts := newTestServer(t, true)
	u := ts.login("owner@example.test")
	byName := seedCapital(t, ts)
	_ = u

	var cats []CategoryDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/categories", nil, nil), &cats)
	var food int64
	for _, c := range cats {
		if c.Name == "Food" {
			food = c.ID
		}
	}
	resp := ts.do(http.MethodPost, APIPrefix+"/transactions", map[string]any{
		"account_id": byName["Cash EUR"], "category_id": food,
		"occurred_on": "2026-02-10", "kind": "expense",
		"amount": map[string]any{"amount_minor": 5000, "currency": "EUR", "exponent": 2},
	}, nil)
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	var body struct {
		Note  string     `json:"note"`
		Items []DriftDTO `json:"items"`
	}
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/snapshots/2026-02/reconciliation", nil, nil), &body)

	var row *DriftDTO
	for i := range body.Items {
		if body.Items[i].AccountID == byName["Cash EUR"] {
			row = &body.Items[i]
		}
	}
	if row == nil {
		t.Fatal("Cash EUR is missing from the reconciliation")
	}
	if row.Implied == nil || row.Implied.AmountMinor != -5000 {
		t.Fatalf("implied = %+v, want -50.00 from the one expense", row.Implied)
	}
	if row.Difference == nil || row.Difference.AmountMinor != 125000 {
		t.Fatalf("difference = %+v, want 1250.00", row.Difference)
	}

	// And the snapshot is untouched: drift is reported, never corrected.
	var report CapitalReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/capital?period=2026-02", nil, nil), &report)
	if report.General == nil || report.General.AmountMinor != 128000 {
		t.Fatalf("general = %+v, want the snapshot's 1280.00", report.General)
	}
}

func TestCapitalAPI_UnrecordedMonthIsNull(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var report CapitalReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/capital?period=2026-05", nil, nil), &report)
	if report.General != nil || report.ReadyForUsage != nil {
		t.Fatalf("an unrecorded month must be null throughout, got %+v", report)
	}
	if report.Change.Recorded {
		t.Fatal("there is no change to report for a month with no balances")
	}
	if report.RunwayMonths != nil {
		t.Fatalf("runway = %v, want nil", *report.RunwayMonths)
	}
}

func TestCapitalAPI_SeriesCoversTheRange(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	seedCapital(t, ts)

	var series CapitalSeriesDTO
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/capital/series?from=2026-01&to=2026-03", nil, nil), &series)

	if len(series.Points) != 3 {
		t.Fatalf("points = %d, want 3", len(series.Points))
	}
	if !series.Points[0].Recorded || !series.Points[1].Recorded {
		t.Fatal("January and February have balances")
	}
	if series.Points[2].Recorded || series.Points[2].General != nil {
		t.Fatalf("March has none and must read blank: %+v", series.Points[2])
	}
}
