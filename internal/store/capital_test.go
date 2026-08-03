package store

import (
	"errors"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// capitalHarness bundles the pieces the capital tests keep reaching for.
type capitalHarness struct {
	*harness
	svc    *capital.Service
	userID int64
}

func newCapitalHarness(t *testing.T) capitalHarness {
	t.Helper()
	h := newHarness(t)
	u := h.user("owner@example.test")
	return capitalHarness{
		harness: h,
		svc: capital.NewService(NewSnapshotRepo(h.db), h.accountService(),
			h.fxService(), h.Currencies, h.clock),
		userID: u.ID,
	}
}

// rate stores one EUR-based rate for the last day of a period.
func (c capitalHarness) rate(t *testing.T, quote, value string, p period.Period) {
	t.Helper()
	r, err := fx.ParseRate(value)
	if err != nil {
		t.Fatalf("parsing rate: %v", err)
	}
	_, last := p.Range()
	if _, err := c.fxService().Upsert(c.ctx, fx.Rate{
		AsOf: last, Base: fx.StorageBase, Quote: quote, Rate: r, Source: "test",
	}); err != nil {
		t.Fatalf("storing rate: %v", err)
	}
}

func (c capitalHarness) record(t *testing.T, accountName string, p period.Period, minor int64, code string) {
	t.Helper()
	acct := c.accountByName(c.userID, accountName)
	if _, err := c.svc.Upsert(c.ctx, c.userID, acct.ID, p, "EUR", capital.Input{
		Amount: ptrMoney(money.New(minor, code)),
	}); err != nil {
		t.Fatalf("recording %s %s: %v", accountName, p, err)
	}
}

func (c capitalHarness) load(t *testing.T, from, to period.Period) capital.Data {
	t.Helper()
	md, _, err := metrics.NewService(NewMetricsRepo(c.db), c.categoryService()).
		Load(c.ctx, c.userID, from, to, "EUR")
	if err != nil {
		t.Fatalf("loading metrics: %v", err)
	}
	data, err := c.svc.Load(c.ctx, c.userID, from, to, "EUR", md, capital.Contemporaneous)
	if err != nil {
		t.Fatalf("loading capital: %v", err)
	}
	return data
}

func TestValue_ParentSumsChildren(t *testing.T) {
	c := newCapitalHarness(t)
	c.rate(t, "USD", "1.25", "2026-01")
	c.rate(t, "HUF", "400", "2026-01")

	c.record(t, "Cash EUR", "2026-01", 100000, "EUR") // 1000.00 EUR
	c.record(t, "Cash USD", "2026-01", 125000, "USD") // 1250.00 USD -> 1000.00 EUR
	c.record(t, "Cash HUF", "2026-01", 40000, "HUF")  // 40,000 Ft   ->  100.00 EUR

	data := c.load(t, "2026-01", "2026-01")
	cash := c.accountByName(c.userID, "Cash")
	got := capital.Value(data, cash.ID, "2026-01")
	if got == nil {
		t.Fatal("the parent has no value")
	}
	if got.Minor != 210000 {
		t.Fatalf("Cash = %d minor, want 210000 — the sum of its children, converted first", got.Minor)
	}
}

func TestValue_ParentNotStored(t *testing.T) {
	c := newCapitalHarness(t)
	cash := c.accountByName(c.userID, "Cash")

	_, err := c.svc.Upsert(c.ctx, c.userID, cash.ID, "2026-01", "EUR", capital.Input{
		Amount: ptrMoney(money.New(100000, "EUR")),
	})
	if !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("writing to a computed parent = %v, want a validation error", err)
	}
}

func TestValue_NestedParents(t *testing.T) {
	c := newCapitalHarness(t)
	accounts := c.accountService()

	// Cash -> Cash EUR is one level; add a second underneath Cash EUR.
	cashEUR := c.accountByName(c.userID, "Cash EUR")
	pocket, err := accounts.Create(c.ctx, c.userID, account.Input{
		Name: "Pocket", AssetClass: account.ClassCash, Currency: "EUR",
		IsLiquid: true, CountsTowardNetWorth: true, ParentID: &cashEUR.ID,
	})
	if err != nil {
		t.Fatalf("creating the nested account: %v", err)
	}

	if _, err := c.svc.Upsert(c.ctx, c.userID, pocket.ID, "2026-01", "EUR", capital.Input{
		Amount: ptrMoney(money.New(25000, "EUR")),
	}); err != nil {
		t.Fatalf("recording the leaf: %v", err)
	}

	data := c.load(t, "2026-01", "2026-01")
	if got := capital.Value(data, cashEUR.ID, "2026-01"); got == nil || got.Minor != 25000 {
		t.Fatalf("Cash EUR = %+v, want 250.00 rolled up from its child", got)
	}
	cash := c.accountByName(c.userID, "Cash")
	if got := capital.Value(data, cash.ID, "2026-01"); got == nil || got.Minor != 25000 {
		t.Fatalf("Cash = %+v, want 250.00 rolled up two levels", got)
	}
	// And counted exactly once.
	if got := capital.General(data, "2026-01"); got == nil || got.Minor != 25000 {
		t.Fatalf("general = %+v, want 250.00", got)
	}
}

