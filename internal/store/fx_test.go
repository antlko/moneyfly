package store

import (
	"math/big"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/platform/money"
)

func TestRateOn_ExactAndSeeded(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	// Migration 0004 seeds the workbook's rates, dated so that nearest-earlier
	// lookup covers the whole Monefy history (earliest row 19.07.2021).
	got, err := svc.RateOn(h.ctx, "EUR", "HUF", mustDate("2021-07-01"))
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if got == nil {
		t.Fatal("the seeded rate must be found on its own date")
	}
	if got.Source != "workbook-seed" {
		t.Fatalf("source = %q", got.Source)
	}
	// E4 HUF/EUR 0.0028 inverted.
	if want := "357.142857142857"; fx.FormatRate(got.Rate) != want {
		t.Fatalf("EUR->HUF = %s, want %s", fx.FormatRate(got.Rate), want)
	}
}

func TestRateOn_NearestEarlier(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	for _, r := range []struct {
		date string
		rate string
	}{
		{"2026-07-01", "390"},
		{"2026-07-20", "395"},
	} {
		rate, _ := fx.ParseRate(r.rate)
		if _, err := svc.Upsert(h.ctx, fx.Rate{
			AsOf: mustDate(r.date), Base: "EUR", Quote: "HUF", Rate: rate,
			Source: "open-er-api", FetchedAt: fixedNow,
		}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	// A gap resolves to the earlier rate.
	got, err := svc.RateOn(h.ctx, "EUR", "HUF", mustDate("2026-07-15"))
	if err != nil || got == nil {
		t.Fatalf("RateOn: %v %v", got, err)
	}
	if fx.FormatRate(got.Rate) != "390" {
		t.Fatalf("rate on 15 July = %s, want the 1 July rate 390", fx.FormatRate(got.Rate))
	}
	if got.AsOf.Format(DateLayout) != "2026-07-01" {
		t.Fatalf("as_of = %s, want 2026-07-01", got.AsOf.Format(DateLayout))
	}

	// Never a later rate: a figure computed in July must not change because a rate
	// arrived in August.
	got, err = svc.RateOn(h.ctx, "EUR", "HUF", mustDate("2026-07-19"))
	if err != nil || got == nil {
		t.Fatalf("RateOn: %v %v", got, err)
	}
	if fx.FormatRate(got.Rate) == "395" {
		t.Fatal("a rate dated later than the request was used")
	}
}

func TestRateOn_NoRate_ReturnsNilNil(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	// Before every stored rate.
	got, err := svc.RateOn(h.ctx, "EUR", "HUF", mustDate("2019-01-01"))
	if err != nil {
		t.Fatalf("a missing rate is not an error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no rate, got %+v", got)
	}
	// A currency with no rates at all.
	got, err = svc.RateOn(h.ctx, "EUR", "PLN", mustDate("2026-07-15"))
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no rate, got %+v", got)
	}
}

func TestRateOn_IdentityAndInverse(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	same, err := svc.RateOn(h.ctx, "EUR", "EUR", mustDate("2026-07-15"))
	if err != nil || same == nil {
		t.Fatalf("RateOn: %v %v", same, err)
	}
	if same.Rate.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("EUR->EUR = %s, want 1", fx.FormatRate(same.Rate))
	}

	// Only EUR->X is stored; the inverse is computed, which is what removes the
	// workbook's 1.14 / 0.88 contradiction.
	inverse, err := svc.RateOn(h.ctx, "HUF", "EUR", mustDate("2026-07-15"))
	if err != nil || inverse == nil {
		t.Fatalf("RateOn: %v %v", inverse, err)
	}
	forward, _ := svc.RateOn(h.ctx, "EUR", "HUF", mustDate("2026-07-15"))
	product := new(big.Rat).Mul(inverse.Rate, forward.Rate)
	if product.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("inverse x forward = %s, want exactly 1", fx.FormatRate(product))
	}
}

