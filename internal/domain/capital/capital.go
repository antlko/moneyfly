// Package capital is the workbook's capital half as code: monthly snapshots,
// computed parent rollups, allocation, net worth, runway and the change split.
//
// Like package metrics it is pure over a Data snapshot loaded once per request.
// Three of the sheet's defects are fixed here and one capability is genuinely
// new — separating real saving from currency movement, which the workbook could
// never do because its rates carried no date (docs/07-metrics-and-budgets.md §7.5).
package capital

import (
	"math/big"
	"sort"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// Snapshot is one account's recorded balance for one month.
//
// Amount and QuantityNano are exclusive: a balance is either an asserted value
// or a quantity to be priced, never both and never neither. The DDL enforces it.
type Snapshot struct {
	ID        int64
	AccountID int64
	Period    period.Period
	// Amount is the native balance, e.g. 110500 HUF.
	Amount *money.Money
	// QuantityNano is a holding to be priced, e.g. grams of gold.
	QuantityNano *int64
	// BaseAmount is the value in the reporting currency at this period's rate.
	BaseAmount *money.Money
	FxRateID   *int64
	Note       string
}

// Data is the snapshot every capital metric is computed over.
type Data struct {
	BaseCurrency string
	Periods      []period.Period
	// Recorded marks the periods holding at least one snapshot. A month nobody
	// recorded is absent, not zero: the sheet's July read 0 and produced a
	// -26,581 "change in real capital" out of nothing.
	Recorded map[period.Period]bool

	Accounts []account.Account
	// Children maps a parent account to its children. Parents are computed and
	// never stored, which is what stops `Banks FOP` being omitted from `Banks`
	// while counting toward `General`.
	Children map[int64][]int64
	// Values is accountID -> period -> base-currency value, leaves only.
	Values map[int64]map[period.Period]money.Money
	// Native is the same in the account's own currency, for the real/FX split.
	Native map[int64]map[period.Period]money.Money
	// Rates is accountID -> period -> units of the account's currency per one
	// unit of the base currency. It is what prices a quantity change.
	Rates map[int64]map[period.Period]*big.Rat
	// Burn holds the candidate burn rates per period, computed from the spending
	// side and handed in so this package stays pure.
	Burn map[period.Period]BurnRates
}

// BurnMode names a way of measuring monthly burn.
type BurnMode string

// The four burn modes.
const (
	BurnEssentialPlanned BurnMode = "essential_planned"
	BurnTrailing3        BurnMode = "actual_trailing_3"
	BurnTrailing12       BurnMode = "actual_trailing_12"
	// BurnLegacyBlend reproduces the sheet's three-way mean. It exists for the
	// parity suite, is never the default, and is labelled legacy in the UI.
	BurnLegacyBlend BurnMode = "legacy_blend"
)

// DefaultBurnMode is what the capital screen opens with.
const DefaultBurnMode = BurnTrailing3

// Valid reports whether the mode is one of the four.
func (b BurnMode) Valid() bool {
	switch b {
	case BurnEssentialPlanned, BurnTrailing3, BurnTrailing12, BurnLegacyBlend:
		return true
	}
	return false
}

// BurnRates are one period's candidate burn rates, keyed by mode. A mode absent
// from the map has no rate for that period, which makes runway nil rather than
// infinite.
type BurnRates map[BurnMode]money.Money

// Share is one slice of the allocation.
type Share struct {
	AccountID  int64
	Name       string
	AssetClass account.AssetClass
	Value      money.Money
	// Share is the fraction of net worth, in [0,1]. Every slice is a leaf
	// account converted to base first, so the shares sum to exactly 1.
	Share float64
}

// CapitalChange splits what the workbook conflated into one number.
//
// The name stutters as capital.CapitalChange, deliberately: `Change` is the
// function that computes it, and it is the name stage 06 published.
//
//nolint:revive // published contract; Change is taken by the function
type CapitalChange struct {
	// Total is general(t) - general(t-1), the sheet's row 59.
	Total money.Money
	// Real is money actually saved or spent: quantity movement priced at this
	// period's rate.
	Real money.Money
	// FX is the remainder — what the currencies did.
	FX money.Money
	// Recorded is false when either period is missing, in which case the figures
	// above are meaningless and the caller must not show them.
	Recorded bool
}

// Value is an account's worth in the reporting currency.
//
// A leaf returns its snapshot. A parent returns the sum over its children,
// recursively — the sheet enumerated children by hand in each parent's formula,
// which is exactly how `Banks FOP` fell out of `Banks`.
func Value(d Data, accountID int64, p period.Period) *money.Money {
	return valueOf(d, accountID, p, map[int64]bool{})
}

func valueOf(d Data, accountID int64, p period.Period, seen map[int64]bool) *money.Money {
	if seen[accountID] {
		// A cycle cannot happen through the account service's checks; refusing
		// to loop is cheaper than trusting that forever.
		return nil
	}
	seen[accountID] = true

	children := d.Children[accountID]
	if len(children) == 0 {
		if v, ok := d.Values[accountID][p]; ok {
			out := v
			return &out
		}
		return nil
	}

	sum := money.New(0, d.BaseCurrency)
	found := false
	for _, child := range children {
		if v := valueOf(d, child, p, seen); v != nil {
			found = true
			sum.Minor += v.Minor
		}
	}
	if !found {
		return nil
	}
	return &sum
}

// ReadyForUsage is the liquid total — row 54.
func ReadyForUsage(d Data, p period.Period) *money.Money { return sumWhere(d, p, liquid) }

// General is net worth — row 55.
func General(d Data, p period.Period) *money.Money { return sumWhere(d, p, counts) }

func liquid(a account.Account) bool { return a.IsLiquid }
func counts(a account.Account) bool { return a.CountsTowardNetWorth }

// sumWhere adds the leaf accounts matching keep. Leaves only: adding a parent as
// well as its children is how the sheet's doughnut reached 162%.
func sumWhere(d Data, p period.Period, keep func(account.Account) bool) *money.Money {
	sum := money.New(0, d.BaseCurrency)
	found := false
	for _, a := range d.Accounts {
		if len(d.Children[a.ID]) > 0 || !keep(a) {
			continue
		}
		if v, ok := d.Values[a.ID][p]; ok {
			found = true
			sum.Minor += v.Minor
		}
	}
	if !found {
		return nil
	}
	return &sum
}

// Allocation is the share of net worth held in each leaf account.
//
// Deviation D6, two bugs in one column: the sheet divided **unconverted**
// amounts by a EUR total, giving `Cash HUF` 41.4% when its true share is about
// 1.2%; and it plotted parents alongside their children, so the slices summed to
// roughly 162%. Conversion happens first, and only leaves are counted, so the
// shares sum to exactly 1.
func Allocation(d Data, p period.Period) []Share {
	total := General(d, p)
	if total == nil || total.Minor == 0 {
		return nil
	}

	out := []Share{}
	for _, a := range d.Accounts {
		if len(d.Children[a.ID]) > 0 || !a.CountsTowardNetWorth {
			continue
		}
		v, ok := d.Values[a.ID][p]
		if !ok {
			continue
		}
		f, _ := new(big.Rat).SetFrac64(v.Minor, total.Minor).Float64()
		out = append(out, Share{
			AccountID: a.ID, Name: a.Name, AssetClass: a.AssetClass, Value: v, Share: f,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value.Minor != out[j].Value.Minor {
			return out[i].Value.Minor > out[j].Value.Minor
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Runway is how many months the liquid assets would last — row 57.
//
// Deviation D7: the sheet's denominator used absolute references, so every
// historical month divided by *today's* burn rate and the whole runway history
// rewrote itself whenever an average changed. Each period uses its own rate here.
//
// nil rather than infinity when the burn rate is zero: "forever" is not a number
// of months, and rendering ∞ would be worse than rendering nothing.
func Runway(d Data, p period.Period, mode BurnMode) *float64 {
	liquidTotal := ReadyForUsage(d, p)
	if liquidTotal == nil {
		return nil
	}
	rates, ok := d.Burn[p]
	if !ok {
		return nil
	}
	burn, ok := rates[mode]
	if !ok || burn.Minor == 0 {
		return nil
	}
	f, _ := new(big.Rat).SetFrac64(liquidTotal.Minor, burn.Minor).Float64()
	return &f
}

// GeneralIn expresses net worth in another currency — rows 71 and 72, at each
// period's own rate rather than today's.
func GeneralIn(d Data, currency string, p period.Period, rate *big.Rat) *money.Money {
	total := General(d, p)
	if total == nil || rate == nil || rate.Sign() == 0 {
		return nil
	}
	// The exponent difference is applied by the caller's converter; here the
	// figure is scaled by the rate alone, which is what rows 71-72 do.
	value := new(big.Rat).Mul(new(big.Rat).SetInt64(total.Minor), rate)
	out := money.New(roundHalfUp(value), currency)
	return &out
}

// Change splits the month-on-month movement — row 59.
//
// Deviation D8: the sheet reported only the total, computed from balances all
// revalued at current FX, so a month when the hryvnia moved looked identical to
// a month of genuine saving.
func Change(d Data, p period.Period) CapitalChange {
	out := CapitalChange{
		Total: money.New(0, d.BaseCurrency),
		Real:  money.New(0, d.BaseCurrency),
		FX:    money.New(0, d.BaseCurrency),
	}
	current := General(d, p)
	if current == nil {
		return out
	}

	prev := p.Prev()
	previous := General(d, prev)
	if previous == nil {
		// Nothing to compare against, so there is no change to report. The
		// sheet's July read -26,581 precisely because it compared a real month
		// against an empty one. The month before the first recorded one is
		// loaded for exactly this purpose, so a carried-in opening balance is
		// simply a snapshot there — the workbook's D55 = 30723.
		return out
	}
	out.Recorded = true
	out.Total = money.New(current.Minor-previous.Minor, d.BaseCurrency)

	// Real change is the quantity movement, priced at this period's rate. What
	// is left over is what the currencies did.
	for _, a := range d.Accounts {
		if len(d.Children[a.ID]) > 0 || !a.CountsTowardNetWorth {
			continue
		}
		now, hasNow := d.Native[a.ID][p]
		before, hasBefore := d.Native[a.ID][prev]
		if !hasNow && !hasBefore {
			continue
		}
		// An account that appears or disappears moves by its whole balance: that
		// is a real movement of money, not a currency effect.
		delta := int64(0)
		if hasNow {
			delta += now.Minor
		}
		if hasBefore {
			delta -= before.Minor
		}
		if delta == 0 {
			continue
		}
		rate := d.Rates[a.ID][p]
		if rate == nil || rate.Sign() == 0 {
			continue
		}
		// delta is in the account's currency; divide by units-per-base to reach
		// the base currency.
		value := new(big.Rat).Quo(new(big.Rat).SetInt64(delta), rate)
		out.Real.Minor += roundHalfUp(value)
	}
	out.FX = money.New(out.Total.Minor-out.Real.Minor, d.BaseCurrency)
	return out
}

// Drift is the difference between a recorded snapshot and what the transactions
// imply. The snapshot always wins; this is informational
// (docs/adr/0003-snapshot-reconcile-balances.md).
type Drift struct {
	AccountID int64
	Name      string
	Snapshot  *money.Money
	Implied   *money.Money
	// Difference is snapshot - implied, in the account's own currency. nil when
	// either side is missing, because a difference from nothing is not a number.
	Difference *money.Money
}

// Reconcile compares snapshots with transaction-implied balances.
func Reconcile(d Data, implied map[int64]money.Money, p period.Period) []Drift {
	out := []Drift{}
	for _, a := range d.Accounts {
		if len(d.Children[a.ID]) > 0 {
			continue
		}
		snap, hasSnap := d.Native[a.ID][p]
		impl, hasImpl := implied[a.ID]
		if !hasSnap && !hasImpl {
			continue
		}
		row := Drift{AccountID: a.ID, Name: a.Name}
		if hasSnap {
			v := snap
			row.Snapshot = &v
		}
		if hasImpl {
			v := impl
			row.Implied = &v
		}
		if hasSnap && hasImpl {
			diff := money.New(snap.Minor-impl.Minor, snap.Currency)
			row.Difference = &diff
		}
		out = append(out, row)
	}
	return out
}

// roundHalfUp rounds a rational to the nearest integer, halves away from zero.
func roundHalfUp(v *big.Rat) int64 {
	num, den := v.Num(), v.Denom()
	neg := v.Sign() < 0
	abs := new(big.Int).Abs(num)

	quo, rem := new(big.Int).QuoRem(abs, den, new(big.Int))
	rem.Mul(rem, big.NewInt(2))
	if rem.Cmp(den) >= 0 {
		quo.Add(quo, big.NewInt(1))
	}
	if neg {
		quo.Neg(quo)
	}
	return quo.Int64()
}