func TestAllocation_LeavesOnly(t *testing.T) {
	c := newCapitalHarness(t)
	c.rate(t, "HUF", "400", "2026-01")
	c.record(t, "Cash EUR", "2026-01", 100000, "EUR")
	c.record(t, "Cash HUF", "2026-01", 40000, "HUF")

	data := c.load(t, "2026-01", "2026-01")
	shares := capital.Allocation(data, "2026-01")

	sum := 0.0
	for _, sh := range shares {
		sum += sh.Share
		if sh.Name == "Cash" || sh.Name == "Banks" {
			t.Errorf("allocation includes the computed parent %q", sh.Name)
		}
	}
	if sum < 0.999999999 || sum > 1.000000001 {
		t.Fatalf("shares sum to %.12f, want 1.0", sum)
	}

	// 40,000 Ft at 400/EUR is 100 EUR of 1100: about 9%. Dividing raw forint by
	// euros, as the sheet did, would give 3636%.
	for _, sh := range shares {
		if sh.Name != "Cash HUF" {
			continue
		}
		if sh.Share < 0.09 || sh.Share > 0.092 {
			t.Fatalf("Cash HUF share = %.6f, want about 0.0909 — convert before dividing", sh.Share)
		}
	}
}

// TestRunway_PerPeriodBurnRate is the direct regression test for deviation D7.
// Under the sheet's absolute references, recording a later month's spending
// rewrote every earlier month's runway.
func TestRunway_PerPeriodBurnRate(t *testing.T) {
	c := newCapitalHarness(t)
	acct := c.accountByName(c.userID, "Cash EUR")
	food := c.categoryByName(c.userID, "Food")
	txns := c.transactionService()

	c.record(t, "Cash EUR", "2026-01", 1200000, "EUR") // 12,000 liquid
	c.record(t, "Cash EUR", "2026-02", 1200000, "EUR")
	if _, err := txns.Create(c.ctx, c.userID,
		"EUR", newExpense(acct.ID, food.ID, "2026-01-10", 100000, "EUR")); err != nil {
		t.Fatalf("seeding January spend: %v", err)
	}

	before := capital.Runway(c.load(t, "2026-01", "2026-02"), "2026-01", capital.BurnTrailing3)
	if before == nil {
		t.Fatal("January runway is nil")
	}

	// A much larger February changes February's runway and must leave January's
	// exactly where it was.
	if _, err := txns.Create(c.ctx, c.userID,
		"EUR", newExpense(acct.ID, food.ID, "2026-02-10", 500000, "EUR")); err != nil {
		t.Fatalf("seeding February spend: %v", err)
	}
	data := c.load(t, "2026-01", "2026-02")
	after := capital.Runway(data, "2026-01", capital.BurnTrailing3)
	if after == nil {
		t.Fatal("January runway is nil after the later month landed")
	}
	if *before != *after {
		t.Fatalf("January runway moved from %.6f to %.6f when February changed; "+
			"each period must use its own burn rate", *before, *after)
	}

	february := capital.Runway(data, "2026-02", capital.BurnTrailing3)
	if february == nil || *february >= *after {
		t.Fatalf("February runway = %v, want it shorter than January's %.6f", february, *after)
	}
}

func TestRunway_ZeroBurn_ReturnsNil(t *testing.T) {
	c := newCapitalHarness(t)
	c.record(t, "Cash EUR", "2026-01", 500000, "EUR")

	data := c.load(t, "2026-01", "2026-01")
	// Nothing was spent and nothing planned, so there is no burn rate. "Forever"
	// is not a number of months.
	if got := capital.Runway(data, "2026-01", capital.BurnTrailing3); got != nil {
		t.Fatalf("runway = %v, want nil rather than infinity", *got)
	}
}

