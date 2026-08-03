package fx

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/antlko/moneyapp/internal/platform/money"
)

// Provider is the slice of internal/provider this package needs. Declaring it
// here rather than importing keeps the dependency pointing the right way: the
// domain says what it needs, the outer layer supplies it.
type Provider interface {
	Key() string
	Fetch(ctx context.Context, base string) (map[string]*big.Rat, time.Time, error)
}

// DefaultPlausibilityMaxChange is the largest single-day move accepted without
// question. A silently wrong rate corrupts every derived figure while looking
// like a real event, so this is a correctness control rather than politeness.
const DefaultPlausibilityMaxChange = 0.15

// RefreshResult is what one refresh did.
type RefreshResult struct {
	// Provider is the one whose values were stored, empty when none succeeded.
	Provider string
	AsOf     time.Time
	// Stored counts the rates written.
	Stored int
	// Rejected lists the quotes whose move failed the plausibility check. Their
	// previous value is kept.
	Rejected []Implausible
	// Errors records what each provider said, in the order they were tried. It
	// is kept even on success, so a degraded primary is visible.
	Errors []string
}

// Implausible is one rejected move.
type Implausible struct {
	Quote    string
	Previous string
	Proposed string
	// Change is the fractional move, e.g. 9.0 for a tenfold jump.
	Change float64
}

// Refresher pulls rates from a chain of providers into the dated history.
//
// The chain is primary, then fallback, then nothing: a total outage keeps the
// last stored value and records the error. No provider failure is ever fatal and
// none of this happens in a request (docs/adr/0009-dated-fx-provider-chain.md).
type Refresher struct {
	service   *Service
	providers []Provider
	maxChange float64
	quotes    []string
}

// NewRefresher builds the refresher. Providers are tried in the order given.
func NewRefresher(service *Service, providers []Provider, maxChange float64, quotes []string) *Refresher {
	if maxChange <= 0 {
		maxChange = DefaultPlausibilityMaxChange
	}
	return &Refresher{service: service, providers: providers, maxChange: maxChange, quotes: quotes}
}

// Refresh fetches once and stores what it gets.
//
// `on` is the date the rates are recorded against; the provider's own date is
// preferred when it supplies one, because a rate published yesterday is
// yesterday's rate.
func (r *Refresher) Refresh(ctx context.Context, on time.Time) (RefreshResult, error) {
	out := RefreshResult{}
	for _, p := range r.providers {
		rates, asOf, err := p.Fetch(ctx, StorageBase)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s: %v", p.Key(), err))
			continue
		}
		if asOf.IsZero() {
			asOf = on
		}
		asOf = asOf.UTC().Truncate(24 * time.Hour)

		stored, rejected, err := r.store(ctx, p.Key(), rates, asOf)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s: %v", p.Key(), err))
			continue
		}
		out.Provider, out.AsOf, out.Stored, out.Rejected = p.Key(), asOf, stored, rejected
		return out, nil
	}
	// Every provider failed. The last stored value stands; the caller badges it
	// stale and records the error. Nothing is deleted and no screen breaks.
	return out, nil
}

func (r *Refresher) store(
	ctx context.Context, source string, rates map[string]*big.Rat, asOf time.Time,
) (stored int, rejected []Implausible, err error) {
	wanted := r.quotes
	if len(wanted) == 0 {
		wanted = make([]string, 0, len(rates))
		for code := range rates {
			wanted = append(wanted, code)
		}
		sort.Strings(wanted)
	}

	for _, quote := range wanted {
		quote = up(quote)
		if quote == StorageBase {
			continue
		}
		rate, ok := rates[quote]
		if !ok {
			continue
		}
		// Compare against the last rate we hold for this pair, whatever its date:
		// a weekend gap is normal and must not look like a jump.
		previous, perr := r.service.RateOn(ctx, StorageBase, quote, asOf)
		if perr != nil {
			return stored, rejected, perr
		}
		if previous != nil && previous.Rate != nil && previous.Rate.Sign() > 0 {
			if change, ok := implausible(previous.Rate, rate, r.maxChange); ok {
				rejected = append(rejected, Implausible{
					Quote: quote, Previous: FormatRate(previous.Rate),
					Proposed: FormatRate(rate), Change: change,
				})
				continue
			}
		}
		if _, err := r.service.Upsert(ctx, Rate{
			AsOf: asOf, Base: StorageBase, Quote: quote, Rate: rate,
			Source: source, FetchedAt: asOf,
		}); err != nil {
			return stored, rejected, err
		}
		stored++
	}
	return stored, rejected, nil
}

// implausible reports whether the move from previous to proposed exceeds the
// threshold, and by how much.
func implausible(previous, proposed *big.Rat, maxChange float64) (float64, bool) {
	delta := new(big.Rat).Sub(proposed, previous)
	delta.Abs(delta)
	ratio := new(big.Rat).Quo(delta, previous)
	change, _ := ratio.Float64()
	return change, change > maxChange
}

// ConvertAt applies an explicit rate, for callers that already hold one.
func ConvertAt(m money.Money, rate *big.Rat, to string, expFrom, expTo int) money.Money {
	return money.New(ConvertMinor(m.Minor, rate, expFrom, expTo), to)
}
