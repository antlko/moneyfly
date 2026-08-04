package db

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"moneyfly/internal/fx"
	syncproto "moneyfly/internal/sync"
)

func date(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(fx.DateLayout, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return parsed.UTC()
}

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("%q is not a rational", s)
	}
	return r
}

func applyRow(t *testing.T, d *DB, userID, entity, id, data string) {
	t.Helper()
	if _, err := d.ApplyOps(userID, []syncproto.Op{{
		Entity: entity, ID: id, Lamport: 1, DeviceID: "dev-1", Data: json.RawMessage(data),
	}}); err != nil {
		t.Fatalf("ApplyOps(%s/%s): %v", entity, id, err)
	}
}

func store(t *testing.T, repo *FxRepo, day, quote, rate, source string) {
	t.Helper()
	if _, err := repo.Upsert(context.Background(), fx.Rate{
		AsOf: date(t, day), Base: fx.StorageBase, Quote: quote,
		Rate: rat(t, rate), Source: source, FetchedAt: date(t, day),
	}); err != nil {
		t.Fatalf("Upsert(%s %s): %v", day, quote, err)
	}
}

// A rate must never be read from the future. A figure computed in March cannot
// change because a rate arrived in April — that is what makes a past month's
// total stable.
func TestFxRepo_RateOnTakesTheNearestEarlier(t *testing.T) {
	repo := openTest(t).Rates()
	ctx := context.Background()
	store(t, repo, "2026-07-01", "USD", "1.10", "open-er-api")
	store(t, repo, "2026-07-10", "USD", "1.20", "open-er-api")

	cases := []struct {
		on, want string
	}{
		{"2026-07-10", "1.20"}, // exact
		{"2026-07-05", "1.10"}, // between: the earlier one
		{"2026-07-31", "1.20"}, // after the last: the last one
	}
	for _, c := range cases {
		got, err := repo.RateOn(ctx, fx.StorageBase, "USD", date(t, c.on))
		if err != nil {
			t.Fatalf("RateOn(%s): %v", c.on, err)
		}
		if got == nil {
			t.Fatalf("RateOn(%s) = nil", c.on)
		}
		if got.Rate.Cmp(rat(t, c.want)) != 0 {
			t.Errorf("RateOn(%s) = %s, want %s", c.on, fx.FormatRate(got.Rate), c.want)
		}
	}

	// Before anything stored: not an error, just nothing to say.
	got, err := repo.RateOn(ctx, fx.StorageBase, "USD", date(t, "2026-06-30"))
	if err != nil || got != nil {
		t.Fatalf("RateOn before history = %v, %v; want nil, nil", got, err)
	}
}

// The refresh loop reruns after a restart, and running it twice in a day must
// not double the table.
func TestFxRepo_UpsertIsIdempotent(t *testing.T) {
	repo := openTest(t).Rates()
	ctx := context.Background()
	store(t, repo, "2026-07-01", "HUF", "360.4", "open-er-api")
	store(t, repo, "2026-07-01", "HUF", "361.9", "open-er-api")

	history, err := repo.History(ctx, fx.StorageBase, "HUF", date(t, "2026-07-01"), date(t, "2026-07-01"))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history has %d rows, want 1 — the same day and source must overwrite", len(history))
	}
	if fx.FormatRate(history[0].Rate) != "361.9" {
		t.Errorf("rate = %s, want the second write to win", fx.FormatRate(history[0].Rate))
	}
}

// Rates are stored as decimal text precisely so they survive the round trip. A
// REAL column would return 1.1381079999999999 here.
func TestFxRepo_RatesRoundTripExactly(t *testing.T) {
	repo := openTest(t).Rates()
	store(t, repo, "2026-07-29", "USD", "1.138108", "open-er-api")

	got, err := repo.RateOn(context.Background(), fx.StorageBase, "USD", date(t, "2026-07-29"))
	if err != nil || got == nil {
		t.Fatalf("RateOn: %v, %v", got, err)
	}
	if got.Rate.Cmp(rat(t, "1.138108")) != 0 {
		t.Errorf("rate = %s, want 1.138108 exactly", got.Rate.FloatString(12))
	}
}

func TestFxRepo_LatestIsOnePerQuote(t *testing.T) {
	repo := openTest(t).Rates()
	store(t, repo, "2026-07-01", "USD", "1.10", "open-er-api")
	store(t, repo, "2026-07-10", "USD", "1.20", "open-er-api")
	store(t, repo, "2026-07-10", "USD", "1.21", "fawazahmed0")
	store(t, repo, "2026-07-02", "HUF", "360.4", "open-er-api")

	latest, err := repo.Latest(context.Background(), fx.StorageBase)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("Latest returned %d rows, want one per quote (USD, HUF)", len(latest))
	}
	byQuote := map[string]string{}
	for _, r := range latest {
		byQuote[r.Quote] = fx.FormatRate(r.Rate)
	}
	// Two sources hold 2026-07-10; the later write is the one that answered.
	if byQuote["USD"] != "1.21" {
		t.Errorf("USD = %s, want 1.21", byQuote["USD"])
	}
	if byQuote["HUF"] != "360.4" {
		t.Errorf("HUF = %s, want 360.4", byQuote["HUF"])
	}
}

// What the refresher requires a provider to publish is derived from the data,
// not hardcoded: adding a forint account is what makes the instance insist on a
// forint rate.
func TestUsedCurrencies(t *testing.T) {
	d := openTest(t)
	user := mustUser(t, d, "a@b.c")
	ctx := context.Background()

	if got, err := d.UsedCurrencies(ctx); err != nil || len(got) != 0 {
		t.Fatalf("UsedCurrencies on a fresh instance = %v, %v; want empty", got, err)
	}

	applyRow(t, d, user.ID, "account", "acc-1", `{"name":"Cash","currency":"HUF"}`)
	applyRow(t, d, user.ID, "txn", "txn-1",
		`{"kind":"transfer","occurredOn":"2026-08-01","amountMinor":-100,"currency":"EUR",`+
			`"toAmountMinor":4200,"toCurrency":"UAH"}`)

	got, err := d.UsedCurrencies(ctx)
	if err != nil {
		t.Fatalf("UsedCurrencies: %v", err)
	}
	// EUR is excluded: it is the storage base, so there is no EUR->EUR rate to
	// demand of anyone.
	want := map[string]bool{"HUF": true, "UAH": true}
	if len(got) != len(want) {
		t.Fatalf("UsedCurrencies = %v, want %v", got, want)
	}
	for _, code := range got {
		if !want[code] {
			t.Errorf("UsedCurrencies included %q", code)
		}
	}
}
