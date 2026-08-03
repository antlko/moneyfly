package capital

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// Valuation says which rate a historical balance is converted at.
type Valuation string

// The two valuation modes.
const (
	// Contemporaneous values each period at its own rate. It is the default and
	// the honest one: a figure from January is what it was worth in January.
	Contemporaneous Valuation = "contemporaneous"
	// Constant values every period at the latest rate, which is what the workbook
	// did — every balance revalued whenever a rate cell changed. It is offered
	// because comparing the two is the clearest way to see how much of a change
	// was currency rather than saving (deviation D8).
	Constant Valuation = "constant"
)

// Valid reports whether the valuation is one of the two.
func (v Valuation) Valid() bool { return v == Contemporaneous || v == Constant }

// Input is a snapshot as the API receives it: a value or a quantity, never both.
type Input struct {
	Amount       *money.Money
	QuantityNano *int64
	Note         string
}

// Repo is the storage contract.
type Repo interface {
	ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]Snapshot, error)
	// ListRange returns every snapshot in [from, to], keyed by account and period.
	ListRange(ctx context.Context, userID int64, from, to period.Period) (map[int64]map[period.Period]Snapshot, error)
	Upsert(ctx context.Context, userID int64, s Snapshot, now time.Time) (Snapshot, error)
	UpsertMany(ctx context.Context, userID int64, snapshots []Snapshot, now time.Time) (int, error)
	Delete(ctx context.Context, userID, accountID int64, p period.Period) error
	// ImpliedBalances returns the balance each account's transactions imply as at
	// the end of the period, in the account's own currency.
	ImpliedBalances(ctx context.Context, userID int64, p period.Period) (map[int64]money.Money, error)
}

// AccountSource supplies the chart of accounts.
type AccountSource interface {
	List(ctx context.Context, userID int64, includeArchived bool) ([]account.Account, error)
	AssertPostable(ctx context.Context, userID, id int64) (account.Account, error)
}

// FXSource prices a snapshot at its own period's rate.
type FXSource interface {
	RateOn(ctx context.Context, base, quote string, on time.Time) (*fx.Rate, error)
	Convert(ctx context.Context, m money.Money, to string, on time.Time) (money.Money, *int64, error)
}

// Service is the capital use-case layer.
type Service struct {
	repo       Repo
	accounts   AccountSource
	fx         FXSource
	currencies *currency.Service
	clock      clock.Clock
}

// NewService builds the capital service.
func NewService(repo Repo, accounts AccountSource, fxSvc FXSource, currencies *currency.Service, clk clock.Clock) *Service {
	return &Service{repo: repo, accounts: accounts, fx: fxSvc, currencies: currencies, clock: clk}
}

// ListByPeriod returns one month's snapshots.
func (s *Service) ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]Snapshot, error) {
	if !p.Valid() {
		return nil, apperr.Validation("period", "must be a month in YYYY-MM form, got %q", p)
	}
	return s.repo.ListByPeriod(ctx, userID, p)
}

// Upsert records one account's balance for one month.
func (s *Service) Upsert(ctx context.Context, userID, accountID int64, p period.Period, baseCurrency string, in Input) (Snapshot, error) {
	if !p.Valid() {
		return Snapshot{}, apperr.Validation("period", "must be a month in YYYY-MM form, got %q", p)
	}
	// AssertPostable is what refuses a write to a computed parent: its value is
	// the sum of its children and cannot be asserted directly.
	acct, err := s.accounts.AssertPostable(ctx, userID, accountID)
	if err != nil {
		return Snapshot{}, err
	}
	if (in.Amount == nil) == (in.QuantityNano == nil) {
		return Snapshot{}, apperr.Validation("amount",
			"record either an amount or a quantity, never both and never neither")
	}

	snap := Snapshot{AccountID: accountID, Period: p, QuantityNano: in.QuantityNano, Note: in.Note}
	if in.Amount != nil {
		if !strings.EqualFold(in.Amount.Currency, acct.Currency) {
			return Snapshot{}, apperr.Validation("amount.currency",
				"must be the account's currency %s, got %s", acct.Currency, in.Amount.Currency)
		}
		amount := *in.Amount
		snap.Amount = &amount

		// The base value is stored as the audit trail of what was true when the
		// figure was entered. Reports reconvert at read time, so a corrected rate
		// is never stranded in an old row.
		_, last := p.Range()
		converted, rateID, err := s.fx.Convert(ctx, amount, baseCurrency, last)
		if err != nil {
			return Snapshot{}, err
		}
		if rateID != nil || strings.EqualFold(amount.Currency, baseCurrency) {
			snap.BaseAmount = &converted
			snap.FxRateID = rateID
		}
	}
	return s.repo.Upsert(ctx, userID, snap, s.clock.Now())
}

