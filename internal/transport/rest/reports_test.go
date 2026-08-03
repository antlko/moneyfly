package rest

import (
	"net/http"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/domain/period"
)

// seedYear records one expense in each of three months and a salary in two, so
// the roll-up rows have something to say and one month stays deliberately blank.
func seedYear(t *testing.T, ts *testServer, u auth.User) {
	t.Helper()

	var cats []CategoryDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/categories", nil, nil), &cats)
	byName := map[string]int64{}
	for _, c := range cats {
		byName[c.Name] = c.ID
	}
	var accounts []AccountDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/accounts", nil, nil), &accounts)
	var cashEUR int64
	for _, a := range accounts {
		if a.Name == "Cash EUR" {
			cashEUR = a.ID
		}
	}
	if cashEUR == 0 || byName["Food"] == 0 || byName["Salary"] == 0 {
		t.Fatal("the seeded taxonomy is missing Cash EUR, Food or Salary")
	}

	post := func(date string, categoryID int64, minor int64, kind string) {
		resp := ts.do(http.MethodPost, APIPrefix+"/transactions", map[string]any{
			"account_id":  cashEUR,
			"category_id": categoryID,
			"occurred_on": date,
			"kind":        kind,
			"amount":      map[string]any{"amount_minor": minor, "currency": "EUR", "exponent": 2},
		}, nil)
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	post("2026-01-10", byName["Food"], 10000, "expense")
	post("2026-02-10", byName["Food"], 20000, "expense")
	// March is left blank on purpose: it must read as absent, never as zero.
	post("2026-04-10", byName["Food"], 30000, "expense")
	post("2026-01-01", byName["Salary"], 200000, "income")
	post("2026-02-01", byName["Salary"], 200000, "income")

	resp := ts.do(http.MethodPost, APIPrefix+"/budgets/bulk", map[string]any{
		"from_period": "2026-01", "to_period": "2026-04",
		"items": []map[string]any{{
			"category_id": byName["Food"],
			"planned":     map[string]any{"amount_minor": 15000, "currency": "EUR", "exponent": 2},
		}},
	}, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestReportSummary_AbsentMonthIsNullNotZero(t *testing.T) {
	ts := newTestServer(t, true)
	u := ts.login("owner@example.test")
	seedYear(t, ts, u)

	var summary ReportSummaryDTO
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/summary?from=2026-01&to=2026-04", nil, nil), &summary)
	if len(summary.Periods) != 4 {
		t.Fatalf("periods = %d, want 4", len(summary.Periods))
	}

	march := summary.Periods[2]
	if march.Period != "2026-03" {
		t.Fatalf("third period = %s, want 2026-03", march.Period)
	}
	if march.Recorded {
		t.Fatal("March recorded nothing and must say so")
	}
	if march.SpendTotal != nil || march.Income != nil || march.Diff != nil || march.SavedPercent != nil {
		t.Fatalf("an unrecorded month must be null throughout, got %+v", march)
	}
	// The plan exists whether or not the month has data: it was set in advance.
	if march.PlannedTotal.AmountMinor != 15000 {
		t.Fatalf("planned total = %d, want 15000", march.PlannedTotal.AmountMinor)
	}

	january := summary.Periods[0]
	if january.SpendTotal == nil || january.SpendTotal.AmountMinor != 10000 {
		t.Fatalf("January spend = %+v, want 100.00", january.SpendTotal)
	}
	if january.Diff == nil || january.Diff.AmountMinor != 190000 {
		t.Fatalf("January diff = %+v, want 1900.00", january.Diff)
	}
	if january.SavedPercent == nil || *january.SavedPercent < 0.94 || *january.SavedPercent > 0.96 {
		t.Fatalf("January saved %% = %v, want about 0.95", january.SavedPercent)
	}

	april := summary.Periods[3]
	if april.Income == nil || april.Income.AmountMinor != 0 {
		t.Fatalf("April income = %+v, want a recorded zero: the month has data, just no salary", april.Income)
	}
	if april.SavedPercent != nil {
		t.Fatalf("April saved %% = %v, want nil for zero income", *april.SavedPercent)
	}
}

func TestReportCategories_GridAndAverages(t *testing.T) {
	ts := newTestServer(t, true)
	u := ts.login("owner@example.test")
	seedYear(t, ts, u)

	var grid ReportCategoriesDTO
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/reports/categories?from=2026-01&to=2026-04", nil, nil), &grid)

	var food *CategorySeriesDTO
	for i := range grid.Categories {
		if grid.Categories[i].Name == "Food" {
			food = &grid.Categories[i]
		}
	}
	if food == nil {
		t.Fatal("the grid has no Food row")
	}
	if len(food.Cells) != 4 {
		t.Fatalf("cells = %d, want 4", len(food.Cells))
	}

	// Three recorded months: (100 + 200 + 300) / 3 = 200. March is not in the
	// denominator, which is the whole point of the AVERAGEIF the sheet used.
	if food.Average == nil || food.Average.AmountMinor != 20000 {
		t.Fatalf("Food average = %+v, want 200.00", food.Average)
	}
	if food.Total == nil || food.Total.AmountMinor != 60000 {
		t.Fatalf("Food total = %+v, want 600.00", food.Total)
	}

	march := food.Cells[2]
	if march.Actual != nil {
		t.Fatalf("March cell = %+v, want null — the grid renders that blank", march.Actual)
	}
	if march.State != "not_recorded" {
		t.Fatalf("March state = %q, want not_recorded", march.State)
	}
	if march.Label == "" {
		t.Fatal("every state carries a label, so colour is never the only signal")
	}

	// Another category with no rows at all is a recorded zero in a recorded
	// month, not an absence.
	for _, row := range grid.Categories {
		if row.Name != "Hobby" {
			continue
		}
		if row.Cells[0].Actual == nil || row.Cells[0].Actual.AmountMinor != 0 {
			t.Fatalf("Hobby January = %+v, want a recorded zero", row.Cells[0].Actual)
		}
		if row.Cells[2].Actual != nil {
			t.Fatalf("Hobby March = %+v, want null", row.Cells[2].Actual)
		}
	}
}

func TestBudgetReport_CarriesAverage(t *testing.T) {
	ts := newTestServer(t, true)
	u := ts.login("owner@example.test")
	seedYear(t, ts, u)

	var report BudgetReportDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/budget?period=2026-02", nil, nil), &report)

	for _, row := range report.Categories {
		if row.Name != "Food" {
			continue
		}
		if row.Actual == nil || row.Actual.AmountMinor != 20000 {
			t.Fatalf("February actual = %+v, want 200.00", row.Actual)
		}
		// The average holds steady across months: it is over the whole history.
		if row.Average == nil || row.Average.AmountMinor != 20000 {
			t.Fatalf("Food average = %+v, want 200.00", row.Average)
		}
		return
	}
	t.Fatal("the report has no Food row")
}

func TestReportSummary_DefaultsToTheFiscalYearOfTheLatestData(t *testing.T) {
	ts := newTestServer(t, true)
	u := ts.login("owner@example.test")
	seedYear(t, ts, u)

	var summary ReportSummaryDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/summary", nil, nil), &summary)

	// The default fiscal year starts in August, so April 2026 belongs to
	// 2025-08..2026-07 — the workbook's own layout.
	if summary.From != "2025-08" || summary.To != "2026-07" {
		t.Fatalf("range = %s..%s, want 2025-08..2026-07", summary.From, summary.To)
	}
	if len(summary.Periods) != 12 {
		t.Fatalf("periods = %d, want 12", len(summary.Periods))
	}
	if summary.FiscalYear != "2025-2026" {
		t.Fatalf("fiscal year = %q, want 2025-2026", summary.FiscalYear)
	}
	_ = u
}

func TestReportSummary_RejectsBackwardsRange(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.do(http.MethodGet, APIPrefix+"/reports/summary?from=2026-06&to=2026-01", nil, nil)
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

func TestReportSummary_EmptyAccountIsBlankNotBroken(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("fresh@example.test")

	var summary ReportSummaryDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/reports/summary", nil, nil), &summary)
	if len(summary.Periods) != 12 {
		t.Fatalf("periods = %d, want a blank fiscal year of 12", len(summary.Periods))
	}
	for _, p := range summary.Periods {
		if p.Recorded || p.SpendTotal != nil {
			t.Fatalf("%s reports data on a fresh account: %+v", p.Period, p)
		}
	}
	if _, err := period.ParsePeriod(summary.From); err != nil {
		t.Fatalf("from = %q, want a period", summary.From)
	}
}