func TestChange_SplitsRealAndFX(t *testing.T) {
	c := newCapitalHarness(t)
	// The hryvnia halves against the euro while the balance does not move.
	c.rate(t, "UAH", "40", "2026-01")
	c.rate(t, "UAH", "50", "2026-02")
	c.record(t, "Banks UAH", "2026-01", 4000000, "UAH") // 40,000 UAH -> 1000 EUR
	c.record(t, "Banks UAH", "2026-02", 4000000, "UAH") // same money  ->  800 EUR

	data := c.load(t, "2026-02", "2026-02")
	ch := capital.Change(data, "2026-02")
	if !ch.Recorded {
		t.Fatal("the change is not recorded")
	}
	if ch.Real.Minor != 0 {
		t.Fatalf("real = %d, want 0 — not a single hryvnia moved", ch.Real.Minor)
	}
	if ch.Total.Minor != ch.FX.Minor {
		t.Fatalf("total %d and fx %d must agree when nothing real happened",
			ch.Total.Minor, ch.FX.Minor)
	}
	if ch.Total.Minor != -20000 {
		t.Fatalf("total = %d, want -20000: 1000 EUR became 800", ch.Total.Minor)
	}
}

func TestChange_QuantitiesOnly(t *testing.T) {
	c := newCapitalHarness(t)
	c.rate(t, "UAH", "40", "2026-01")
	c.rate(t, "UAH", "40", "2026-02")
	c.record(t, "Banks UAH", "2026-01", 4000000, "UAH")
	c.record(t, "Banks UAH", "2026-02", 6000000, "UAH") // saved 20,000 UAH

	data := c.load(t, "2026-02", "2026-02")
	ch := capital.Change(data, "2026-02")
	if ch.FX.Minor != 0 {
		t.Fatalf("fx = %d, want 0 — the rate did not move", ch.FX.Minor)
	}
	if ch.Real.Minor != 50000 {
		t.Fatalf("real = %d, want 50000: 20,000 UAH at 40/EUR", ch.Real.Minor)
	}
	if ch.Total.Minor != ch.Real.Minor {
		t.Fatalf("total %d must equal real %d", ch.Total.Minor, ch.Real.Minor)
	}
}

// TestChange_FirstPeriod_UsesOpeningSnapshot guards against the sheet's P59,
// where a real month compared against an empty one produced a phantom -26,581.
func TestChange_FirstPeriod_UsesOpeningSnapshot(t *testing.T) {
	c := newCapitalHarness(t)
	c.record(t, "Cash EUR", "2026-02", 500000, "EUR")

	// No December figure, so February has nothing to compare against.
	data := c.load(t, "2026-02", "2026-02")
	if ch := capital.Change(data, "2026-02"); ch.Recorded {
		t.Fatalf("change = %+v, want unrecorded: there is no previous month", ch)
	}

	// Record the opening balance and the change becomes real.
	c.record(t, "Cash EUR", "2026-01", 300000, "EUR")
	data = c.load(t, "2026-02", "2026-02")
	ch := capital.Change(data, "2026-02")
	if !ch.Recorded || ch.Total.Minor != 200000 {
		t.Fatalf("change = %+v, want a recorded 2000.00", ch)
	}
}

func TestSnapshot_SkippedMonthIsGap(t *testing.T) {
	c := newCapitalHarness(t)
	c.record(t, "Cash EUR", "2026-01", 100000, "EUR")
	c.record(t, "Cash EUR", "2026-03", 300000, "EUR")

	data := c.load(t, "2026-01", "2026-03")
	if data.Recorded["2026-02"] {
		t.Fatal("February has no snapshot and must not be recorded")
	}
	if got := capital.General(data, "2026-02"); got != nil {
		t.Fatalf("February net worth = %+v, want nil — a gap is never interpolated", got)
	}
}

func TestSnapshot_BackdatedRipples(t *testing.T) {
	c := newCapitalHarness(t)
	c.record(t, "Cash EUR", "2026-02", 500000, "EUR")

	before := capital.Change(c.load(t, "2026-02", "2026-02"), "2026-02")
	if before.Recorded {
		t.Fatal("there is nothing to compare February against yet")
	}

	// Filling in January retrospectively changes February's answer.
	c.record(t, "Cash EUR", "2026-01", 400000, "EUR")
	after := capital.Change(c.load(t, "2026-02", "2026-02"), "2026-02")
	if !after.Recorded || after.Total.Minor != 100000 {
		t.Fatalf("change = %+v, want a recorded 1000.00 once January exists", after)
	}
}

