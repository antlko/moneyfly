package fx

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"
)

type fakeProvider struct {
	key   string
	rates map[string]*big.Rat
	asOf  time.Time
	err   error
	calls int
}

func (f *fakeProvider) Key() string { return f.key }
func (f *fakeProvider) Fetch(context.Context, string) (map[string]*big.Rat, time.Time, error) {
	f.calls++
	if f.err != nil {
		return nil, time.Time{}, f.err
	}
	return f.rates, f.asOf, nil
}

func table(t *testing.T, pairs map[string]string) map[string]*big.Rat {
	t.Helper()
	out := map[string]*big.Rat{}
	for code, value := range pairs {
		out[code] = ratOf(t, value)
	}
	return out
}

func TestRefresher_FirstProviderThatAnswersWins(t *testing.T) {
	primary := &fakeProvider{key: "primary", err: errors.New("502")}
	fallback := &fakeProvider{
		key:   "fallback",
		rates: table(t, map[string]string{"USD": "1.14", "HUF": "400"}),
	}
	service := serviceWith(t)

	result, err := NewRefresher(service, []Provider{primary, fallback}, 0, nil).
		Refresh(context.Background(), on(t, "2026-08-04"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.Provider != "fallback" {
		t.Errorf("provider = %q, want the fallback to have taken over", result.Provider)
	}
	if result.Stored != 2 {
		t.Errorf("stored = %d, want 2", result.Stored)
	}
	// The primary's failure is still reported: a degraded primary that is being
	// silently covered for is exactly the thing worth knowing about.
	if len(result.Errors) != 1 {
		t.Errorf("errors = %v, want the primary's failure recorded", result.Errors)
	}
}

// A total outage keeps the last stored value. Nothing is deleted, no screen
// breaks, and the caller decides how loudly to complain.
func TestRefresher_TotalOutageIsNotAnError(t *testing.T) {
	down := &fakeProvider{key: "down", err: errors.New("dns")}
	service := serviceWith(t, eurTo(t, "USD", "1.14", "2026-08-01"))

	result, err := NewRefresher(service, []Provider{down}, 0, nil).
		Refresh(context.Background(), on(t, "2026-08-04"))
	if err != nil {
		t.Fatalf("Refresh must not fail on a provider outage: %v", err)
	}
	if result.Provider != "" || result.Stored != 0 {
		t.Errorf("result = %+v, want nothing stored", result)
	}
	if got, _ := service.RateOn(context.Background(), StorageBase, "USD", on(t, "2026-08-04")); got == nil {
		t.Error("the previously stored rate must survive an outage")
	}
}

// The plausibility check compares a provider against a provider — never against
// a number a person typed.
//
// Otherwise one mistyped rate poisons the pair permanently: every real rate
// afterwards looks like an implausible jump away from the typo and is refused,
// and the symptom is rates that quietly stop moving — the exact failure the
// check exists to prevent.
func TestRefresher_AManualRateIsNotAPlausibilityYardstick(t *testing.T) {
	typo := eurTo(t, "HUF", "39.15", "2026-08-01") // a decimal point out of place
	typo.Source = SourceManual
	service := serviceWith(t, typo)
	p := &fakeProvider{key: "good", rates: table(t, map[string]string{"HUF": "391.5"})}

	result, err := NewRefresher(service, []Provider{p}, 0, nil).
		Refresh(context.Background(), on(t, "2026-08-04"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("rejected = %+v, want the real rate accepted", result.Rejected)
	}
	got, _ := service.RateOn(context.Background(), StorageBase, "HUF", on(t, "2026-08-04"))
	if got == nil || got.Rate.Cmp(ratOf(t, "391.5")) != 0 {
		t.Errorf("HUF = %v, want the provider's 391.5 stored", got)
	}
}

// A rate that moved 10x is far more likely to be a broken feed than a currency
// event, and storing it would corrupt every derived figure while looking
// entirely plausible on screen.
func TestRefresher_ImplausibleMoveIsRejectedAndThePreviousKept(t *testing.T) {
	service := serviceWith(t, eurTo(t, "USD", "1.14", "2026-08-01"))
	p := &fakeProvider{
		key:   "wobbly",
		rates: table(t, map[string]string{"USD": "11.4", "HUF": "400"}),
	}

	result, err := NewRefresher(service, []Provider{p}, 0, nil).
		Refresh(context.Background(), on(t, "2026-08-04"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Quote != "USD" {
		t.Fatalf("rejected = %+v, want the USD move refused", result.Rejected)
	}
	// The rest of the table still lands: one bad quote must not cost the others.
	if result.Stored != 1 {
		t.Errorf("stored = %d, want HUF to have been stored anyway", result.Stored)
	}
	got, _ := service.RateOn(context.Background(), StorageBase, "USD", on(t, "2026-08-04"))
	if got == nil || got.Rate.Cmp(ratOf(t, "1.14")) != 0 {
		t.Errorf("USD = %v, want the previous 1.14 kept", got)
	}
}

// A first-ever rate has nothing to be compared against, so the gate must not
// reject it — otherwise a fresh instance can never store anything.
func TestRefresher_FirstRateForAPairIsAlwaysAccepted(t *testing.T) {
	service := serviceWith(t)
	p := &fakeProvider{key: "p", rates: table(t, map[string]string{"XAU": "0.0003"})}

	result, err := NewRefresher(service, []Provider{p}, 0, nil).
		Refresh(context.Background(), on(t, "2026-08-04"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.Stored != 1 || len(result.Rejected) != 0 {
		t.Errorf("result = %+v, want the first rate stored", result)
	}
}

// The provider's own date wins over "today": a rate published on Friday is
// Friday's rate, and recording it as Sunday's would make the weekend look like
// a move.
func TestRefresher_UsesTheProvidersDate(t *testing.T) {
	service := serviceWith(t)
	p := &fakeProvider{
		key:   "p",
		rates: table(t, map[string]string{"USD": "1.14"}),
		asOf:  on(t, "2026-07-31"),
	}

	result, err := NewRefresher(service, []Provider{p}, 0, nil).
		Refresh(context.Background(), on(t, "2026-08-02"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !result.AsOf.Equal(on(t, "2026-07-31")) {
		t.Errorf("asOf = %s, want the provider's 2026-07-31", result.AsOf.Format(DateLayout))
	}
}
