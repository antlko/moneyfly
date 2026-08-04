package fx

import (
	"context"
	"math/big"
	"testing"
	"time"

	"moneyfly/internal/money"
)

// memRepo is an in-memory fx.Repo. The storage-level behaviour (nearest-earlier
// lookup, idempotent upsert) is covered against real SQL in
// internal/db/fx_test.go; here the point is the arithmetic on top.
type memRepo struct{ rates []Rate }

func (m *memRepo) RateOn(_ context.Context, base, quote string, on time.Time) (*Rate, error) {
	var best *Rate
	for i := range m.rates {
		r := m.rates[i]
		if r.Base != base || r.Quote != quote || r.AsOf.After(on) {
			continue
		}
		if best == nil || r.AsOf.After(best.AsOf) {
			found := r
			best = &found
		}
	}
	return best, nil
}

func (m *memRepo) History(context.Context, string, string, time.Time, time.Time) ([]Rate, error) {
	return m.rates, nil
}
func (m *memRepo) Upsert(_ context.Context, r Rate) (Rate, error) {
	m.rates = append(m.rates, r)
	return r, nil
}
func (m *memRepo) Latest(context.Context, string) ([]Rate, error) { return m.rates, nil }

func on(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(DateLayout, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return parsed.UTC()
}

func ratOf(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("%q is not a rational", s)
	}
	return r
}

func serviceWith(t *testing.T, rates ...Rate) *Service {
	t.Helper()
	return NewService(&memRepo{rates: rates})
}

func eurTo(t *testing.T, quote, rate, day string) Rate {
	t.Helper()
	return Rate{AsOf: on(t, day), Base: StorageBase, Quote: quote, Rate: ratOf(t, rate), Source: "test"}
}

func TestService_SameCurrencyIsIdentity(t *testing.T) {
	s := serviceWith(t)
	r, err := s.RateOn(context.Background(), "HUF", "HUF", on(t, "2026-08-01"))
	if err != nil || r == nil {
		t.Fatalf("RateOn: %v, %v", r, err)
	}
	if r.Rate.Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("rate = %s, want 1", FormatRate(r.Rate))
	}
}

// Only EUR->X is stored. The inverse is computed, which is what makes it
// impossible to hold EUR->USD 1.14 and USD->EUR 0.88 at once and disagree with
// yourself.
func TestService_InverseIsComputedNotStored(t *testing.T) {
	s := serviceWith(t, eurTo(t, "USD", "1.25", "2026-08-01"))

	r, err := s.RateOn(context.Background(), "USD", StorageBase, on(t, "2026-08-01"))
	if err != nil || r == nil {
		t.Fatalf("RateOn: %v, %v", r, err)
	}
	if r.Rate.Cmp(ratOf(t, "0.8")) != 0 {
		t.Errorf("USD->EUR = %s, want exactly 0.8", FormatRate(r.Rate))
	}
}

// USD->HUF = (EUR->HUF) / (EUR->USD).
func TestService_CrossRateGoesThroughTheStorageBase(t *testing.T) {
	s := serviceWith(t,
		eurTo(t, "USD", "1.25", "2026-08-01"),
		eurTo(t, "HUF", "400", "2026-07-28"),
	)

	r, err := s.RateOn(context.Background(), "USD", "HUF", on(t, "2026-08-01"))
	if err != nil || r == nil {
		t.Fatalf("RateOn: %v, %v", r, err)
	}
	if r.Rate.Cmp(ratOf(t, "320")) != 0 {
		t.Errorf("USD->HUF = %s, want 320", FormatRate(r.Rate))
	}
	// A pair is only as fresh as its stalest leg; claiming otherwise would hide
	// a provider that stopped publishing one of the two.
	if !r.AsOf.Equal(on(t, "2026-07-28")) {
		t.Errorf("asOf = %s, want the older leg 2026-07-28", r.AsOf.Format(DateLayout))
	}
}

