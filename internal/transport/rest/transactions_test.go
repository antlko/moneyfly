package rest

import (
	"net/http"
	"strconv"
	"testing"
)

// ids fetches the seeded category and account a test needs.
func (ts *testServer) ids(t *testing.T, categoryName, accountName string) (categoryID, accountID int64) {
	t.Helper()
	var cats []CategoryDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories", nil, nil), &cats)
	for _, c := range cats {
		if c.Name == categoryName {
			categoryID = c.ID
		}
	}
	var accounts []AccountDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/accounts", nil, nil), &accounts)
	for _, a := range accounts {
		if a.Name == accountName {
			accountID = a.ID
		}
	}
	if categoryID == 0 || accountID == 0 {
		t.Fatalf("could not resolve %q / %q", categoryName, accountName)
	}
	return categoryID, accountID
}

func expenseBody(accountID, categoryID int64, on string, minor int64, currency string, exponent int) map[string]any {
	return map[string]any{
		"account_id": accountID, "category_id": categoryID, "occurred_on": on, "kind": "expense",
		"amount": map[string]any{"amount_minor": minor, "currency": currency, "exponent": exponent},
	}
}

func TestQuickEntry_CreateAndList(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	food, cash := ts.ids(t, "Food", "Cash EUR")

	// The three-tap path: category, amount, save.
	resp := ts.do(http.MethodPost, "/api/v1/transactions",
		expenseBody(cash, food, "2026-07-15", 1250, "EUR", 2), nil)
	expectStatus(t, resp, http.StatusCreated)
	var created TransactionDTO
	decode(t, resp, &created)

	if created.Amount.AmountMinor != 1250 || created.Amount.Exponent != 2 {
		t.Fatalf("amount = %+v", created.Amount)
	}
	if created.BaseAmount == nil || created.BaseAmount.AmountMinor != 1250 {
		t.Fatalf("base amount = %+v", created.BaseAmount)
	}
	if created.Unconverted {
		t.Error("a euro amount in a euro base currency is not unconverted")
	}
	if created.CategoryName != "Food" || created.AccountName != "Cash EUR" {
		t.Errorf("names not returned for list rendering: %+v", created)
	}

	var page struct {
		Items      []TransactionDTO `json:"items"`
		HasMore    bool             `json:"has_more"`
		NextCursor *string          `json:"next_cursor"`
	}
	decode(t, ts.do(http.MethodGet, "/api/v1/transactions", nil, nil), &page)
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("list = %+v", page.Items)
	}
	if page.HasMore || page.NextCursor != nil {
		t.Errorf("a single-page result must not claim more: %+v", page)
	}
}

