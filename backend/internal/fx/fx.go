// Package fx holds dated exchange rates and conversion.
//
// One direction only: EUR->X is stored and the inverse is computed, which
// structurally eliminates the workbook's 1.14 / 0.88 inconsistency. Lookup is
// exact date, else nearest *earlier* date, never later — a past figure must not
// change because of a future rate (docs/ARCHITECTURE.md).
package fx

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"moneyfly/internal/money"
)

// StorageBase is the only base currency rates are stored against.
const StorageBase = "EUR"

// DateLayout is the storage format of as_of_date.
const DateLayout = "2006-01-02"

// Rate is one dated rate from one source.
type Rate struct {
	ID        int64
	AsOf      time.Time
	Base      string
	Quote     string
	Rate      *big.Rat
	Source    string
	FetchedAt time.Time
}

// Repo is the storage contract.
type Repo interface {
	// RateOn returns the rate for the exact date, else the nearest earlier one,
	// else (nil, nil). Provider priority breaks ties within a date.
	RateOn(ctx context.Context, base, quote string, on time.Time) (*Rate, error)
	// History returns rates in [from, to] ascending.
	History(ctx context.Context, base, quote string, from, to time.Time) ([]Rate, error)
	// Upsert stores one rate; re-running a fetch for the same day is a no-op.
	Upsert(ctx context.Context, r Rate) (Rate, error)
	// Latest returns the most recent rate per quote currency.
	Latest(ctx context.Context, base string) ([]Rate, error)
}

// Service is the FX use-case layer.
type Service struct {
	repo Repo
}

// NewService builds the service.
func NewService(repo Repo) *Service {
	return &Service{repo: repo}
}

// RateOn returns the applicable rate, or (nil, nil) when none exists.
//
// A same-currency request returns a synthetic rate of 1 with no id: it is a fact,
// not a stored observation.
func (s *Service) RateOn(ctx context.Context, base, quote string, on time.Time) (*Rate, error) {
	base, quote = up(base), up(quote)
	if base == quote {
		return &Rate{AsOf: on, Base: base, Quote: quote, Rate: big.NewRat(1, 1), Source: "identity"}, nil
	}
	if base == StorageBase {
		return s.repo.RateOn(ctx, base, quote, on)
	}
	if quote == StorageBase {
		// The inverse is computed, never stored.
		direct, err := s.repo.RateOn(ctx, StorageBase, base, on)
		if err != nil || direct == nil {
			return nil, err
		}
		if direct.Rate.Sign() == 0 {
			return nil, fmt.Errorf("fx: stored rate %s->%s on %s is zero", StorageBase, base, direct.AsOf.Format(DateLayout))
		}
		inv := new(big.Rat).Inv(direct.Rate)
		return &Rate{
			ID: direct.ID, AsOf: direct.AsOf, Base: base, Quote: quote,
			Rate: inv, Source: direct.Source, FetchedAt: direct.FetchedAt,
		}, nil
	}
	// Cross rate through the storage base: USD->HUF = (EUR->HUF) / (EUR->USD).
	toQuote, err := s.repo.RateOn(ctx, StorageBase, quote, on)
	if err != nil || toQuote == nil {
		return nil, err
	}
	toBase, err := s.repo.RateOn(ctx, StorageBase, base, on)
	if err != nil || toBase == nil {
		return nil, err
	}
	if toBase.Rate.Sign() == 0 {
		return nil, fmt.Errorf("fx: stored rate %s->%s is zero", StorageBase, base)
	}
	cross := new(big.Rat).Quo(toQuote.Rate, toBase.Rate)
	asOf := toQuote.AsOf
	if toBase.AsOf.Before(asOf) {
		// The pair is only as fresh as its stalest leg.
		asOf = toBase.AsOf
	}
	return &Rate{
		// The recorded id is the quote-side rate: it is the one that prices the
		// result. The base-side leg is reproducible from the same date.
		ID: toQuote.ID, AsOf: asOf, Base: base, Quote: quote,
		Rate: cross, Source: toQuote.Source + "+" + toBase.Source, FetchedAt: toQuote.FetchedAt,
	}, nil
}

