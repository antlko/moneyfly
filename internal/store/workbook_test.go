package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/seed"
)

// workbookHarness is the migration wired over the real database, exactly as
// `migrate-excel` wires it.
type workbookHarness struct {
	*harness
	target seed.WorkbookTarget
	wb     seed.Workbook
	userID int64
}

func newWorkbookHarness(t *testing.T) workbookHarness {
	t.Helper()
	h := newHarness(t)
	u := h.user("owner@example.test")

	wb, err := seed.LoadWorkbook(fixtureDir + "parity/workbook-2025-2026.json")
	if err != nil {
		t.Fatalf("LoadWorkbook: %v", err)
	}

	metricsRepo := NewMetricsRepo(h.db)
	fxSvc := h.fxService()
	return workbookHarness{
		harness: h, wb: wb, userID: u.ID,
		target: seed.WorkbookTarget{
			Categories:   h.categoryService(),
			Accounts:     h.accountService(),
			Budgets:      h.budgetService(),
			Capital:      capital.NewService(NewSnapshotRepo(h.db), h.accountService(), fxSvc, h.Currencies, h.clock),
			Transactions: h.transactionService(),
			RecordedPeriods: func(ctx context.Context, userID int64) ([]period.Period, error) {
				return metricsRepo.RecordedPeriods(ctx, userID)
			},
			StoreRate: func(ctx context.Context, asOf time.Time, quote, rate string) error {
				parsed, err := fx.ParseRate(rate)
				if err != nil {
					return err
				}
				_, err = fxSvc.Upsert(ctx, fx.Rate{
					AsOf: asOf, Base: fx.StorageBase, Quote: quote,
					Rate: parsed, Source: "excel-import", FetchedAt: fixedNow,
				})
				return err
			},
		},
	}
}

func (w workbookHarness) run(t *testing.T, dryRun bool) seed.MigrationPlan {
	t.Helper()
	plan, err := seed.MigrateWorkbook(w.ctx, w.userID, w.wb, w.target, dryRun)
	if err != nil {
		t.Fatalf("MigrateWorkbook(dryRun=%v): %v", dryRun, err)
	}
	return plan
}

func (w workbookHarness) counts(t *testing.T) (transactions, budgets, snapshots, rates int) {
	t.Helper()
	rows := map[string]*int{
		`SELECT count(*) FROM transaction_entry WHERE user_id = ? AND deleted_at IS NULL`: &transactions,
		`SELECT count(*) FROM budget WHERE user_id = ?`:                                   &budgets,
		`SELECT count(*) FROM balance_snapshot WHERE user_id = ?`:                         &snapshots,
	}
	for query, into := range rows {
		if err := w.db.QueryRow(query, w.userID).Scan(into); err != nil {
			t.Fatalf("counting: %v", err)
		}
	}
	if err := w.db.QueryRow(
		`SELECT count(*) FROM fx_rate WHERE source = 'excel-import'`).Scan(&rates); err != nil {
		t.Fatalf("counting rates: %v", err)
	}
	return
}

func TestMigrateExcel_DryRunWritesNothing(t *testing.T) {
	w := newWorkbookHarness(t)
	before, beforeBudgets, beforeSnapshots, beforeRates := w.counts(t)

	plan := w.run(t, true)
	if plan.Aggregates == 0 || plan.Snapshots == 0 || plan.Budgets == 0 {
		t.Fatalf("the dry run planned nothing: %+v", plan)
	}

	after, afterBudgets, afterSnapshots, afterRates := w.counts(t)
	if after != before || afterBudgets != beforeBudgets ||
		afterSnapshots != beforeSnapshots || afterRates != beforeRates {
		t.Fatal("a dry run must write nothing at all")
	}
}

func TestMigrateExcel_Idempotent(t *testing.T) {
	w := newWorkbookHarness(t)
	w.run(t, false)
	first, firstBudgets, firstSnapshots, _ := w.counts(t)

	w.run(t, false)
	second, secondBudgets, secondSnapshots, _ := w.counts(t)

	if second != first || secondBudgets != firstBudgets || secondSnapshots != firstSnapshots {
		t.Fatalf("re-running changed the row counts: %d/%d/%d then %d/%d/%d",
			first, firstBudgets, firstSnapshots, second, secondBudgets, secondSnapshots)
	}
}

// TestMigrateExcel_SentinelBecomesAbsent is the sentinel rule: the -1 that
// corrupted the workbook's own totals must not enter the database as anything.
func TestMigrateExcel_SentinelBecomesAbsent(t *testing.T) {
	w := newWorkbookHarness(t)
	w.run(t, false)

	var negative int
	if err := w.db.QueryRow(`
		SELECT count(*) FROM transaction_entry
		WHERE user_id = ? AND amount_minor <= 0`, w.userID).Scan(&negative); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if negative != 0 {
		t.Fatalf("%d rows have a non-positive amount; the sentinel got in", negative)
	}

	// July is unrecorded in the workbook and must stay unrecorded here.
	var july int
	if err := w.db.QueryRow(`
		SELECT count(*) FROM transaction_entry
		WHERE user_id = ? AND occurred_on LIKE '2026-07%'`, w.userID).Scan(&july); err != nil {
		t.Fatalf("counting July: %v", err)
	}
	if july != 0 {
		t.Fatalf("July has %d rows; the unfilled month must stay empty", july)
	}
}