func TestQuickEntry_HUFConvertsAtSeededRate(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	comms, hufAccount := ts.ids(t, "Communications", "Cash HUF")

	// 4000 HUF at the workbook's seeded rate is 11.20 EUR. HUF has exponent 0.
	resp := ts.do(http.MethodPost, "/api/v1/transactions",
		expenseBody(hufAccount, comms, "2026-07-15", 4000, "HUF", 0), nil)
	expectStatus(t, resp, http.StatusCreated)
	var created TransactionDTO
	decode(t, resp, &created)

	if created.Amount.Exponent != 0 {
		t.Fatalf("HUF exponent = %d, want 0", created.Amount.Exponent)
	}
	if created.BaseAmount == nil {
		t.Fatal("the HUF amount must be converted on write")
	}
	if created.BaseAmount.AmountMinor != 1120 || created.BaseAmount.Currency != "EUR" {
		t.Fatalf("base = %+v, want 1120 EUR minor units", created.BaseAmount)
	}
	if created.BaseAmount.FxRateID == nil || created.BaseAmount.AsOfDate == nil {
		t.Fatalf("the rate behind the figure must be reported: %+v", created.BaseAmount)
	}

	// A client claiming HUF has cents is told, rather than silently scaled by 100.
	resp = ts.do(http.MethodPost, "/api/v1/transactions",
		expenseBody(hufAccount, comms, "2026-07-16", 4000, "HUF", 2), nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

func TestQuickEntry_MissingRateIsReported(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	comms, hufAccount := ts.ids(t, "Communications", "Cash HUF")

	// Before any stored rate: the write succeeds and says the figure is unconverted.
	resp := ts.do(http.MethodPost, "/api/v1/transactions",
		expenseBody(hufAccount, comms, "2019-03-04", 4000, "HUF", 0), nil)
	expectStatus(t, resp, http.StatusCreated)
	var created TransactionDTO
	decode(t, resp, &created)
	if created.BaseAmount != nil {
		t.Fatalf("base amount = %+v, want null", created.BaseAmount)
	}
	if !created.Unconverted {
		t.Fatal("the response must say the amount could not be converted")
	}
}

func TestIdempotency_ReplayReturnsOriginal(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	food, cash := ts.ids(t, "Food", "Cash EUR")
	body := expenseBody(cash, food, "2026-07-15", 1250, "EUR", 2)
	headers := map[string]string{HeaderIdempotencyKey: "demo-1"}

	resp := ts.do(http.MethodPost, "/api/v1/transactions", body, headers)
	expectStatus(t, resp, http.StatusCreated)
	var first TransactionDTO
	decode(t, resp, &first)

	// The optimistic save retried: same key, same body.
	resp = ts.do(http.MethodPost, "/api/v1/transactions", body, headers)
	expectStatus(t, resp, http.StatusCreated)
	var replay TransactionDTO
	decode(t, resp, &replay)
	if replay.ID != first.ID {
		t.Fatalf("replay created a new row: %d then %d", first.ID, replay.ID)
	}

	var page struct {
		Items []TransactionDTO `json:"items"`
	}
	decode(t, ts.do(http.MethodGet, "/api/v1/transactions", nil, nil), &page)
	if len(page.Items) != 1 {
		t.Fatalf("%d rows stored, want 1", len(page.Items))
	}

	// Without the key, the same body is a second real transaction — two identical
	// coffees on one day are two coffees.
	resp = ts.do(http.MethodPost, "/api/v1/transactions", body, nil)
	expectStatus(t, resp, http.StatusCreated)
	var second TransactionDTO
	decode(t, resp, &second)
	if second.ID == first.ID {
		t.Fatal("without an idempotency key a duplicate body must create a row")
	}
	if second.Occurrence != 2 {
		t.Fatalf("occurrence = %d, want 2", second.Occurrence)
	}

	// Reusing a key with a different body is a client bug, so it conflicts.
	resp = ts.do(http.MethodPost, "/api/v1/transactions",
		expenseBody(cash, food, "2026-07-16", 9999, "EUR", 2), headers)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a reused key with a different body returned %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTransfers_OverHTTP(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	_, cash := ts.ids(t, "Food", "Cash EUR")
	_, bank := ts.ids(t, "Food", "Banks EUR")

	resp := ts.do(http.MethodPost, "/api/v1/transfers", map[string]any{
		"from_account_id": cash, "to_account_id": bank, "occurred_on": "2026-07-15",
		"amount": map[string]any{"amount_minor": 50000, "currency": "EUR", "exponent": 2},
	}, nil)
	expectStatus(t, resp, http.StatusCreated)
	var pair []TransactionDTO
	decode(t, resp, &pair)
	if len(pair) != 2 {
		t.Fatalf("%d rows, want 2", len(pair))
	}
	for _, tr := range pair {
		if tr.CategoryID != nil {
			t.Error("a transfer must not carry a category")
		}
		if tr.TransferGroupID == nil {
			t.Error("both halves need a transfer_group_id")
		}
	}

	// Transfers do not appear in the budget report.
	var report BudgetReportDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/reports/budget?period=2026-07", nil, nil), &report)
	if report.SpendTotal == nil || report.SpendTotal.AmountMinor != 0 {
		t.Fatalf("spend total = %+v, want a recorded zero: a transfer is not spending", report.SpendTotal)
	}

	// Deleting one half deletes both.
	resp = ts.do(http.MethodDelete, "/api/v1/transactions/"+strconv.FormatInt(pair[0].ID, 10), nil, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	for _, tr := range pair {
		resp := ts.do(http.MethodGet, "/api/v1/transactions/"+strconv.FormatInt(tr.ID, 10), nil, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("row %d returned %d after the pair was deleted", tr.ID, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestTransactions_ValidationRejectsTransferKindAndBadDates(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	food, cash := ts.ids(t, "Food", "Cash EUR")

	// POST /transactions is not the way to record a transfer.
	body := expenseBody(cash, food, "2026-07-15", 1250, "EUR", 2)
	body["kind"] = "transfer_out"
	resp := ts.do(http.MethodPost, "/api/v1/transactions", body, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()

	// Dates are YYYY-MM-DD. The importer's DD.MM.YYYY handling stays in the importer.
	body = expenseBody(cash, food, "15.07.2026", 1250, "EUR", 2)
	resp = ts.do(http.MethodPost, "/api/v1/transactions", body, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()

	// An expense needs a category.
	resp = ts.raw(http.MethodPost, "/api/v1/transactions",
		`{"account_id":`+strconv.FormatInt(cash, 10)+`,"occurred_on":"2026-07-15","kind":"expense",`+
			`"amount":{"amount_minor":1250,"currency":"EUR","exponent":2}}`)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

func TestBudgetReport_OverHTTP(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	food, cash := ts.ids(t, "Food", "Cash EUR")

	// Plan 300.00 for Food, spend 12.50 of it: 4%, green.
	resp := ts.do(http.MethodPut, "/api/v1/budgets/"+strconv.FormatInt(food, 10)+"/2026-07",
		map[string]any{"planned": map[string]any{"amount_minor": 30000, "currency": "EUR", "exponent": 2}}, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = ts.do(http.MethodPost, "/api/v1/transactions",
		expenseBody(cash, food, "2026-07-15", 1250, "EUR", 2), nil)
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	var report BudgetReportDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/reports/budget?period=2026-07", nil, nil), &report)

	if report.Period != "2026-07" || report.BaseCurrency != "EUR" {
		t.Fatalf("report header = %+v", report)
	}
	var row *BudgetReportRowDTO
	for i := range report.Categories {
		if report.Categories[i].Name == "Food" {
			row = &report.Categories[i]
		}
	}
	if row == nil {
		t.Fatal("Food missing from the report")
	}
	if row.Actual == nil || row.Actual.AmountMinor != 1250 {
		t.Fatalf("actual = %+v", row.Actual)
	}
	if row.Planned == nil || row.Planned.AmountMinor != 30000 {
		t.Fatalf("planned = %+v", row.Planned)
	}
	if row.Ratio == nil || *row.Ratio < 0.0416 || *row.Ratio > 0.0417 {
		t.Fatalf("ratio = %v, want ~0.04167", row.Ratio)
	}
	if row.State != "within" {
		t.Fatalf("state = %q, want within", row.State)
	}
	// Colour is never the only signal: the state carries a label.
	if row.Label == "" {
		t.Error("the state label must be present for the UI")
	}

	// A month with no data is blank, not zero.
	var previous BudgetReportDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/reports/budget?period=2026-06", nil, nil), &previous)
	if previous.SpendTotal != nil {
		t.Fatalf("an untouched month must report null, got %+v", previous.SpendTotal)
	}
	for _, r := range previous.Categories {
		if r.Actual != nil {
			t.Fatalf("category %q reported %+v in an unrecorded month", r.Name, r.Actual)
		}
		if r.State != "not_recorded" {
			t.Fatalf("category %q state = %q, want not_recorded", r.Name, r.State)
		}
	}
}

func TestBudgetReport_RejectsBadPeriodAndValuation(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	for _, query := range []string{
		"", "?period=", "?period=2026-13", "?period=July", "?period=2026-07-01",
		"?period=2026-07&valuation=nonsense",
	} {
		resp := ts.do(http.MethodGet, "/api/v1/reports/budget"+query, nil, nil)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("query %q returned %d, want 422", query, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// Both documented valuation modes are accepted.
	for _, valuation := range []string{"contemporaneous", "constant"} {
		resp := ts.do(http.MethodGet, "/api/v1/reports/budget?period=2026-07&valuation="+valuation, nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("valuation %q returned %d", valuation, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestBudgets_BulkSeedOverHTTP(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var cats []CategoryDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/categories", nil, nil), &cats)
	items := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		items = append(items, map[string]any{
			"category_id": c.ID,
			"planned":     map[string]any{"amount_minor": 10000, "currency": "EUR", "exponent": 2},
		})
	}

	resp := ts.do(http.MethodPost, "/api/v1/budgets/bulk", map[string]any{
		"from_period": "2025-08", "to_period": "2026-07", "items": items,
	}, nil)
	expectStatus(t, resp, http.StatusOK)
	var result struct {
		PeriodsWritten int `json:"periods_written"`
		RowsWritten    int `json:"rows_written"`
	}
	decode(t, resp, &result)
	if result.PeriodsWritten != 12 || result.RowsWritten != 12*len(cats) {
		t.Fatalf("bulk result = %+v", result)
	}

	var budgets []BudgetDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/budgets?period=2026-01", nil, nil), &budgets)
	if len(budgets) != len(cats) {
		t.Fatalf("%d budget rows in January, want %d", len(budgets), len(cats))
	}
	if budgets[0].CategoryName == "" {
		t.Error("the category name must be included for display")
	}
}

func TestTransactions_SearchAndFilters(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	food, cash := ts.ids(t, "Food", "Cash EUR")
	transport, _ := ts.ids(t, "Transport", "Cash EUR")

	for _, spec := range []struct {
		category int64
		date     string
		desc     string
		minor    int64
	}{
		{food, "2026-07-01", "Продукты ", 4400},
		{food, "2026-07-05", "Bakery run", 850},
		{transport, "2026-07-10", "Monthly pass", 5500},
	} {
		body := expenseBody(cash, spec.category, spec.date, spec.minor, "EUR", 2)
		body["description"] = spec.desc
		resp := ts.do(http.MethodPost, "/api/v1/transactions", body, nil)
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	type page struct {
		Items      []TransactionDTO `json:"items"`
		HasMore    bool             `json:"has_more"`
		NextCursor *string          `json:"next_cursor"`
	}

	// Cyrillic search works: the data has always contained it.
	var got page
	decode(t, ts.do(http.MethodGet, "/api/v1/transactions?q=Продукты", nil, nil), &got)
	if len(got.Items) != 1 {
		t.Fatalf("Cyrillic search returned %d rows, want 1", len(got.Items))
	}

	decode(t, ts.do(http.MethodGet,
		"/api/v1/transactions?category_id="+strconv.FormatInt(transport, 10), nil, nil), &got)
	if len(got.Items) != 1 || got.Items[0].CategoryName != "Transport" {
		t.Fatalf("category filter = %+v", got.Items)
	}

	decode(t, ts.do(http.MethodGet, "/api/v1/transactions?from=2026-07-04&to=2026-07-31", nil, nil), &got)
	if len(got.Items) != 2 {
		t.Fatalf("date filter returned %d rows, want 2", len(got.Items))
	}

	// Cursor pagination: infinite scroll walks every row once.
	decode(t, ts.do(http.MethodGet, "/api/v1/transactions?limit=2", nil, nil), &got)
	if !got.HasMore || got.NextCursor == nil {
		t.Fatalf("expected a further page: %+v", got)
	}
	seen := map[int64]bool{}
	for _, item := range got.Items {
		seen[item.ID] = true
	}
	decode(t, ts.do(http.MethodGet, "/api/v1/transactions?limit=2&cursor="+*got.NextCursor, nil, nil), &got)
	for _, item := range got.Items {
		if seen[item.ID] {
			t.Fatalf("row %d appeared on both pages", item.ID)
		}
		seen[item.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("pagination saw %d rows, want 3", len(seen))
	}
	if got.HasMore {
		t.Error("the last page must not claim more")
	}
}

func TestTransactions_DeleteRemovesFromTotals(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")
	food, cash := ts.ids(t, "Food", "Cash EUR")

	resp := ts.do(http.MethodPost, "/api/v1/transactions",
		expenseBody(cash, food, "2026-07-15", 1250, "EUR", 2), nil)
	expectStatus(t, resp, http.StatusCreated)
	var created TransactionDTO
	decode(t, resp, &created)

	var before BudgetReportDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/reports/budget?period=2026-07", nil, nil), &before)
	if before.SpendTotal == nil || before.SpendTotal.AmountMinor != 1250 {
		t.Fatalf("spend total before delete = %+v", before.SpendTotal)
	}

	resp = ts.do(http.MethodDelete, "/api/v1/transactions/"+strconv.FormatInt(created.ID, 10), nil, nil)
	expectStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	var after BudgetReportDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/reports/budget?period=2026-07", nil, nil), &after)
	if after.SpendTotal != nil {
		t.Fatalf("after deleting the only transaction the month is unrecorded, got %+v", after.SpendTotal)
	}
}

func TestFxRates_Endpoint(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var rates []FxRateDTO
	decode(t, ts.do(http.MethodGet, "/api/v1/fx/rates?quote=HUF&from=2021-01-01&to=2026-12-31", nil, nil), &rates)
	if len(rates) == 0 {
		t.Fatal("the seeded workbook rates must be readable")
	}
	// The rate is a decimal string, never a float on the wire.
	if rates[0].Rate != "357.142857142857" {
		t.Fatalf("EUR->HUF = %q", rates[0].Rate)
	}
	if rates[0].Source != "workbook-seed" {
		t.Fatalf("source = %q", rates[0].Source)
	}

	resp := ts.do(http.MethodGet, "/api/v1/fx/rates", nil, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()

	var latest []map[string]any
	decode(t, ts.do(http.MethodGet, "/api/v1/fx/rates/latest", nil, nil), &latest)
	if len(latest) != 3 {
		t.Fatalf("%d latest rates, want 3 (USD, HUF, UAH)", len(latest))
	}
	for _, entry := range latest {
		if _, ok := entry["age_days"]; !ok {
			t.Error("staleness must be reported alongside the rate")
		}
	}
}