// Convert returns the converted amount and the id of the rate used, for audit.
//
// A missing rate is (zero, nil, nil): the caller stores a NULL base amount rather
// than failing the write. Losing the transaction would be worse than not knowing
// its euro value yet.
func (s *Service) Convert(ctx context.Context, m money.Money, to string, on time.Time) (money.Money, *int64, error) {
	to = up(to)
	from := up(m.Currency)
	if from == to {
		return money.New(m.Minor, to), nil, nil
	}
	rate, err := s.RateOn(ctx, from, to, on)
	if err != nil {
		return money.Money{}, nil, err
	}
	if rate == nil {
		return money.Money{}, nil, nil
	}
	out := ConvertMinor(m.Minor, rate.Rate, money.Exponent(from), money.Exponent(to))
	var id *int64
	if rate.ID != 0 {
		rateID := rate.ID
		id = &rateID
	}
	return money.New(out, to), id, nil
}

// ConvertMinor applies a rate to minor units, rounding half-up exactly once at
// the target currency's exponent.
//
//	minor_to = minor_from x rate x 10^(expTo - expFrom)
func ConvertMinor(minor int64, rate *big.Rat, expFrom, expTo int) int64 {
	value := new(big.Rat).SetInt64(minor)
	value.Mul(value, rate)
	if d := expTo - expFrom; d > 0 {
		value.Mul(value, new(big.Rat).SetInt(pow10(d)))
	} else if d < 0 {
		value.Quo(value, new(big.Rat).SetInt(pow10(-d)))
	}
	return roundHalfUp(value)
}

// Upsert stores a rate. Only EUR-based rates are accepted, because the inverse is
// computed rather than stored.
func (s *Service) Upsert(ctx context.Context, r Rate) (Rate, error) {
	if up(r.Base) != StorageBase {
		return Rate{}, fmt.Errorf(
			"fx: only %s-based rates are stored; the inverse is computed, got base %q",
			StorageBase, r.Base)
	}
	if r.Rate == nil || r.Rate.Sign() <= 0 {
		return Rate{}, fmt.Errorf("fx: rate must be positive")
	}
	r.Base, r.Quote = up(r.Base), up(r.Quote)
	return s.repo.Upsert(ctx, r)
}

// History returns stored rates for a pair.
func (s *Service) History(ctx context.Context, base, quote string, from, to time.Time) ([]Rate, error) {
	return s.repo.History(ctx, up(base), up(quote), from, to)
}

// Latest returns the most recent rate for every quote currency.
func (s *Service) Latest(ctx context.Context) ([]Rate, error) {
	return s.repo.Latest(ctx, StorageBase)
}

// ParseRate reads a decimal string into an exact rational. Rates are stored as
// text precisely so no binary-float drift is introduced here.
func ParseRate(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("fx: %q is not a decimal number", s)
	}
	return r, nil
}

// FormatRate renders a rate for storage with enough places to round-trip the
// providers' precision.
func FormatRate(r *big.Rat) string {
	if r == nil {
		return ""
	}
	if r.IsInt() {
		return r.Num().String()
	}
	return trimZeros(r.FloatString(12))
}

func trimZeros(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == '0' {
		i--
	}
	if i > 0 && s[i-1] == '.' {
		i--
	}
	return s[:i]
}

func roundHalfUp(v *big.Rat) int64 {
	num, den := v.Num(), v.Denom()
	neg := v.Sign() < 0
	absNum := new(big.Int).Abs(num)

	quo, rem := new(big.Int).QuoRem(absNum, den, new(big.Int))
	// Round half away from zero: 2*rem >= den promotes.
	rem.Mul(rem, big.NewInt(2))
	if rem.Cmp(den) >= 0 {
		quo.Add(quo, big.NewInt(1))
	}
	if neg {
		quo.Neg(quo)
	}
	return quo.Int64()
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func up(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		out = append(out, c)
	}
	return string(out)
}