func TestService_MissingRateIsNotAnError(t *testing.T) {
	s := serviceWith(t)
	r, err := s.RateOn(context.Background(), "USD", "HUF", on(t, "2026-08-01"))
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if r != nil {
		t.Fatalf("RateOn = %v, want nil", r)
	}

	// Convert says the same thing: no rate, no amount, no error. Refusing the
	// write would be worse than not knowing the euro value yet.
	out, id, err := s.Convert(context.Background(), money.New(1000, "USD"), "HUF", on(t, "2026-08-01"))
	if err != nil || id != nil || out.Minor != 0 {
		t.Fatalf("Convert = %v, %v, %v; want zero, nil, nil", out, id, err)
	}
}

// The exponent shift is the part that is easy to get wrong, and HUF is the case
// that catches it: 0 decimals against EUR's 2.
func TestConvertMinor_ExponentShift(t *testing.T) {
	cases := []struct {
		name             string
		minor            int64
		rate             string
		expFrom, expTo   int
		want             int64
		whyItMattersHere string
	}{
		{
			name: "EUR to HUF loses two places",
			// 14.40 EUR at 400 HUF/EUR is 5760 forint, stored as 5760 because
			// HUF has no minor unit.
			minor: 1440, rate: "400", expFrom: 2, expTo: 0, want: 5760,
		},
		{
			name: "HUF to EUR gains two places",
			// 2858 forint at 1/400 is 7.145 EUR -> 714 cents, rounded once.
			minor: 2858, rate: "0.0025", expFrom: 0, expTo: 2, want: 715,
		},
		{
			name:  "same exponent is a plain multiply",
			minor: 10000, rate: "1.138108", expFrom: 2, expTo: 2, want: 11381,
		},
		{
			name:  "rounds half away from zero",
			minor: 1, rate: "0.5", expFrom: 2, expTo: 2, want: 1,
		},
		{
			name:  "and does so symmetrically for negatives",
			minor: -1, rate: "0.5", expFrom: 2, expTo: 2, want: -1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ConvertMinor(c.minor, ratOf(t, c.rate), c.expFrom, c.expTo)
			if got != c.want {
				t.Errorf("ConvertMinor(%d, %s, %d, %d) = %d, want %d",
					c.minor, c.rate, c.expFrom, c.expTo, got, c.want)
			}
		})
	}
}

// This table is mirrored verbatim in web-ui/src/lib/fx.test.ts. The two
// implementations must agree exactly, for the same reason the LWW rule is
// duplicated: the client converts offline and the server converts for export,
// and a half-cent of disagreement between them is a bug report nobody can
// reproduce.
func TestConvertMinor_MatchesTheClientPort(t *testing.T) {
	cases := []struct {
		minor          int64
		rate           string
		expFrom, expTo int
		want           int64
	}{
		{minor: 32000, rate: "0.019531", expFrom: 0, expTo: 2, want: 62499},
		{minor: 1000, rate: "1.138108", expFrom: 2, expTo: 2, want: 1138},
		{minor: -5000, rate: "1.138108", expFrom: 2, expTo: 2, want: -5691},
		{minor: 743, rate: "360.409427", expFrom: 2, expTo: 0, want: 2678},
		{minor: 0, rate: "1.5", expFrom: 2, expTo: 2, want: 0},
	}
	for _, c := range cases {
		got := ConvertMinor(c.minor, ratOf(t, c.rate), c.expFrom, c.expTo)
		if got != c.want {
			t.Errorf("ConvertMinor(%d, %s, %d, %d) = %d, want %d",
				c.minor, c.rate, c.expFrom, c.expTo, got, c.want)
		}
	}
}

func TestService_UpsertRefusesANonEuroBase(t *testing.T) {
	s := serviceWith(t)
	_, err := s.Upsert(context.Background(), Rate{
		AsOf: on(t, "2026-08-01"), Base: "USD", Quote: "HUF", Rate: big.NewRat(1, 1),
	})
	if err == nil {
		t.Fatal("storing a USD-based rate must be refused; the inverse is computed")
	}
}

func TestFormatRate_TrimsWithoutLosingPrecision(t *testing.T) {
	cases := map[string]string{
		"1.138108": "1.138108",
		"1.20":     "1.2",
		"400":      "400",
		"0.0025":   "0.0025",
	}
	for in, want := range cases {
		if got := FormatRate(ratOf(t, in)); got != want {
			t.Errorf("FormatRate(%s) = %s, want %s", in, got, want)
		}
	}
}
