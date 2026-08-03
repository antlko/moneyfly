package store

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// The Excel parity gate.
//
// The fixture is the workbook's own numbers, extracted by
// testdata/parity/generate.py. Each figure carries the value the app must
// produce and, where the sheet disagrees, the sheet's value plus the deviation
// ids that explain why — so nobody can quietly "fix" a number without recording
// the reason (docs/appendix-excel-parity.md §A.8).
//
// The test seeds a real database from the sheet's spend grid and reads every
// metric back through the real loader, so it exercises the SQL as well as the
// formulas. The one thing it must never do is assert a value the app computed.

const parityFixture = "../../testdata/parity/workbook-2025-2026.json"

// knownDeviations are the documented divergences. A fixture entry citing
// anything else fails TestParity_DeviationsDocumented.
var knownDeviations = map[string]bool{
	"D1": true, "D2": true, "D3": true, "D4": true, "D5": true, "D6": true,
	"D7": true, "D8": true, "D9": true, "D10": true, "D11": true,
}

type parityMoney struct {
	ExpectedMinor int64    `json:"expected_minor"`
	Workbook      *float64 `json:"workbook"`
	WorkbookMinor *int64   `json:"workbook_minor"`
	Deviations    []string `json:"deviations"`
}

type parityRatio struct {
	Expected   float64  `json:"expected"`
	Workbook   *float64 `json:"workbook"`
	Deviations []string `json:"deviations"`
}

type parityCategory struct {
	Row          int                       `json:"row"`
	SheetName    string                    `json:"sheet_name"`
	Name         string                    `json:"name"`
	Essential    bool                      `json:"essential"`
	PlannedMinor int64                     `json:"planned_minor"`
	Spend        map[period.Period]int64   `json:"spend"`
	SpendSheet   map[period.Period]float64 `json:"spend_sheet"`
	Average      parityRatio               `json:"average"`
	Total        parityMoney               `json:"total"`
}

type parityFile struct {
	Source     string                 `json:"source"`
	Base       string                 `json:"base_currency"`
	Periods    []period.Period        `json:"periods"`
	Recorded   map[period.Period]bool `json:"recorded"`
	Categories []parityCategory       `json:"categories"`
	Rollups    struct {
		PlannedTotal           parityMoney                   `json:"planned_total"`
		PossibleMinimum        parityMoney                   `json:"possible_minimum"`
		PossibleMinimumAverage parityRatio                   `json:"possible_minimum_average"`
		SpendTotal             map[period.Period]parityMoney `json:"spend_total"`
		SpendTotalAverage      parityRatio                   `json:"spend_total_average"`
		Income                 map[period.Period]parityMoney `json:"income"`
		IncomePlanned          parityMoney                   `json:"income_planned"`
		Diff                   map[period.Period]parityMoney `json:"diff"`
		DiffPlanned            parityMoney                   `json:"diff_planned"`
		SavedPercent           map[period.Period]parityRatio `json:"saved_percent"`
		SavedPercentPlanned    parityRatio                   `json:"saved_percent_planned"`
	} `json:"rollups"`
	// Capital is the sheet's second half, decoded in parity_capital_test.go.
	Capital parityCapital `json:"capital"`
}

func loadParity(t *testing.T) parityFile {
	t.Helper()
	raw, err := os.ReadFile(parityFixture)
	if err != nil {
		t.Fatalf("reading the parity fixture: %v", err)
	}
	var f parityFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decoding the parity fixture: %v", err)
	}
	if len(f.Categories) != 18 {
		t.Fatalf("fixture has %d categories, want the workbook's 18", len(f.Categories))
	}
	return f
}

// parityWorld is a database seeded from the sheet, plus the loaded snapshot.
type parityWorld struct {
	fixture parityFile
	data    metrics.Data
	byName  map[string]category.Category
}