// TestMigrateExcel_SkipsMonthsCoveredByCSV is the one that prevents a silent
// doubling of every overlapping month — the most likely way this stage could
// corrupt real data.
func TestMigrateExcel_SkipsMonthsCoveredByCSV(t *testing.T) {
	w := newWorkbookHarness(t)

	// A transaction already exists in one of the workbook's months, as it would
	// after the Monefy import.
	acct := w.accountByName(w.userID, "Cash EUR")
	food := w.categoryByName(w.userID, "Food")
	if _, err := w.transactionService().Create(w.ctx, w.userID, "EUR",
		newExpense(acct.ID, food.ID, "2026-01-10", 5000, "EUR")); err != nil {
		t.Fatalf("seeding the CSV month: %v", err)
	}

	plan := w.run(t, false)
	if len(plan.SkippedPeriods) == 0 {
		t.Fatal("the covered month was not skipped")
	}
	found := false
	for _, p := range plan.SkippedPeriods {
		if p == "2026-01" {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped %v, want 2026-01 among them", plan.SkippedPeriods)
	}
	if len(plan.Notes) == 0 || !strings.Contains(strings.Join(plan.Notes, " "), "double") {
		t.Fatalf("notes = %v, want the doubling risk stated plainly", plan.Notes)
	}

	// And January holds only the CSV's row, not the workbook's aggregates on top.
	var january int
	if err := w.db.QueryRow(`
		SELECT count(*) FROM transaction_entry
		WHERE user_id = ? AND occurred_on LIKE '2026-01%'`, w.userID).Scan(&january); err != nil {
		t.Fatalf("counting January: %v", err)
	}
	if january != 1 {
		t.Fatalf("January has %d rows, want only the 1 from the CSV", january)
	}
}

func TestMigrateExcel_SeedsFxAsExcelImport(t *testing.T) {
	w := newWorkbookHarness(t)
	w.run(t, false)

	rows, err := w.db.Query(
		`SELECT base, quote, rate FROM fx_rate WHERE source = 'excel-import' ORDER BY quote`)
	if err != nil {
		t.Fatalf("reading rates: %v", err)
	}
	defer func() { _ = rows.Close() }()

	seen := map[string]string{}
	for rows.Next() {
		var base, quote, rate string
		if err := rows.Scan(&base, &quote, &rate); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		// Only EUR->X is stored: the sheet's USD/EUR contradicts its EUR/USD, and
		// one direction removes the contradiction by construction.
		if base != "EUR" {
			t.Errorf("stored a %s-based rate; only EUR->X belongs in the table", base)
		}
		seen[quote] = rate
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading rates: %v", err)
	}
	for _, quote := range []string{"USD", "HUF", "UAH"} {
		if _, ok := seen[quote]; !ok {
			t.Errorf("no %s rate was imported", quote)
		}
	}
	// E2 (EUR/USD = 1.14) is referenced by no formula and is not imported; the
	// USD rate comes from E3's reciprocal instead.
	if got := seen["USD"]; strings.HasPrefix(got, "1.14") {
		t.Errorf("USD rate = %s, want the reciprocal of E3 rather than the dead E2", got)
	}
}

func TestMigrateExcel_RoundingTraceable(t *testing.T) {
	w := newWorkbookHarness(t)
	w.run(t, false)

	var description string
	if err := w.db.QueryRow(`
		SELECT description FROM transaction_entry
		WHERE user_id = ? AND description LIKE ? LIMIT 1`,
		w.userID, "%"+seed.AggregateNote+"%").Scan(&description); err != nil {
		t.Fatalf("reading a migrated row: %v", err)
	}
	// The original cell value travels with the row, so a rounded figure can
	// always be traced back to what the sheet said.
	if !strings.Contains(description, "cell ") {
		t.Fatalf("description = %q, want the original cell value recorded", description)
	}
	if !strings.Contains(description, seed.AggregateNote) {
		t.Fatalf("description = %q, want it marked as a monthly aggregate", description)
	}
}

func TestMigrateExcel_LoadsCapitalAndBudgets(t *testing.T) {
	w := newWorkbookHarness(t)
	plan := w.run(t, false)

	_, budgets, snapshots, _ := w.counts(t)
	if budgets == 0 {
		t.Fatal("no budgets were written")
	}
	if snapshots != plan.Snapshots {
		t.Fatalf("wrote %d snapshots, planned %d", snapshots, plan.Snapshots)
	}

	// And the capital half reads back: net worth for the last recorded month.
	fxSvc := w.fxService()
	svc := capital.NewService(NewSnapshotRepo(w.db), w.accountService(), fxSvc, w.Currencies, w.clock)
	data, err := svc.Load(w.ctx, w.userID, "2026-06", "2026-06", "EUR",
		metrics.Data{BaseCurrency: "EUR"}, capital.Contemporaneous)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	general := capital.General(data, "2026-06")
	if general == nil {
		t.Fatal("net worth is nil after the migration")
	}
	// The workbook's B55, reproduced from migrated data.
	if general.Minor != 2658166 {
		t.Fatalf("general = %d, want 26581.66 from the workbook", general.Minor)
	}
}
