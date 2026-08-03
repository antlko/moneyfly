package budget

import (
	"context"
	"math/big"

	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// State is the budget state of one row, from the precedence in
// docs/07-metrics-and-budgets.md §7.6.
type State string

// The six states. Colour is never the only signal in the UI: each one carries an
// icon and a label (docs/08-ux.md §8.1).
const (
	StateSeverelyOver State = "severely_over"
	StateOver         State = "over"
	StateApproaching  State = "approaching"
	StateWithin       State = "within"
	StateZero         State = "zero"
	StateNotRecorded  State = "not_recorded"
)

// Thresholds are the two knobs behind the states. They come from configuration
// until the per-user setting table lands in stage 07.
type Thresholds struct {
	// WarnPercent is the workbook's C2 (10), so 90-100% of plan is amber.
	WarnPercent float64
	// OverMultiplier was hardcoded 2x in the workbook.
	OverMultiplier float64
}

// Actuals is the spend and income side of the report, supplied by the transaction
// store. A nil amount means the period recorded nothing — never zero.
type Actuals struct {
	// Recorded reports whether the period has any transaction at all. It is what
	// separates "no data" from "spent nothing", which the workbook's -1 sentinel
	// could not.
	Recorded bool
	// SpendByCategory holds base-currency spend per category id, for categories
	// with at least one row.
	SpendByCategory map[int64]money.Money
	SpendTotal      money.Money
	Income          money.Money
	// Unconverted counts rows whose base amount is NULL because no rate was
	// available. They are excluded from the totals, and the count is surfaced
	// rather than hidden.
	Unconverted int
}

// ActualsSource is the slice of the transaction store this report needs.
type ActualsSource interface {
	PeriodActuals(ctx context.Context, userID int64, p period.Period, baseCurrency string) (Actuals, error)
}

// CategoryRow is one line of the report.
type CategoryRow struct {
	CategoryID  int64
	Name        string
	Icon        string
	Color       string
	IsEssential bool
	// Actual is nil when the period recorded nothing at all.
	Actual *money.Money
	// Planned is nil when no budget row exists — not planned is not the same as
	// planned zero.
	Planned *money.Money
	Ratio   *float64
	State   State
}

// Report is the plan-versus-actual answer for one period.
type Report struct {
	Period          period.Period
	Valuation       string
	BaseCurrency    string
	SpendTotal      *money.Money
	PlannedTotal    money.Money
	Income          *money.Money
	Diff            *money.Money
	SavedPercent    *float64
	PossibleMinimum money.Money
	Unconverted     int
	Categories      []CategoryRow
}

// Report builds the single-period budget report.
//
// Averages and multi-period aggregates are stage 05; this is deliberately one
// period only.
func (s *Service) Report(
	ctx context.Context,
	userID int64,
	p period.Period,
	baseCurrency string,
	actuals ActualsSource,
	th Thresholds,
) (Report, error) {
	if !p.Valid() {
		return Report{}, apperr.Validation("period", "must be a month in YYYY-MM form, got %q", p)
	}
	cats, err := s.categories.List(ctx, userID, category.KindExpense, false)
	if err != nil {
		return Report{}, err
	}
	plans, err := s.repo.ListByPeriod(ctx, userID, p)
	if err != nil {
		return Report{}, err
	}
	plannedBy := make(map[int64]money.Money, len(plans))
	for _, b := range plans {
		plannedBy[b.CategoryID] = b.Planned
	}
	act, err := actuals.PeriodActuals(ctx, userID, p, baseCurrency)
	if err != nil {
		return Report{}, err
	}

	out := Report{
		Period:          p,
		Valuation:       "contemporaneous",
		BaseCurrency:    baseCurrency,
		PlannedTotal:    money.New(0, baseCurrency),
		PossibleMinimum: money.New(0, baseCurrency),
		Unconverted:     act.Unconverted,
		Categories:      make([]CategoryRow, 0, len(cats)),
	}

	for _, c := range cats {
		row := CategoryRow{
			CategoryID: c.ID, Name: c.Name, Icon: c.Icon, Color: c.Color, IsEssential: c.IsEssential,
		}
		if planned, ok := plannedBy[c.ID]; ok {
			p := planned
			row.Planned = &p
			out.PlannedTotal.Minor += planned.Minor
			if c.IsEssential {
				out.PossibleMinimum.Minor += planned.Minor
			}
		}
		if spend, ok := act.SpendByCategory[c.ID]; ok {
			a := spend
			row.Actual = &a
		} else if act.Recorded {
			// The period has data, so a category with no rows genuinely spent
			// nothing. Without data at all, it stays nil.
			zero := money.New(0, baseCurrency)
			row.Actual = &zero
		}
		row.Ratio = ratio(row.Actual, row.Planned)
		row.State = Classify(row.Actual, row.Planned, th)
		out.Categories = append(out.Categories, row)
	}

	if act.Recorded {
		total := act.SpendTotal
		out.SpendTotal = &total
		income := act.Income
		out.Income = &income
		diff, err := income.Sub(total)
		if err != nil {
			return Report{}, err
		}
		out.Diff = &diff
		out.SavedPercent = savedPercent(total, income)
	}
	return out, nil
}

// Classify applies the precedence from §7.6. First match wins.
//
// Two deliberate orderings:
//
//   - `not recorded` is tested first, because a NULL cannot be compared at all.
//   - `zero` is tested before the over-budget rules, matching the workbook's own
//     conditional-format priority (appendix §A.6 rule 2). Under the §7.6 ordering
//     a zero plan with zero spend would classify as severely over, which is
//     nonsense.
func Classify(actual, planned *money.Money, th Thresholds) State {
	if actual == nil {
		return StateNotRecorded
	}
	if actual.Minor == 0 {
		return StateZero
	}
	if planned == nil {
		// Nothing was planned, so there is no plan to be over. The UI labels this
		// row "no plan" rather than claiming it is within budget.
		return StateWithin
	}
	over := th.OverMultiplier
	if over <= 1 {
		over = 2
	}
	warn := th.WarnPercent
	if warn < 0 || warn >= 100 {
		warn = 10
	}

	spend := big.NewRat(actual.Minor, 1)
	plan := big.NewRat(planned.Minor, 1)

	severe := new(big.Rat).Mul(plan, ratFromFloat(over))
	// The 2x boundary itself is severely over, per the stage-03 test table.
	if spend.Cmp(severe) >= 0 {
		return StateSeverelyOver
	}
	if spend.Cmp(plan) > 0 {
		return StateOver
	}
	// The amber threshold is computed as plan x (100 - warn) / 100 in exact
	// rationals, not as plan x (1 - warn/100). The binary value of 0.9 is a shade
	// above nine tenths, which would put exactly 90% of plan just under the line —
	// and 90% of plan is precisely where the workbook's C2 rule starts.
	hundred := big.NewRat(100, 1)
	factor := new(big.Rat).Quo(new(big.Rat).Sub(hundred, ratFromFloat(warn)), hundred)
	if spend.Cmp(new(big.Rat).Mul(plan, factor)) >= 0 {
		return StateApproaching
	}
	return StateWithin
}

// Label is the accessible label for a state, so colour is never the only signal.
func Label(s State) string {
	switch s {
	case StateSeverelyOver:
		return "severely over budget"
	case StateOver:
		return "over budget"
	case StateApproaching:
		return "approaching budget"
	case StateWithin:
		return "within budget"
	case StateZero:
		return "nothing spent"
	default:
		return "not recorded"
	}
}

func ratio(actual, planned *money.Money) *float64 {
	if actual == nil || planned == nil || planned.Minor == 0 {
		return nil
	}
	r, _ := new(big.Rat).SetFrac64(actual.Minor, planned.Minor).Float64()
	return &r
}

// savedPercent is 1 - spend/income, unclamped: January was -3.75 and that is the
// truth. Zero income yields nil, because unknown and zero are different facts.
func savedPercent(spend, income money.Money) *float64 {
	if income.Minor == 0 {
		return nil
	}
	r := new(big.Rat).SetFrac64(spend.Minor, income.Minor)
	saved := new(big.Rat).Sub(big.NewRat(1, 1), r)
	f, _ := saved.Float64()
	return &f
}

func ratFromFloat(f float64) *big.Rat {
	r := new(big.Rat)
	// SetFloat64 is exact for the binary value; multiplying by an integer plan
	// keeps the comparison free of accumulated error.
	if r.SetFloat64(f) == nil {
		return big.NewRat(1, 1)
	}
	return r
}
