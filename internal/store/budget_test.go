package store

import (
	"errors"
	"math"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// workbookThresholds are the sheet's own values: C2 = 10 for amber, 2x hardcoded
// for severely over.
var workbookThresholds = budget.Thresholds{WarnPercent: 10, OverMultiplier: 2}

func rowFor(t *testing.T, report budget.Report, name string) budget.CategoryRow {
	t.Helper()
	for _, row := range report.Categories {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("category %q missing from the report", name)
	return budget.CategoryRow{}
}

func TestBudgetReport_UnrecordedIsNull(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()
	food := h.categoryByName(u.ID, "Food")

	// A plan exists, but the month has no transactions at all.
	if _, err := budgets.Upsert(h.ctx, u.ID, food.ID, "2026-06", money.New(30000, "EUR")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	report, err := budgets.Report(h.ctx, u.ID, "2026-06", "EUR", h.Txn, workbookThresholds)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	// This is the workbook's -1 sentinel, correctly expressed. Absent is not zero.
	if report.SpendTotal != nil {
		t.Fatalf("spend total = %+v, want null for an unrecorded month", report.SpendTotal)
	}
	if report.Income != nil || report.Diff != nil || report.SavedPercent != nil {
		t.Fatalf("income, diff and saved%% must all be null: %+v %+v %+v",
			report.Income, report.Diff, report.SavedPercent)
	}
	row := rowFor(t, report, "Food")
	if row.Actual != nil {
		t.Fatalf("actual = %+v, want null", row.Actual)
	}
	if row.State != budget.StateNotRecorded {
		t.Fatalf("state = %q, want not_recorded", row.State)
	}
	if row.Ratio != nil {
		t.Fatalf("ratio = %v, want null", *row.Ratio)
	}
	// The plan is still reported: it was recorded.
	if row.Planned == nil || row.Planned.Minor != 30000 {
		t.Fatalf("planned = %+v, want 30000", row.Planned)
	}
}

func TestBudgetReport_RecordedZeroIsZero(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()
	txns := h.transactionService()

	food := h.categoryByName(u.ID, "Food")
	hobby := h.categoryByName(u.ID, "Hobby")
	acct := h.accountByName(u.ID, "Cash EUR")

	if _, err := budgets.Upsert(h.ctx, u.ID, hobby.ID, "2026-07", money.New(2000, "EUR")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// The month has data, but nothing was spent on Hobby.
	if _, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(acct.ID, food.ID, "2026-07-15", 1250, "EUR")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	report, err := budgets.Report(h.ctx, u.ID, "2026-07", "EUR", h.Txn, workbookThresholds)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	row := rowFor(t, report, "Hobby")
	if row.Actual == nil {
		t.Fatal("a recorded month with no spend on this category is zero, not null")
	}
	if row.Actual.Minor != 0 {
		t.Fatalf("actual = %d, want 0", row.Actual.Minor)
	}
	if row.State != budget.StateZero {
		t.Fatalf("state = %q, want zero", row.State)
	}
	// In the workbook, Hobby was all zeros and its total came out as -1. Here the
	// two facts are distinguishable.
	if report.SpendTotal == nil || report.SpendTotal.Minor != 1250 {
		t.Fatalf("spend total = %+v, want 1250", report.SpendTotal)
	}
}

func TestBudgetReport_States(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	planned := money.New(10000, "EUR") // 100.00
	cases := []struct {
		spendMinor int64
		want       budget.State
		why        string
	}{
		{0, budget.StateZero, "recorded, nothing spent"},
		{5000, budget.StateWithin, "half the plan"},
		{8999, budget.StateWithin, "just under the amber threshold"},
		{9000, budget.StateApproaching, "exactly 90% of plan is amber (C2 = 10)"},
		{9500, budget.StateApproaching, "inside the amber band"},
		{10000, budget.StateApproaching, "exactly at plan is amber, not over"},
		{10001, budget.StateOver, "a cent over plan"},
		{19999, budget.StateOver, "just under twice the plan"},
		{20000, budget.StateSeverelyOver, "the 2x boundary itself is severely over"},
		{46287, budget.StateSeverelyOver, "the workbook's Transport month, 3.09x plan"},
	}
	for _, c := range cases {
		actual := money.New(c.spendMinor, "EUR")
		got := budget.Classify(&actual, &planned, workbookThresholds)
		if got != c.want {
			t.Errorf("spend %d against plan %d = %q, want %q (%s)",
				c.spendMinor, planned.Minor, got, c.want, c.why)
		}
	}

	// Not recorded beats everything: a NULL cannot be compared.
	if got := budget.Classify(nil, &planned, workbookThresholds); got != budget.StateNotRecorded {
		t.Errorf("nil actual = %q, want not_recorded", got)
	}
	// No plan: there is nothing to be over. The UI labels this "no plan".
	spend := money.New(5000, "EUR")
	if got := budget.Classify(&spend, nil, workbookThresholds); got != budget.StateWithin {
		t.Errorf("no plan with spend = %q, want within", got)
	}
	zero := money.New(0, "EUR")
	if got := budget.Classify(&zero, nil, workbookThresholds); got != budget.StateZero {
		t.Errorf("no plan, no spend = %q, want zero", got)
	}
	// A plan of exactly zero with spend against it is severely over — you planned
	// to spend nothing and spent. But zero against zero is not.
	zeroPlan := money.New(0, "EUR")
	if got := budget.Classify(&spend, &zeroPlan, workbookThresholds); got != budget.StateSeverelyOver {
		t.Errorf("spend against a zero plan = %q, want severely_over", got)
	}
	if got := budget.Classify(&zero, &zeroPlan, workbookThresholds); got != budget.StateZero {
		t.Errorf("zero spend against a zero plan = %q, want zero", got)
	}

	// Every state carries an accessible label, so colour is never the only signal.
	for _, state := range []budget.State{
		budget.StateSeverelyOver, budget.StateOver, budget.StateApproaching,
		budget.StateWithin, budget.StateZero, budget.StateNotRecorded,
	} {
		if budget.Label(state) == "" {
			t.Errorf("state %q has no label", state)
		}
	}
	_ = u
}

func TestBudgetReport_SavedPercentUnclamped(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	house := h.categoryByName(u.ID, "House")
	// Salary is seeded: income needs line items, and without a seeded income
	// category there is nothing to file a salary against.
	salary := h.categoryByName(u.ID, "Salary")

	// Spend 4.75x income, as in the workbook's January. Saved % is -3.75 and that
	// is the truth; clamping would hide the most important month in the data.
	income := newExpense(acct.ID, salary.ID, "2026-01-05", 100000, "EUR")
	income.Kind = transaction.KindIncome
	if _, err := txns.Create(h.ctx, u.ID, "EUR", income); err != nil {
		t.Fatalf("Create income: %v", err)
	}
	if _, err := txns.Create(h.ctx, u.ID, "EUR",
		newExpense(acct.ID, house.ID, "2026-01-10", 475000, "EUR")); err != nil {
		t.Fatalf("Create expense: %v", err)
	}

	report, err := budgets.Report(h.ctx, u.ID, "2026-01", "EUR", h.Txn, workbookThresholds)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.SavedPercent == nil {
		t.Fatal("saved percent must be computed when income is positive")
	}
	if math.Abs(*report.SavedPercent-(-3.75)) > 1e-9 {
		t.Fatalf("saved percent = %v, want -3.75 exactly, unclamped", *report.SavedPercent)
	}
	if report.Diff == nil || report.Diff.Minor != -375000 {
		t.Fatalf("diff = %+v, want -375000", report.Diff)
	}
}

func TestBudgetReport_ZeroIncomeGivesNullSavedPercent(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()
	txns := h.transactionService()

	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")
	if _, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(acct.ID, food.ID, "2026-07-15", 1250, "EUR")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	report, err := budgets.Report(h.ctx, u.ID, "2026-07", "EUR", h.Txn, workbookThresholds)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	// Zero savings and unknown savings are different facts; the sheet's
	// IFERROR(..., 0) conflated them.
	if report.SavedPercent != nil {
		t.Fatalf("saved percent = %v, want null when income is zero", *report.SavedPercent)
	}
	if report.Income == nil || report.Income.Minor != 0 {
		t.Fatalf("income = %+v, want a recorded zero", report.Income)
	}
}

func TestBudgetReport_PossibleMinimumSumsEssentialPlans(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()

	// Plan every category at 100.00; the essential 13 make up Possible Minimum.
	cats, err := h.Categories.List(h.ctx, u.ID, "expense", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	items := make([]budget.BulkItem, 0, len(cats))
	for _, c := range cats {
		items = append(items, budget.BulkItem{CategoryID: c.ID, Planned: money.New(10000, "EUR")})
	}
	if _, err := budgets.Bulk(h.ctx, u.ID, "2026-07", "2026-07", items); err != nil {
		t.Fatalf("Bulk: %v", err)
	}

	report, err := budgets.Report(h.ctx, u.ID, "2026-07", "EUR", h.Txn, workbookThresholds)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.PlannedTotal.Minor != int64(len(cats))*10000 {
		t.Fatalf("planned total = %d, want %d", report.PlannedTotal.Minor, int64(len(cats))*10000)
	}
	// 13 essential categories, from the workbook's B28 membership.
	if report.PossibleMinimum.Minor != 13*10000 {
		t.Fatalf("possible minimum = %d, want %d", report.PossibleMinimum.Minor, 13*10000)
	}
}

func TestBulkBudget_SeedsRange(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()

	cats, err := h.Categories.List(h.ctx, u.ID, "expense", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	items := make([]budget.BulkItem, 0, len(cats))
	for _, c := range cats {
		items = append(items, budget.BulkItem{CategoryID: c.ID, Planned: money.New(10000, "EUR")})
	}

	// The workbook holds one annual figure per category. Seeding a year is how it
	// lands without 216 keystrokes.
	result, err := budgets.Bulk(h.ctx, u.ID, "2025-08", "2026-07", items)
	if err != nil {
		t.Fatalf("Bulk: %v", err)
	}
	if result.PeriodsWritten != 12 {
		t.Fatalf("periods written = %d, want 12", result.PeriodsWritten)
	}
	if result.RowsWritten != 12*len(cats) {
		t.Fatalf("rows written = %d, want %d", result.RowsWritten, 12*len(cats))
	}
	for _, p := range []period.Period{"2025-08", "2026-01", "2026-07"} {
		got, err := budgets.ListByPeriod(h.ctx, u.ID, p)
		if err != nil {
			t.Fatalf("ListByPeriod %s: %v", p, err)
		}
		if len(got) != len(cats) {
			t.Fatalf("%s has %d budget rows, want %d", p, len(got), len(cats))
		}
	}
	// Outside the range, nothing was written.
	outside, err := budgets.ListByPeriod(h.ctx, u.ID, "2026-08")
	if err != nil {
		t.Fatalf("ListByPeriod: %v", err)
	}
	if len(outside) != 0 {
		t.Fatalf("%d rows written outside the range", len(outside))
	}
}

func TestBulkBudget_Validates(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()
	food := h.categoryByName(u.ID, "Food")
	item := budget.BulkItem{CategoryID: food.ID, Planned: money.New(10000, "EUR")}

	if _, err := budgets.Bulk(h.ctx, u.ID, "2026-07", "2026-06", []budget.BulkItem{item}); !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("a reversed range must be rejected, got %v", err)
	}
	if _, err := budgets.Bulk(h.ctx, u.ID, "2026-07", "2026-07", nil); !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("an empty item list must be rejected, got %v", err)
	}
	if _, err := budgets.Bulk(h.ctx, u.ID, "2026-07", "2026-07",
		[]budget.BulkItem{item, item}); !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("a duplicated category must be rejected, got %v", err)
	}
	bad := budget.BulkItem{CategoryID: food.ID, Planned: money.New(-1, "EUR")}
	if _, err := budgets.Bulk(h.ctx, u.ID, "2026-07", "2026-07",
		[]budget.BulkItem{bad}); !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("a negative plan must be rejected, got %v", err)
	}
}

func TestBudget_UpsertReplacesAndDeleteRemoves(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()
	food := h.categoryByName(u.ID, "Food")

	if _, err := budgets.Upsert(h.ctx, u.ID, food.ID, "2026-07", money.New(30000, "EUR")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := budgets.Upsert(h.ctx, u.ID, food.ID, "2026-07", money.New(35000, "EUR")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := budgets.ListByPeriod(h.ctx, u.ID, "2026-07")
	if err != nil {
		t.Fatalf("ListByPeriod: %v", err)
	}
	if len(got) != 1 || got[0].Planned.Minor != 35000 {
		t.Fatalf("upsert must replace, got %+v", got)
	}
	if got[0].CategoryName != "Food" {
		t.Fatalf("category name = %q", got[0].CategoryName)
	}

	if err := budgets.Delete(h.ctx, u.ID, food.ID, "2026-07"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err = budgets.ListByPeriod(h.ctx, u.ID, "2026-07")
	if err != nil {
		t.Fatalf("ListByPeriod: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("%d rows left after delete", len(got))
	}
	// Deleting a plan is not the same as planning zero, so a second delete is a
	// not-found rather than a silent success.
	if err := budgets.Delete(h.ctx, u.ID, food.ID, "2026-07"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("deleting an absent plan = %v, want not found", err)
	}
}

func TestBudgetReport_UnconvertedRowsAreSurfacedNotZeroed(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()
	txns := h.transactionService()

	huf := h.accountByName(u.ID, "Cash HUF")
	comms := h.categoryByName(u.ID, "Communications")

	// A date before any rate exists: base_amount is NULL.
	if _, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(huf.ID, comms.ID, "2019-03-04", 4000, "HUF")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	report, err := budgets.Report(h.ctx, u.ID, "2019-03", "EUR", h.Txn, workbookThresholds)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.Unconverted != 1 {
		t.Fatalf("unconverted = %d, want 1 — an unconvertible row must be reported, not treated as zero", report.Unconverted)
	}
	// The month is recorded, but the unconvertible row contributes nothing to the
	// total rather than a guessed figure.
	if report.SpendTotal == nil || report.SpendTotal.Minor != 0 {
		t.Fatalf("spend total = %+v, want a recorded zero with the row flagged", report.SpendTotal)
	}
}

func TestBudgetReport_RejectsBadPeriod(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	budgets := h.budgetService()

	if _, err := budgets.Report(h.ctx, u.ID, "2026-13", "EUR", h.Txn, workbookThresholds); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("an invalid period must be a validation error, got %v", err)
	}
}

func TestUserIsolation_Budgets(t *testing.T) {
	h := newHarness(t)
	a := h.user("a@example.test")
	b := h.user("b@example.test")
	budgets := h.budgetService()

	bFood := h.categoryByName(b.ID, "Food")
	if _, err := budgets.Upsert(h.ctx, b.ID, bFood.ID, "2026-07", money.New(30000, "EUR")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// User A cannot plan against user B's category.
	if _, err := budgets.Upsert(h.ctx, a.ID, bFood.ID, "2026-07", money.New(1, "EUR")); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not budget user B's category, got %v", err)
	}
	if err := budgets.Delete(h.ctx, a.ID, bFood.ID, "2026-07"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not delete user B's budget, got %v", err)
	}
	// A sees nothing of B's plan.
	aRows, err := budgets.ListByPeriod(h.ctx, a.ID, "2026-07")
	if err != nil {
		t.Fatalf("ListByPeriod: %v", err)
	}
	if len(aRows) != 0 {
		t.Fatalf("user A sees %d of user B's budget rows", len(aRows))
	}
	// B's plan is intact.
	bRows, err := budgets.ListByPeriod(h.ctx, b.ID, "2026-07")
	if err != nil {
		t.Fatalf("ListByPeriod: %v", err)
	}
	if len(bRows) != 1 || bRows[0].Planned.Minor != 30000 {
		t.Fatalf("user B's plan was damaged: %+v", bRows)
	}
}