func TestReconciliation_DriftReported(t *testing.T) {
	c := newCapitalHarness(t)
	acct := c.accountByName(c.userID, "Cash EUR")
	food := c.categoryByName(c.userID, "Food")
	txns := c.transactionService()

	if _, err := txns.Create(c.ctx, c.userID,
		"EUR", newExpense(acct.ID, food.ID, "2026-01-10", 25000, "EUR")); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	c.record(t, "Cash EUR", "2026-01", 100000, "EUR")

	drift, err := c.svc.Reconciliation(c.ctx, c.userID, "2026-01", "EUR")
	if err != nil {
		t.Fatalf("Reconciliation: %v", err)
	}
	var row *capital.Drift
	for i := range drift {
		if drift[i].AccountID == acct.ID {
			row = &drift[i]
		}
	}
	if row == nil {
		t.Fatal("Cash EUR is missing from the reconciliation")
	}
	if row.Snapshot == nil || row.Snapshot.Minor != 100000 {
		t.Fatalf("snapshot = %+v, want 1000.00", row.Snapshot)
	}
	if row.Implied == nil || row.Implied.Minor != -25000 {
		t.Fatalf("implied = %+v, want -250.00 from the one expense", row.Implied)
	}
	if row.Difference == nil || row.Difference.Minor != 125000 {
		t.Fatalf("difference = %+v, want 1250.00", row.Difference)
	}

	// The snapshot is authoritative and nothing is corrected behind the user's back.
	data := c.load(t, "2026-01", "2026-01")
	if got := capital.Value(data, acct.ID, "2026-01"); got == nil || got.Minor != 100000 {
		t.Fatalf("value = %+v, want the snapshot's 1000.00, unchanged by the drift", got)
	}
}

func TestCapitalUserIsolation(t *testing.T) {
	c := newCapitalHarness(t)
	other := c.user("other@example.test")
	c.record(t, "Cash EUR", "2026-01", 100000, "EUR")

	snapshots, err := c.svc.ListByPeriod(c.ctx, other.ID, "2026-01")
	if err != nil {
		t.Fatalf("ListByPeriod: %v", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("another user sees %d snapshots, want 0", len(snapshots))
	}

	md := metrics.Data{BaseCurrency: "EUR"}
	data, err := c.svc.Load(c.ctx, other.ID, "2026-01", "2026-01", "EUR", md, capital.Contemporaneous)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := capital.General(data, "2026-01"); got != nil {
		t.Fatalf("another user's net worth = %+v, want nil", got)
	}

	// And the owner's account id is not writable from the other session.
	owned := c.accountByName(c.userID, "Cash EUR")
	if _, err := c.svc.Upsert(c.ctx, other.ID, owned.ID, "2026-01", "EUR", capital.Input{
		Amount: ptrMoney(money.New(1, "EUR")),
	}); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("writing another user's account = %v, want ErrNotFound", err)
	}
}

func TestBurnRates_LegacyBlendUsesEachPeriod(t *testing.T) {
	c := newCapitalHarness(t)
	acct := c.accountByName(c.userID, "Cash EUR")
	food := c.categoryByName(c.userID, "Food")
	txns := c.transactionService()
	budgets := c.budgetService()

	if _, err := budgets.Upsert(c.ctx, c.userID, food.ID, "2026-01", money.New(30000, "EUR")); err != nil {
		t.Fatalf("planning: %v", err)
	}
	if _, err := txns.Create(c.ctx, c.userID,
		"EUR", newExpense(acct.ID, food.ID, "2026-01-10", 60000, "EUR")); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	md, _, err := metrics.NewService(NewMetricsRepo(c.db), c.categoryService()).
		Load(c.ctx, c.userID, "2026-01", "2026-01", "EUR")
	if err != nil {
		t.Fatalf("loading metrics: %v", err)
	}
	rates := capital.BurnRatesFor(md, "2026-01")

	// Food is essential, so the planned minimum is its 300.00.
	if got := rates[capital.BurnEssentialPlanned]; got.Minor != 30000 {
		t.Fatalf("essential planned = %d, want 30000", got.Minor)
	}
	if got := rates[capital.BurnTrailing3]; got.Minor != 60000 {
		t.Fatalf("trailing 3 = %d, want 60000 — the one recorded month", got.Minor)
	}
	// (300 + 600 + 300) / 3 = 400.
	if got := rates[capital.BurnLegacyBlend]; got.Minor != 40000 {
		t.Fatalf("legacy blend = %d, want 40000", got.Minor)
	}
}
