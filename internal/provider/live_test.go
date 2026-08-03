//go:build live

// This file is behind a build tag on purpose. CI must not depend on someone
// else's uptime, but provider drift has to be detectable by a human:
//
//	go test -tags=live ./internal/provider/
//
// It hits the real endpoints and asserts only what the design depends on — that
// both answer, that both carry UAH, and that the rates are in a sane range.
package provider

import (
	"context"
	"math/big"
	"testing"
	"time"
)

func TestLive_BothProvidersAnswer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := NewClient()
	for _, p := range []RateProvider{
		NewOpenErAPI(client, ""),
		NewFawazahmed0(client, ""),
	} {
		t.Run(p.Key(), func(t *testing.T) {
			rates, asOf, err := p.Fetch(ctx, "EUR")
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			for _, code := range Required {
				if rates[code] == nil {
					t.Fatalf("%s is missing; this provider can no longer serve as a source", code)
				}
			}
			// UAH per EUR has sat between 30 and 80 for years. A value outside
			// that says the response shape changed, not that the currency moved.
			uah, _ := rates["UAH"].Float64()
			if uah < 30 || uah > 80 {
				t.Errorf("EUR->UAH = %v, which is outside any plausible range", uah)
			}
			if rates["EUR"] != nil && rates["EUR"].Cmp(big.NewRat(1, 1)) != 0 {
				t.Errorf("EUR->EUR = %v, want 1", rates["EUR"])
			}
			t.Logf("%s: %d rates as of %s", p.Key(), len(rates), asOf.Format(time.RFC3339))
		})
	}
}