// Bulk records a whole month in one transaction — the confirm-or-adjust screen
// that replaces roughly thirty workbook cells.
func (s *Service) Bulk(ctx context.Context, userID int64, p period.Period, baseCurrency string, items map[int64]Input) (int, error) {
	if !p.Valid() {
		return 0, apperr.Validation("period", "must be a month in YYYY-MM form, got %q", p)
	}
	snapshots := make([]Snapshot, 0, len(items))
	for accountID, in := range items {
		acct, err := s.accounts.AssertPostable(ctx, userID, accountID)
		if err != nil {
			return 0, err
		}
		if (in.Amount == nil) == (in.QuantityNano == nil) {
			return 0, apperr.Validation("items",
				"account %d: record either an amount or a quantity, never both and never neither", accountID)
		}
		snap := Snapshot{AccountID: accountID, Period: p, QuantityNano: in.QuantityNano, Note: in.Note}
		if in.Amount != nil {
			if !strings.EqualFold(in.Amount.Currency, acct.Currency) {
				return 0, apperr.Validation("items",
					"account %q holds %s, got %s", acct.Name, acct.Currency, in.Amount.Currency)
			}
			amount := *in.Amount
			snap.Amount = &amount
			_, last := p.Range()
			converted, rateID, err := s.fx.Convert(ctx, amount, baseCurrency, last)
			if err != nil {
				return 0, err
			}
			if rateID != nil || strings.EqualFold(amount.Currency, baseCurrency) {
				snap.BaseAmount = &converted
				snap.FxRateID = rateID
			}
		}
		snapshots = append(snapshots, snap)
	}
	return s.repo.UpsertMany(ctx, userID, snapshots, s.clock.Now())
}

// Delete removes one snapshot. A missing month is a gap, never an interpolation.
func (s *Service) Delete(ctx context.Context, userID, accountID int64, p period.Period) error {
	return s.repo.Delete(ctx, userID, accountID, p)
}

// Load builds the snapshot every capital metric is computed over.
//
// The month before `from` is loaded as well but kept out of Periods: it is what
// the first period's change compares against, and it is where a carried-in
// opening balance lives.
func (s *Service) Load(
	ctx context.Context, userID int64, from, to period.Period, baseCurrency string,
	md metrics.Data, valuation Valuation,
) (Data, error) {
	if valuation == "" {
		valuation = Contemporaneous
	}
	if !valuation.Valid() {
		return Data{}, apperr.Validation("valuation",
			"must be contemporaneous or constant, got %q", valuation)
	}
	periods, err := from.Until(to)
	if err != nil {
		return Data{}, err
	}
	if len(periods) > metrics.MaxPeriods {
		return Data{}, apperr.Validation("to",
			"a report may span at most %d months, got %d", metrics.MaxPeriods, len(periods))
	}

	accounts, err := s.accounts.List(ctx, userID, true)
	if err != nil {
		return Data{}, err
	}
	stored, err := s.repo.ListRange(ctx, userID, from.Prev(), to)
	if err != nil {
		return Data{}, err
	}

	data := Data{
		BaseCurrency: baseCurrency,
		Periods:      periods,
		Recorded:     make(map[period.Period]bool, len(periods)),
		Accounts:     accounts,
		Children:     map[int64][]int64{},
		Values:       map[int64]map[period.Period]money.Money{},
		Native:       map[int64]map[period.Period]money.Money{},
		Rates:        map[int64]map[period.Period]*big.Rat{},
		Burn:         map[period.Period]BurnRates{},
	}
	byID := make(map[int64]account.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
		if a.ParentID != nil {
			data.Children[*a.ParentID] = append(data.Children[*a.ParentID], a.ID)
		}
	}

	for accountID, byPeriod := range stored {
		acct, ok := byID[accountID]
		if !ok {
			continue
		}
		for p, snap := range byPeriod {
			// Constant valuation prices every month at the latest rate, which is
			// exactly what the sheet did by keeping one undated rate cell.
			pricedAt := p
			if valuation == Constant {
				pricedAt = to
			}
			native, base, rate, err := s.value(ctx, acct, pricedAt, snap, baseCurrency)
			if err != nil {
				return Data{}, err
			}
			if native == nil || base == nil {
				continue
			}
			if data.Native[accountID] == nil {
				data.Native[accountID] = map[period.Period]money.Money{}
				data.Values[accountID] = map[period.Period]money.Money{}
				data.Rates[accountID] = map[period.Period]*big.Rat{}
			}
			data.Native[accountID][p] = *native
			data.Values[accountID][p] = *base
			data.Rates[accountID][p] = rate
			data.Recorded[p] = true
		}
	}

	for _, p := range periods {
		data.Burn[p] = BurnRatesFor(md, p)
	}
	return data, nil
}