// seedParity writes one transaction per non-zero cell, plus the income line
// items the sheet never had, then loads the snapshot through the real loader.
func seedParity(t *testing.T) parityWorld {
	t.Helper()
	fixture := loadParity(t)

	h := newHarness(t)
	u := h.user("parity@example.test")
	txns := h.transactionService()
	budgets := h.budgetService()

	cats, err := h.categoryService().List(h.ctx, u.ID, "", true)
	if err != nil {
		t.Fatalf("listing categories: %v", err)
	}
	byName := map[string]category.Category{}
	for _, c := range cats {
		byName[c.Name] = c
	}

	acct := h.accountByName(u.ID, "Cash EUR")
	first, last := fixture.Periods[0], fixture.Periods[len(fixture.Periods)-1]

	for _, fc := range fixture.Categories {
		c, ok := byName[fc.Name]
		if !ok {
			t.Fatalf("the seeded taxonomy has no category %q (sheet row %d, %q)",
				fc.Name, fc.Row, fc.SheetName)
		}
		if c.IsEssential != fc.Essential {
			t.Errorf("category %q essential = %v, want %v from the workbook's B28 formula",
				fc.Name, c.IsEssential, fc.Essential)
		}
		// The plan is one figure per category applied to every month, which is how
		// the sheet's column B works.
		if _, err := budgets.Bulk(h.ctx, u.ID, first, last, []budget.BulkItem{
			{CategoryID: c.ID, Planned: money.New(fc.PlannedMinor, fixture.Base)},
		}); err != nil {
			t.Fatalf("planning %q: %v", fc.Name, err)
		}

		for _, p := range fixture.Periods {
			minor, ok := fc.Spend[p]
			if !ok || minor == 0 {
				// A recorded zero is the absence of a row inside a month that has
				// other rows — not a zero-amount transaction.
				continue
			}
			in := newExpense(acct.ID, c.ID, string(p)+"-15", minor, fixture.Base)
			if _, err := txns.Create(h.ctx, u.ID, fixture.Base, in); err != nil {
				t.Fatalf("seeding %s %s: %v", fc.Name, p, err)
			}
		}
	}

	salary := byName["Salary"]
	for p, entry := range fixture.Rollups.Income {
		if entry.ExpectedMinor == 0 {
			continue
		}
		in := newExpense(acct.ID, salary.ID, string(p)+"-01", entry.ExpectedMinor, fixture.Base)
		in.Kind = transaction.KindIncome
		if _, err := txns.Create(h.ctx, u.ID, fixture.Base, in); err != nil {
			t.Fatalf("seeding income %s: %v", p, err)
		}
	}

	svc := metrics.NewService(NewMetricsRepo(h.db), h.categoryService())
	data, _, err := svc.Load(h.ctx, u.ID, first, last, fixture.Base)
	if err != nil {
		t.Fatalf("loading the metrics snapshot: %v", err)
	}
	return parityWorld{fixture: fixture, data: data, byName: byName}
}

func TestParity_SpendGrid(t *testing.T) {
	w := seedParity(t)

	for _, fc := range w.fixture.Categories {
		c := w.byName[fc.Name]
		for _, p := range w.fixture.Periods {
			got := metrics.Spend(w.data, c.ID, p)
			if !w.fixture.Recorded[p] {
				// July is unfilled. The sheet says -1; the app must say nothing at
				// all, which is the entire point of the exercise.
				if got != nil {
					t.Errorf("%s %s = %+v, want nil for an unrecorded month", fc.Name, p, got)
				}
				continue
			}
			want := fc.Spend[p] // a missing key inside a recorded month is a recorded zero
			if got == nil {
				t.Errorf("%s %s is nil, want %d minor", fc.Name, p, want)
				continue
			}
			if got.Minor != want {
				t.Errorf("%s %s = %d minor, want %d (sheet %v)",
					fc.Name, p, got.Minor, want, fc.SpendSheet[p])
			}
		}
	}
}

func TestParity_Averages(t *testing.T) {
	w := seedParity(t)

	for _, fc := range w.fixture.Categories {
		c := w.byName[fc.Name]
		got := metrics.AverageRatio(w.data, c.ID)
		if got == nil {
			t.Errorf("%s average is nil, want %v", fc.Name, fc.Average.Expected)
			continue
		}
		f, _ := got.Float64()
		if !closeEnough(f, fc.Average.Expected) {
			t.Errorf("%s average = %.10f, want %.10f", fc.Name, f, fc.Average.Expected)
		}
	}

	// The anchor from §A.9: 8124 over 11 recorded months.
	house := metrics.AverageRatio(w.data, w.byName["House"].ID)
	f, _ := house.Float64()
	if !closeEnough(f, 738.5454545) {
		t.Fatalf("House average = %.10f, want 738.5454545", f)
	}
}

