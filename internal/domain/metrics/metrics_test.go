package metrics

import (
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/money"
)

const eur = "EUR"

// build makes a snapshot with one expense category, id 1, planned 100.00.
//
// spend maps a period to its amount in minor units; a period absent from
// `recorded` is unrecorded, which is the distinction every test here is about.
func build(recorded []period.Period, all []period.Period, spend map[period.Period]int64) Data {
	d := Data{
		BaseCurrency: eur,
		Periods:      all,
		Recorded:     map[period.Period]bool{},
		Categories: []category.Category{
			{ID: 1, Name: "Food", Kind: category.KindExpense, IsEssential: true},
			{ID: 2, Name: "Hobby", Kind: category.KindExpense},
		},
		Spend:       map[int64]map[period.Period]money.Money{1: {}},
		AverageBase: map[int64]map[period.Period]money.Money{1: {}},
		Income:      map[period.Period]money.Money{},
		Planned:     map[int64]map[period.Period]money.Money{1: {}},
	}
	for _, p := range recorded {
		d.Recorded[p] = true
	}
	for p, minor := range spend {
		d.Spend[1][p] = money.New(minor, eur)
		d.AverageBase[1][p] = money.New(minor, eur)
	}
	for _, p := range all {
		d.Planned[1][p] = money.New(10000, eur)
	}
	return d
}

func TestAverage_ExcludesUnrecorded(t *testing.T) {
	all := []period.Period{"2026-01", "2026-02", "2026-03"}
	// March recorded nothing at all, so it is not in the denominator.
	d := build(all[:2], all, map[period.Period]int64{"2026-01": 10000, "2026-02": 20000})

	got := Average(d, 1)
	if got == nil || got.Minor != 15000 {
		t.Fatalf("average = %+v, want 150.00 — the unrecorded month must not divide", got)
	}
}

func TestAverage_IncludesRecordedZero(t *testing.T) {
	all := []period.Period{"2026-01", "2026-02"}
	// February is recorded but spent nothing in this category. That zero belongs
	// in the mean, exactly as the workbook's AVERAGEIF treats it.
	d := build(all, all, map[period.Period]int64{"2026-01": 10000})

	got := Average(d, 1)
	if got == nil || got.Minor != 5000 {
		t.Fatalf("average = %+v, want 50.00 — a recorded zero drags the mean down", got)
	}
}

func TestAverage_HonoursExcludeFlag(t *testing.T) {
	all := []period.Period{"2026-01", "2026-02"}
	d := build(all, all, map[period.Period]int64{"2026-01": 10000, "2026-02": 1000000})
	// January's 10,000 was a one-off. It stays in the total and leaves the mean.
	d.AverageBase[1] = map[period.Period]money.Money{"2026-01": money.New(10000, eur)}

	average := Average(d, 1)
	if average == nil || average.Minor != 5000 {
		t.Fatalf("average = %+v, want 50.00 with the outlier excluded", average)
	}
	total := Total(d, 1)
	if total == nil || total.Minor != 1010000 {
		t.Fatalf("total = %+v, want 10100.00 — the money was still spent", total)
	}
}

func TestAverage_AllUnrecorded_ReturnsNil(t *testing.T) {
	all := []period.Period{"2026-01", "2026-02"}
	d := build(nil, all, nil)

	if got := Average(d, 1); got != nil {
		t.Fatalf("average = %+v, want nil — no data is not an average of zero", got)
	}
	if got := Total(d, 1); got != nil {
		t.Fatalf("total = %+v, want nil", got)
	}
	if got := Spend(d, 1, "2026-01"); got != nil {
		t.Fatalf("spend = %+v, want nil", got)
	}
}

func TestTotal_SingleRecordedPeriod(t *testing.T) {
	all := []period.Period{"2026-01", "2026-02"}
	d := build(all[:1], all, map[period.Period]int64{"2026-01": 12345})

	got := Total(d, 1)
	if got == nil || got.Minor != 12345 {
		t.Fatalf("total = %+v, want the single recorded period's 123.45", got)
	}
}