// value converts one snapshot to the reporting currency at its own period's rate.
//
// A quantity-only snapshot has no price source until stage 07, so it is skipped
// rather than valued at a guess: an unpriced holding is unknown, not zero.
func (s *Service) value(
	ctx context.Context, acct account.Account, pricedAt period.Period,
	snap Snapshot, baseCurrency string,
) (native, base *money.Money, rate *big.Rat, err error) {
	if snap.Amount == nil {
		return nil, nil, nil, nil
	}
	_, last := pricedAt.Range()

	converted, _, err := s.fx.Convert(ctx, *snap.Amount, baseCurrency, last)
	if err != nil {
		return nil, nil, nil, err
	}
	if strings.EqualFold(snap.Amount.Currency, baseCurrency) {
		one := big.NewRat(1, 1)
		amount := *snap.Amount
		return &amount, &converted, one, nil
	}

	// Rates[account][period] is units of the account's currency per one unit of
	// the base currency, which is what prices a quantity change in Change().
	r, err := s.fx.RateOn(ctx, baseCurrency, acct.Currency, last)
	if err != nil {
		return nil, nil, nil, err
	}
	if r == nil {
		// No rate for this date: the value is unknown, and unknown is not zero.
		return nil, nil, nil, nil
	}
	amount := *snap.Amount
	return &amount, &converted, r.Rate, nil
}

// Reconciliation compares a month's snapshots with what its transactions imply.
func (s *Service) Reconciliation(ctx context.Context, userID int64, p period.Period, baseCurrency string) ([]Drift, error) {
	if !p.Valid() {
		return nil, apperr.Validation("period", "must be a month in YYYY-MM form, got %q", p)
	}
	implied, err := s.repo.ImpliedBalances(ctx, userID, p)
	if err != nil {
		return nil, err
	}
	data, err := s.Load(ctx, userID, p, p, baseCurrency, metrics.Data{}, Contemporaneous)
	if err != nil {
		return nil, err
	}
	return Reconcile(data, implied, p), nil
}

// BurnRatesFor computes the four candidate burn rates for one period from the
// spending side.
//
// Deviation D7 lives here: every figure is taken as at `p`, so a later month's
// spending can never change an earlier month's runway. The sheet's absolute
// references rewrote the entire history whenever an average moved.
func BurnRatesFor(md metrics.Data, p period.Period) BurnRates {
	out := BurnRates{}
	if md.BaseCurrency == "" {
		return out
	}
	essential := metrics.PossibleMinimum(md, p)
	out[BurnEssentialPlanned] = essential

	if trailing := trailingMean(md, p, 3); trailing != nil {
		out[BurnTrailing3] = *trailing
	}
	if trailing := trailingMean(md, p, 12); trailing != nil {
		out[BurnTrailing12] = *trailing
	}

	// The sheet's three-way mean: average essential plan, average total spend,
	// and the essential plan itself. No rationale was ever stated for it; it is
	// reproduced only so the parity suite can compare against row 57.
	avgSpend := metrics.SpendTotalAverage(md)
	avgEssential := possibleMinimumAverage(md)
	if avgSpend != nil && avgEssential != nil {
		blend := new(big.Rat).Add(avgSpend, avgEssential)
		blend.Add(blend, new(big.Rat).SetFrac64(essential.Minor, 100))
		blend.Quo(blend, big.NewRat(3, 1))
		// Back to minor units.
		blend.Mul(blend, big.NewRat(100, 1))
		out[BurnLegacyBlend] = money.New(roundHalfUp(blend), md.BaseCurrency)
	}
	return out
}

// trailingMean is the mean spend over the n recorded periods up to and including
// p, or nil when none of them recorded anything.
func trailingMean(md metrics.Data, p period.Period, n int) *money.Money {
	sum := int64(0)
	count := int64(0)
	for i := len(md.Periods) - 1; i >= 0 && count < int64(n); i-- {
		cur := md.Periods[i]
		if cur > p {
			continue
		}
		total := metrics.SpendTotal(md, cur)
		if total == nil {
			continue
		}
		sum += total.Minor
		count++
	}
	if count == 0 {
		return nil
	}
	out := money.New(divRound(sum, count), md.BaseCurrency)
	return &out
}

// possibleMinimumAverage is the mean of Possible Minimum over recorded periods —
// what the sheet's `C28` should have held instead of a stale literal (D3).
func possibleMinimumAverage(md metrics.Data) *big.Rat {
	sum := int64(0)
	count := int64(0)
	for _, p := range md.Periods {
		if !md.Recorded[p] {
			continue
		}
		count++
		sum += metrics.PossibleMinimum(md, p).Minor
	}
	if count == 0 {
		return nil
	}
	return new(big.Rat).SetFrac64(sum, count*100)
}

func divRound(sum, count int64) int64 {
	if count == 0 {
		return 0
	}
	q := sum / count
	if 2*(sum%count) >= count {
		q++
	}
	return q
}

// GeneralInCurrency expresses net worth in another currency at the period's rate.
func (s *Service) GeneralInCurrency(
	ctx context.Context, d Data, code string, p period.Period,
) (*money.Money, error) {
	total := General(d, p)
	if total == nil {
		return nil, nil
	}
	_, last := p.Range()
	converted, _, err := s.fx.Convert(ctx, *total, code, last)
	if err != nil {
		return nil, fmt.Errorf("capital: converting net worth to %s: %w", code, err)
	}
	if strings.EqualFold(code, d.BaseCurrency) {
		out := *total
		return &out, nil
	}
	rate, err := s.fx.RateOn(ctx, d.BaseCurrency, code, last)
	if err != nil || rate == nil {
		return nil, err
	}
	return &converted, nil
}