func TestParity_Totals_SentinelFixed(t *testing.T) {
	w := seedParity(t)

	for _, fc := range w.fixture.Categories {
		c := w.byName[fc.Name]
		got := metrics.Total(w.data, c.ID)
		if got == nil {
			t.Errorf("%s total is nil", fc.Name)
			continue
		}
		if got.Minor != fc.Total.ExpectedMinor {
			t.Errorf("%s total = %d minor, want %d", fc.Name, got.Minor, fc.Total.ExpectedMinor)
		}
	}

	// D1 made concrete: the sheet sums the -1 sentinel into both of these.
	if got := metrics.Total(w.data, w.byName["House"].ID); got.Minor != 812400 {
		t.Fatalf("House total = %d, want 812400 (the sheet's T9 reads 8123)", got.Minor)
	}
	if got := metrics.Total(w.data, w.byName["Hobby"].ID); got.Minor != 0 {
		t.Fatalf("Hobby total = %d, want 0 — the sheet's T19 reads -1 for a year of no spending",
			got.Minor)
	}
}

func TestParity_SpendTotal(t *testing.T) {
	w := seedParity(t)

	for _, p := range w.fixture.Periods {
		got := metrics.SpendTotal(w.data, p)
		want, ok := w.fixture.Rollups.SpendTotal[p]
		if !ok {
			if got != nil {
				t.Errorf("spend total %s = %+v, want nil for an unrecorded month", p, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("spend total %s is nil, want %d minor", p, want.ExpectedMinor)
			continue
		}
		if got.Minor != want.ExpectedMinor {
			t.Errorf("spend total %s = %d minor, want %d", p, got.Minor, want.ExpectedMinor)
		}
	}

	average := metrics.SpendTotalAverage(w.data)
	if average == nil {
		t.Fatal("the spend-total average is nil")
	}
	f, _ := average.Float64()
	if !closeEnough(f, w.fixture.Rollups.SpendTotalAverage.Expected) {
		t.Fatalf("spend total average = %.10f, want %.10f",
			f, w.fixture.Rollups.SpendTotalAverage.Expected)
	}
	// §A.9: 42091.7108 / 11.
	if !closeEnough(f, 3826.519164) {
		t.Fatalf("spend total average = %.10f, want the workbook's 3826.519164 within tolerance", f)
	}
}

func TestParity_PossibleMinimum(t *testing.T) {
	w := seedParity(t)

	want := w.fixture.Rollups.PossibleMinimum.ExpectedMinor
	for _, p := range w.fixture.Periods {
		got := metrics.PossibleMinimum(w.data, p)
		if got.Minor != want {
			t.Fatalf("possible minimum %s = %d minor, want %d", p, got.Minor, want)
		}
	}
	if want != 178000 {
		t.Fatalf("the fixture's possible minimum is %d, want the workbook's 1780", want)
	}

	planned := metrics.PlannedTotal(w.data, w.fixture.Periods[0])
	if planned.Minor != w.fixture.Rollups.PlannedTotal.ExpectedMinor {
		t.Fatalf("planned total = %d minor, want %d",
			planned.Minor, w.fixture.Rollups.PlannedTotal.ExpectedMinor)
	}
}

func TestParity_SavedPercent(t *testing.T) {
	w := seedParity(t)

	for _, p := range w.fixture.Periods {
		got := metrics.SavedPercent(w.data, p)
		want, ok := w.fixture.Rollups.SavedPercent[p]
		if !ok {
			if got != nil {
				t.Errorf("saved %% %s = %v, want nil", p, *got)
			}
			continue
		}
		if got == nil {
			t.Errorf("saved %% %s is nil, want %.10f", p, want.Expected)
			continue
		}
		if !closeEnough(*got, want.Expected) {
			t.Errorf("saved %% %s = %.10f, want %.10f", p, *got, want.Expected)
		}
	}

	// The month that matters: January spent 4.75x its income. Unclamped.
	january := metrics.SavedPercent(w.data, "2026-01")
	if january == nil {
		t.Fatal("January saved % is nil")
	}
	if !closeEnough(*january, -3.751067461) {
		t.Fatalf("January saved %% = %.10f, want -3.751067461 unclamped", *january)
	}
	if *january > -1 {
		t.Fatalf("saved %% was clamped: %.10f", *january)
	}

	// The planned figure, 1 - 2350/2800.
	if !closeEnough(w.fixture.Rollups.SavedPercentPlanned.Expected, 0.1607142857) {
		t.Fatalf("planned saved %% = %.10f, want 0.1607142857",
			w.fixture.Rollups.SavedPercentPlanned.Expected)
	}
}

func TestParity_IncomeAndDiff(t *testing.T) {
	w := seedParity(t)

	for _, p := range w.fixture.Periods {
		income := metrics.Income(w.data, p)
		want, ok := w.fixture.Rollups.Income[p]
		if !ok {
			if income != nil {
				t.Errorf("income %s = %+v, want nil", p, income)
			}
			continue
		}
		if income == nil || income.Minor != want.ExpectedMinor {
			t.Errorf("income %s = %+v, want %d minor", p, income, want.ExpectedMinor)
		}

		diff := metrics.Diff(w.data, p)
		wantDiff := w.fixture.Rollups.Diff[p]
		if diff == nil || diff.Minor != wantDiff.ExpectedMinor {
			t.Errorf("diff %s = %+v, want %d minor", p, diff, wantDiff.ExpectedMinor)
		}
	}
}

// TestParity_DeviationsDocumented is the guard against quietly correcting a
// number: every figure that differs from the sheet must say which documented
// deviation it is.
func TestParity_DeviationsDocumented(t *testing.T) {
	fixture := loadParity(t)

	seen := map[string]bool{}
	checkMoney := func(label string, m parityMoney) {
		if m.Workbook == nil {
			return
		}
		if len(m.Deviations) == 0 {
			t.Errorf("%s differs from the workbook with no deviation id", label)
			return
		}
		for _, d := range m.Deviations {
			seen[d] = true
			if !knownDeviations[d] {
				t.Errorf("%s cites unknown deviation %q", label, d)
			}
		}
	}
	checkRatio := func(label string, r parityRatio) {
		if r.Workbook == nil {
			return
		}
		if len(r.Deviations) == 0 {
			t.Errorf("%s differs from the workbook with no deviation id", label)
			return
		}
		for _, d := range r.Deviations {
			seen[d] = true
			if !knownDeviations[d] {
				t.Errorf("%s cites unknown deviation %q", label, d)
			}
		}
	}

	for _, c := range fixture.Categories {
		checkMoney(c.Name+" total", c.Total)
		checkRatio(c.Name+" average", c.Average)
	}
	for p, m := range fixture.Rollups.SpendTotal {
		checkMoney("spend total "+string(p), m)
	}
	for p, m := range fixture.Rollups.Diff {
		checkMoney("diff "+string(p), m)
	}
	for p, r := range fixture.Rollups.SavedPercent {
		checkRatio("saved percent "+string(p), r)
	}
	checkMoney("possible minimum", fixture.Rollups.PossibleMinimum)
	checkRatio("possible minimum average", fixture.Rollups.PossibleMinimumAverage)
	checkRatio("spend total average", fixture.Rollups.SpendTotalAverage)

	// The sentinel and the rounding are both expected to appear; if they stop
	// appearing, the fixture has been regenerated against different data.
	for _, want := range []string{"D1", "D11"} {
		if !seen[want] {
			t.Errorf("no fixture entry cites %s any more; regenerate or update the appendix", want)
		}
	}

	ids := make([]string, 0, len(seen))
	for d := range seen {
		ids = append(ids, d)
	}
	sort.Strings(ids)
	t.Logf("deviations exercised by the fixture: %v", ids)
}

// closeEnough is the §A.10 tolerance: 1e-6 relative on ratios. Money is compared
// exactly, everywhere, because money is integer minor units.
func closeEnough(got, want float64) bool {
	scale := math.Max(math.Max(math.Abs(got), math.Abs(want)), 1e-12)
	return math.Abs(got-want)/scale <= 1e-6
}