func TestConvert_HUFToEUR(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	// 4000 HUF at the seeded rate of 357.142857142857 HUF per EUR.
	// 4000 / 357.142857142857 = 11.2000000000000...  -> 1120 minor EUR.
	// HUF has exponent 0, EUR has 2, so the scaling has to be right too.
	got, rateID, err := svc.Convert(h.ctx, money.New(4000, "HUF"), "EUR", mustDate("2026-07-15"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if rateID == nil {
		t.Fatal("the rate id must be recorded for audit")
	}
	if got.Currency != "EUR" {
		t.Fatalf("currency = %s", got.Currency)
	}
	if got.Minor != 1120 {
		t.Fatalf("4000 HUF = %d EUR minor units, want 1120 (11.20)", got.Minor)
	}
}

func TestConvert_SameCurrencyIsIdentity(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	got, rateID, err := svc.Convert(h.ctx, money.New(1250, "EUR"), "EUR", mustDate("2026-07-15"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 1250 || got.Currency != "EUR" {
		t.Fatalf("got %+v", got)
	}
	if rateID != nil {
		t.Fatal("no rate is involved in an identity conversion")
	}
}

func TestConvert_CrossRate(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	// USD->HUF via EUR: (EUR->HUF) / (EUR->USD) = 357.142857142857 / 1.14
	//                 = 313.28320802...
	// 100.00 USD -> 31328 HUF (exponent 0), rounded once at the end.
	got, _, err := svc.Convert(h.ctx, money.New(10000, "USD"), "HUF", mustDate("2026-07-15"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Currency != "HUF" {
		t.Fatalf("currency = %s", got.Currency)
	}
	if got.Minor != 31328 {
		t.Fatalf("100 USD = %d HUF, want 31328", got.Minor)
	}
}

func TestConvert_RoundsOnceHalfUp(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	rate, _ := fx.ParseRate("2")
	if _, err := svc.Upsert(h.ctx, fx.Rate{
		AsOf: mustDate("2026-01-01"), Base: "EUR", Quote: "UAH", Rate: rate,
		Source: "open-er-api", FetchedAt: fixedNow,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// 1 UAH minor unit at 2 UAH per EUR is 0.5 EUR minor units, which rounds up.
	got, _, err := svc.Convert(h.ctx, money.New(1, "UAH"), "EUR", mustDate("2026-06-01"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 1 {
		t.Fatalf("0.5 minor units must round half-up to 1, got %d", got.Minor)
	}
	// 3 -> 1.5 -> 2.
	got, _, err = svc.Convert(h.ctx, money.New(3, "UAH"), "EUR", mustDate("2026-06-01"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 2 {
		t.Fatalf("1.5 minor units must round half-up to 2, got %d", got.Minor)
	}
}

func TestConvert_NoRateReturnsNothing(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	got, rateID, err := svc.Convert(h.ctx, money.New(1000, "HUF"), "EUR", mustDate("2019-01-01"))
	if err != nil {
		t.Fatalf("a missing rate must not be an error: %v", err)
	}
	if rateID != nil || got.Minor != 0 {
		t.Fatalf("expected an empty result, got %+v / %v", got, rateID)
	}
}

func TestFx_UpsertIsIdempotentPerDayAndSource(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	rate, _ := fx.ParseRate("390.5")
	first, err := svc.Upsert(h.ctx, fx.Rate{
		AsOf: mustDate("2026-07-20"), Base: "EUR", Quote: "HUF", Rate: rate,
		Source: "open-er-api", FetchedAt: fixedNow,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	updated, _ := fx.ParseRate("391")
	second, err := svc.Upsert(h.ctx, fx.Rate{
		AsOf: mustDate("2026-07-20"), Base: "EUR", Quote: "HUF", Rate: updated,
		Source: "open-er-api", FetchedAt: fixedNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("re-running a fetch must update in place, got ids %d and %d", first.ID, second.ID)
	}
	if fx.FormatRate(second.Rate) != "391" {
		t.Fatalf("rate = %s, want the updated 391", fx.FormatRate(second.Rate))
	}
}

func TestFx_TwoProvidersOneDayResolveByPriority(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	primary, _ := fx.ParseRate("390")
	fallback, _ := fx.ParseRate("395")
	// UNIQUE (as_of_date, base, quote, source) lets both hold an opinion.
	if _, err := svc.Upsert(h.ctx, fx.Rate{
		AsOf: mustDate("2026-07-20"), Base: "EUR", Quote: "HUF", Rate: fallback,
		Source: "fawazahmed0", FetchedAt: fixedNow,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := svc.Upsert(h.ctx, fx.Rate{
		AsOf: mustDate("2026-07-20"), Base: "EUR", Quote: "HUF", Rate: primary,
		Source: "open-er-api", FetchedAt: fixedNow,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := svc.RateOn(h.ctx, "EUR", "HUF", mustDate("2026-07-20"))
	if err != nil || got == nil {
		t.Fatalf("RateOn: %v %v", got, err)
	}
	// open-er-api has priority 10, fawazahmed0 has 20; lower wins.
	if got.Source != "open-er-api" {
		t.Fatalf("source = %q, want the higher-priority open-er-api", got.Source)
	}
}

func TestFx_RejectsNonEURBase(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	rate, _ := fx.ParseRate("0.0028")
	if _, err := svc.Upsert(h.ctx, fx.Rate{
		AsOf: mustDate("2026-07-20"), Base: "HUF", Quote: "EUR", Rate: rate, Source: "manual",
	}); err == nil {
		t.Fatal("storing the inverse direction must be refused; it is computed")
	}
}

func TestFx_History(t *testing.T) {
	h := newHarness(t)
	svc := h.fxService()

	for _, d := range []string{"2026-07-01", "2026-07-10", "2026-07-20"} {
		rate, _ := fx.ParseRate("390")
		if _, err := svc.Upsert(h.ctx, fx.Rate{
			AsOf: mustDate(d), Base: "EUR", Quote: "HUF", Rate: rate,
			Source: "open-er-api", FetchedAt: fixedNow,
		}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	got, err := svc.History(h.ctx, "EUR", "HUF", mustDate("2026-07-05"), mustDate("2026-07-25"))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d rates in range, want 2", len(got))
	}
	if !got[0].AsOf.Before(got[1].AsOf) {
		t.Fatal("history must be ascending by date")
	}
}
