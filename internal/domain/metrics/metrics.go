// Package metrics is the spreadsheet as code: every formula from
// docs/07-metrics-and-budgets.md as a pure function over loaded data, which is
// what makes the Excel parity tests possible.
//
// Two rules run through the whole package:
//
//   - **Absent is not zero.** A metric over a period with no data returns nil.
//     The workbook used -1 as a sentinel and then summed it, which is how `Hobby`
//     came to report a total of -1 (appendix §A.8 deviation D1).
//   - **No I/O.** Everything takes a Data snapshot, loaded once per request. That
//     is what lets the parity suite run the real formulas without a server.
package metrics

import (
	"math/big"
	"sort"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// Data is the snapshot every metric is computed over. It is loaded once per
// request, so a twelve-month report is one round of queries rather than 12 x N.
type Data struct {
	BaseCurrency string
	// Periods are the months the report covers, ascending. A period may be
	// present here and absent from Recorded: that is the difference between "in
	// range" and "has data".
	Periods []period.Period
	// Recorded marks the periods holding at least one transaction. This is the
	// single most important field: it is what the -1 sentinel was trying to say.
	Recorded map[period.Period]bool

	Categories []category.Category
	// Spend is category -> period -> amount, for recorded periods only.
	Spend map[int64]map[period.Period]money.Money
	// AverageBase is Spend less the rows flagged exclude_from_average. A flagged
	// outlier still counts in Spend and Total; it just stops one 10,248 January
	// from making the mean meaningless.
	AverageBase map[int64]map[period.Period]money.Money
	Income      map[period.Period]money.Money
	// Planned is category -> period -> plan.
	Planned map[int64]map[period.Period]money.Money
}

// BudgetState is the state of one figure against its plan. There is one
// implementation of the precedence, in package budget, and this aliases it — a
// second copy would be a second answer.
type BudgetState = budget.State

// Thresholds are the two knobs behind the states.
type Thresholds = budget.Thresholds

// Spend is the amount spent in one category in one period.
//
// nil means the period recorded nothing at all. Zero means it recorded nothing
// in this category, which is a different fact and drags the average down exactly
// as the workbook's AVERAGEIF does.
func Spend(d Data, categoryID int64, p period.Period) *money.Money {
	if !d.Recorded[p] {
		return nil
	}
	if v, ok := d.Spend[categoryID][p]; ok {
		out := v
		return &out
	}
	zero := money.New(0, d.BaseCurrency)
	return &zero
}

// Average is the mean spend over the recorded periods — the workbook's column C,
// `IFERROR(AVERAGEIF(D9:P9,"<>-1"),0)`, with the sentinel replaced by an honest
// absence and the IFERROR's 0 replaced by nil.
//
// Rows flagged exclude_from_average are left out of the numerator and the
// denominator both.
func Average(d Data, categoryID int64) *money.Money {
	sum := int64(0)
	count := int64(0)
	for _, p := range d.Periods {
		if !d.Recorded[p] {
			continue
		}
		count++
		if v, ok := d.AverageBase[categoryID][p]; ok {
			sum += v.Minor
		}
	}
	if count == 0 {
		return nil
	}
	out := money.New(divRound(sum, count), d.BaseCurrency)
	return &out
}

// AverageRatio is Average as an exact ratio, for the parity suite and for any
// caller that must not lose the fraction of a cent to rounding.
func AverageRatio(d Data, categoryID int64) *big.Rat {
	sum := int64(0)
	count := int64(0)
	for _, p := range d.Periods {
		if !d.Recorded[p] {
			continue
		}
		count++
		if v, ok := d.AverageBase[categoryID][p]; ok {
			sum += v.Minor
		}
	}
	if count == 0 {
		return nil
	}
	return new(big.Rat).SetFrac64(sum, count*100)
}

// Total is the sum over recorded periods — the workbook's column T.
//
// Deviation D1: the sheet's `SUM(E9:P9)` includes the -1 sentinel, so `T9` reads
// 8123 against a true 8124 and `Hobby`, which spent nothing all year, reads -1.
// Absent periods are excluded here.
func Total(d Data, categoryID int64) *money.Money {
	sum := int64(0)
	found := false
	for _, p := range d.Periods {
		if !d.Recorded[p] {
			continue
		}
		found = true
		if v, ok := d.Spend[categoryID][p]; ok {
			sum += v.Minor
		}
	}
	if !found {
		return nil
	}
	out := money.New(sum, d.BaseCurrency)
	return &out
}

// SpendTotal is the sum across expense categories for one period — row 27.
//
// Deviation D2: the sheet used `SUM` in one column and `SUMIF(...,"<>-1")` in
// another, so two columns of the same row meant different things. There is one
// definition here.
func SpendTotal(d Data, p period.Period) *money.Money {
	if !d.Recorded[p] {
		return nil
	}
	sum := int64(0)
	for _, c := range d.Categories {
		if c.Kind != category.KindExpense {
			continue
		}
		if v, ok := d.Spend[c.ID][p]; ok {
			sum += v.Minor
		}
	}
	out := money.New(sum, d.BaseCurrency)
	return &out
}

// SpendTotalAverage is the mean of SpendTotal over recorded periods — `C27`,
// verified at 42091.7108 / 11 = 3826.519164.
func SpendTotalAverage(d Data) *big.Rat {
	sum := int64(0)
	count := int64(0)
	for _, p := range d.Periods {
		total := SpendTotal(d, p)
		if total == nil {
			continue
		}
		count++
		sum += total.Minor
	}
	if count == 0 {
		return nil
	}
	return new(big.Rat).SetFrac64(sum, count*100)
}

// PossibleMinimum is the planned spend of the essential categories — `B28`,
// verified at 1780.
//
// Deviation D3: `C28` was a stale literal, 2094.948491, left behind by an
// earlier edit. It is always computed here.
func PossibleMinimum(d Data, p period.Period) money.Money {
	sum := int64(0)
	for _, c := range d.Categories {
		if !c.IsEssential || c.Kind != category.KindExpense {
			continue
		}
		if v, ok := d.Planned[c.ID][p]; ok {
			sum += v.Minor
		}
	}
	return money.New(sum, d.BaseCurrency)
}

// PlannedTotal is the sum of every category's plan for a period — `B27`.
func PlannedTotal(d Data, p period.Period) money.Money {
	sum := int64(0)
	for _, c := range d.Categories {
		if c.Kind != category.KindExpense {
			continue
		}
		if v, ok := d.Planned[c.ID][p]; ok {
			sum += v.Minor
		}
	}
	return money.New(sum, d.BaseCurrency)
}

// Income is the income recorded in a period, or nil when nothing was recorded.
func Income(d Data, p period.Period) *money.Money {
	if !d.Recorded[p] {
		return nil
	}
	if v, ok := d.Income[p]; ok {
		out := v
		return &out
	}
	zero := money.New(0, d.BaseCurrency)
	return &zero
}

// Diff is income minus spend — row 32.
func Diff(d Data, p period.Period) *money.Money {
	income, spend := Income(d, p), SpendTotal(d, p)
	if income == nil || spend == nil {
		return nil
	}
	out := money.New(income.Minor-spend.Minor, d.BaseCurrency)
	return &out
}

// SavedPercent is 1 - spend/income — row 33.
//
// nil when income is zero: unknown and zero are different facts, and a division
// by zero is neither. **Never clamped.** January's -3.751067461 says the month
// spent 4.75x its income, which is the most informative figure in the dataset;
// clamping it to 0 would hide exactly the thing worth seeing.
func SavedPercent(d Data, p period.Period) *float64 {
	income, spend := Income(d, p), SpendTotal(d, p)
	if income == nil || spend == nil || income.Minor == 0 {
		return nil
	}
	r := new(big.Rat).SetFrac64(spend.Minor, income.Minor)
	f, _ := new(big.Rat).Sub(big.NewRat(1, 1), r).Float64()
	return &f
}

// Ratio is actual over planned, or nil when either is missing or the plan is zero.
func Ratio(actual, planned *money.Money) *float64 {
	if actual == nil || planned == nil || planned.Minor == 0 {
		return nil
	}
	f, _ := new(big.Rat).SetFrac64(actual.Minor, planned.Minor).Float64()
	return &f
}

// State classifies a figure against its plan (§7.6).
//
// The stage-05 contract wrote this as `State(actual *Money, planned Money, warnPct,
// overMult float64)`. Planned is a pointer here because "no plan recorded" and
// "planned zero" are different facts, and the thresholds travel together because
// they are always read together; the stage file records the refinement.
func State(actual, planned *money.Money, th Thresholds) BudgetState {
	return budget.Classify(actual, planned, th)
}

// Label is the accessible label for a state, so colour is never the only signal.
func Label(s BudgetState) string { return budget.Label(s) }

// RecordedPeriods returns the recorded periods in ascending order.
func RecordedPeriods(d Data) []period.Period {
	out := make([]period.Period, 0, len(d.Periods))
	for _, p := range d.Periods {
		if d.Recorded[p] {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// divRound divides minor units, rounding half away from zero — the same rule as
// every other rounding in the system, applied once, at the end.
func divRound(sum, count int64) int64 {
	if count == 0 {
		return 0
	}
	neg := (sum < 0) != (count < 0)
	a, b := sum, count
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	q := a / b
	if 2*(a%b) >= b {
		q++
	}
	if neg {
		return -q
	}
	return q
}