func TestSavedPercent_ZeroIncome_ReturnsNil(t *testing.T) {
	all := []period.Period{"2026-01"}
	d := build(all, all, map[period.Period]int64{"2026-01": 10000})
	d.Income["2026-01"] = money.New(0, eur)

	if got := SavedPercent(d, "2026-01"); got != nil {
		t.Fatalf("saved %% = %v, want nil — not 0, not Inf", *got)
	}
}

func TestSavedPercent_NotClamped(t *testing.T) {
	all := []period.Period{"2026-01"}
	// The workbook's January: spend 4.75x income.
	d := build(all, all, map[period.Period]int64{"2026-01": 1356430})
	d.Income["2026-01"] = money.New(285500, eur)

	got := SavedPercent(d, "2026-01")
	if got == nil {
		t.Fatal("saved % is nil")
	}
	if *got > -3.7 || *got < -3.8 {
		t.Fatalf("saved %% = %.6f, want about -3.75 preserved", *got)
	}
}

func TestSpendTotal_UnrecordedIsNil(t *testing.T) {
	all := []period.Period{"2026-01", "2026-02"}
	d := build(all[:1], all, map[period.Period]int64{"2026-01": 10000})

	if got := SpendTotal(d, "2026-02"); got != nil {
		t.Fatalf("spend total = %+v, want nil for an unrecorded month", got)
	}
	if got := Diff(d, "2026-02"); got != nil {
		t.Fatalf("diff = %+v, want nil", got)
	}
	if got := SavedPercent(d, "2026-02"); got != nil {
		t.Fatalf("saved %% = %v, want nil", *got)
	}
}

func TestPossibleMinimum_EssentialOnly(t *testing.T) {
	all := []period.Period{"2026-01"}
	d := build(all, all, nil)
	// Hobby is planned but not essential, so it is outside Possible Minimum.
	d.Planned[2] = map[period.Period]money.Money{"2026-01": money.New(2000, eur)}

	if got := PossibleMinimum(d, "2026-01"); got.Minor != 10000 {
		t.Fatalf("possible minimum = %d, want 10000 — essentials only", got.Minor)
	}
	if got := PlannedTotal(d, "2026-01"); got.Minor != 12000 {
		t.Fatalf("planned total = %d, want 12000 — every category", got.Minor)
	}
}

func TestState_Boundaries(t *testing.T) {
	th := Thresholds{WarnPercent: 10, OverMultiplier: 2}
	planned := money.New(10000, eur)
	at := func(minor int64) *money.Money {
		m := money.New(minor, eur)
		return &m
	}

	cases := []struct {
		name   string
		actual *money.Money
		want   BudgetState
	}{
		// Exactly at plan is amber, not over: it falls inside the sheet's
		// "between B-(B x C2/100) and B" band, which is rule 3 of the §7.6
		// precedence. The stage-05 note "within, not over" means only that it is
		// not classified as over.
		{"exactly planned", at(10000), budget.StateApproaching},
		{"exactly twice planned", at(20000), budget.StateSeverelyOver},
		{"a cent over", at(10001), budget.StateOver},
		{"exactly the warn threshold", at(9000), budget.StateApproaching},
		{"well under", at(1000), budget.StateWithin},
		{"nothing spent", at(0), budget.StateZero},
		{"not recorded", nil, budget.StateNotRecorded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := State(c.actual, &planned, th); got != c.want {
				t.Fatalf("state = %q, want %q", got, c.want)
			}
		})
	}
}

// TestMetrics_PureNoIO keeps the engine a pure function of its Data: the moment
// it can reach a database or the network, the parity suite stops proving
// anything about the formulas.
func TestMetrics_PureNoIO(t *testing.T) {
	forbidden := []string{
		"github.com/antlko/moneyapp/internal/store",
		"net/http",
		"database/sql",
		"os",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(gotoken.NewFileSet(), filepath.Join(".", e.Name()), nil, goparser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad {
					t.Errorf("%s imports %q; metrics must stay pure over its Data", e.Name(), path)
				}
			}
		}
	}
}
