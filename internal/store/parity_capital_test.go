package store

import (
	"math"
	"math/big"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// The capital half of the Excel parity gate — appendix §A.4-A.5.
//
// The sheet's settings block stored one direction of each rate (`USD/EUR = 0.88`)
// and used it inline in every parent formula. Here the rates are stored EUR-based
// and the inverse is computed, which is what removes the 1.14 / 0.88
// inconsistency the sheet carried in cells E2 and E3.

type parityAccount struct {
	Row      int                     `json:"row"`
	Name     string                  `json:"name"`
	Currency string                  `json:"currency"`
	Exponent int                     `json:"exponent"`
	IsLiquid bool                    `json:"is_liquid"`
	Counts   bool                    `json:"counts_toward_net_worth"`
	Parent   *string                 `json:"parent"`
	Native   map[period.Period]int64 `json:"native"`
	Base     map[period.Period]int64 `json:"base"`
}

type parityCapital struct {
	Rates         map[string]float64                       `json:"rates"`
	Accounts      []parityAccount                          `json:"accounts"`
	ReadyForUsage map[period.Period]parityMoney            `json:"ready_for_usage"`
	General       map[period.Period]parityMoney            `json:"general"`
	GeneralIn     map[string]map[period.Period]parityMoney `json:"general_in"`
	Allocation    map[period.Period]map[string]float64     `json:"allocation"`
	RunwayLegacy  map[period.Period]struct {
		Workbook float64 `json:"workbook"`
	} `json:"runway_legacy_blend"`
	WorkbookAllocationSum float64 `json:"workbook_allocation_sum"`
	WorkbookCashHUFShare  float64 `json:"workbook_cash_huf_share"`
}

// capitalWorld is a database seeded from the sheet's capital block.
type capitalWorld struct {
	fixture parityFile
	capital parityCapital
	data    capital.Data
	metrics metrics.Data
	byName  map[string]account.Account
	h       *harness
	userID  int64
}

func seedCapitalParity(t *testing.T) capitalWorld {
	t.Helper()
	fixture := loadParity(t)
	if len(fixture.Capital.Accounts) == 0 {
		t.Fatal("the parity fixture has no capital block; regenerate it")
	}

	h := newHarness(t)
	u := h.user("capital-parity@example.test")
	accounts := h.accountService()

	// EUR-based rates, one row per currency per month. The sheet's rates carry no
	// date at all; dating them is what makes the real/FX split possible.
	for code, perEUR := range fixture.Capital.Rates {
		rate, err := fx.ParseRate(ratString(perEUR))
		if err != nil {
			t.Fatalf("parsing rate for %s: %v", code, err)
		}
		inverse := new(big.Rat).Inv(rate)
		for _, p := range fixture.Periods {
			_, last := p.Range()
			if _, err := h.fxService().Upsert(h.ctx, fx.Rate{
				AsOf: last, Base: fx.StorageBase, Quote: code, Rate: inverse, Source: "parity",
			}); err != nil {
				t.Fatalf("seeding rate %s %s: %v", code, p, err)
			}
		}
		// The month before the first is loaded for the change comparison.
		_, last := fixture.Periods[0].Prev().Range()
		if _, err := h.fxService().Upsert(h.ctx, fx.Rate{
			AsOf: last, Base: fx.StorageBase, Quote: code, Rate: inverse, Source: "parity",
		}); err != nil {
			t.Fatalf("seeding opening rate %s: %v", code, err)
		}
	}

	all, err := accounts.List(h.ctx, u.ID, true)
	if err != nil {
		t.Fatalf("listing accounts: %v", err)
	}
	byName := map[string]account.Account{}
	for _, a := range all {
		byName[a.Name] = a
	}

	snapshots := NewSnapshotRepo(h.db)
	capitalSvc := capital.NewService(snapshots, accounts, h.fxService(), h.Currencies, h.clock)

	for _, fa := range fixture.Capital.Accounts {
		acct, ok := byName[fa.Name]
		if !ok {
			t.Fatalf("the seeded chart of accounts has no %q (sheet row %d)", fa.Name, fa.Row)
		}
		if acct.IsLiquid != fa.IsLiquid {
			t.Errorf("account %q liquid = %v, want %v", fa.Name, acct.IsLiquid, fa.IsLiquid)
		}
		if acct.CountsTowardNetWorth != fa.Counts {
			t.Errorf("account %q counts = %v, want %v", fa.Name, acct.CountsTowardNetWorth, fa.Counts)
		}
		for p, minor := range fa.Native {
			if _, err := capitalSvc.Upsert(h.ctx, u.ID, acct.ID, p, "EUR", capital.Input{
				Amount: ptrMoney(money.New(minor, fa.Currency)),
			}); err != nil {
				t.Fatalf("recording %s %s: %v", fa.Name, p, err)
			}
		}
	}

	first, last := fixture.Periods[0], fixture.Periods[len(fixture.Periods)-1]
	md, _, err := metrics.NewService(NewMetricsRepo(h.db), h.categoryService()).
		Load(h.ctx, u.ID, first, last, "EUR")
	if err != nil {
		t.Fatalf("loading metrics: %v", err)
	}
	data, err := capitalSvc.Load(h.ctx, u.ID, first, last, "EUR", md, capital.Contemporaneous)
	if err != nil {
		t.Fatalf("loading capital: %v", err)
	}
	return capitalWorld{
		fixture: fixture, capital: fixture.Capital, data: data, metrics: md,
		byName: byName, h: h, userID: u.ID,
	}
}

func TestParity_ReadyForUsage(t *testing.T) {
	w := seedCapitalParity(t)

	for p, want := range w.capital.ReadyForUsage {
		got := capital.ReadyForUsage(w.data, p)
		if got == nil {
			t.Errorf("ready for usage %s is nil, want %d minor", p, want.ExpectedMinor)
			continue
		}
		if got.Minor != want.ExpectedMinor {
			t.Errorf("ready for usage %s = %d minor, want %d", p, got.Minor, want.ExpectedMinor)
		}
	}

	// §A.9: 6674.8 + 11644.8584.
	if got := capital.ReadyForUsage(w.data, "2026-06"); got == nil || got.Minor != 1831966 {
		t.Fatalf("ready for usage = %+v, want 18319.66", got)
	}
}

func TestParity_General(t *testing.T) {
	w := seedCapitalParity(t)

	for p, want := range w.capital.General {
		got := capital.General(w.data, p)
		if got == nil || got.Minor != want.ExpectedMinor {
			t.Errorf("general %s = %+v, want %d minor", p, got, want.ExpectedMinor)
		}
	}
	// §A.9: the nine-term sum.
	if got := capital.General(w.data, "2026-06"); got == nil || got.Minor != 2658166 {
		t.Fatalf("general = %+v, want 26581.66", got)
	}
}

// TestParity_BanksFOPCountedOnce is the sheet's most instructive bug: `Banks FOP`
// was left out of the hand-written `Banks` formula (row 41) yet included in
// `General` (row 55). As an ordinary child of a computed parent it is now in both.
func TestParity_BanksFOPCountedOnce(t *testing.T) {
	w := seedCapitalParity(t)
	p := period.Period("2026-06")

	banks := capital.Value(w.data, w.byName["Banks"].ID, p)
	if banks == nil {
		t.Fatal("Banks has no value")
	}
	children := int64(0)
	for _, name := range []string{"Banks USD", "Banks EUR", "Banks HUF", "Banks UAH", "Banks FOP"} {
		v := capital.Value(w.data, w.byName[name].ID, p)
		if v != nil {
			children += v.Minor
		}
	}
	if banks.Minor != children {
		t.Fatalf("Banks = %d, want the sum of its five children %d", banks.Minor, children)
	}

	// And counted exactly once in net worth: parents are computed, never stored.
	general := capital.General(w.data, p)
	if general.Minor != w.capital.General[p].ExpectedMinor {
		t.Fatalf("general = %d, want %d — a parent must not be added alongside its children",
			general.Minor, w.capital.General[p].ExpectedMinor)
	}
}

func TestParity_AllocationSumsToOne(t *testing.T) {
	w := seedCapitalParity(t)

	for _, p := range w.fixture.Periods {
		shares := capital.Allocation(w.data, p)
		if len(shares) == 0 {
			continue
		}
		sum := 0.0
		for _, sh := range shares {
			sum += sh.Share
			if len(w.data.Children[sh.AccountID]) > 0 {
				t.Errorf("%s: allocation includes the parent %q", p, sh.Name)
			}
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("%s: shares sum to %.12f, want 1.0", p, sum)
		}
	}

	// The sheet's own doughnut summed to about 162% because it plotted parents
	// alongside their children — deviation D6.
	if w.capital.WorkbookAllocationSum < 1.5 {
		t.Fatalf("the fixture records the workbook's allocation sum as %.4f; "+
			"it should be about 1.62, which is the bug D6 describes",
			w.capital.WorkbookAllocationSum)
	}
}

func TestParity_CashHUFShare(t *testing.T) {
	w := seedCapitalParity(t)
	p := period.Period("2026-06")

	var got float64
	for _, sh := range capital.Allocation(w.data, p) {
		if sh.Name == "Cash HUF" {
			got = sh.Share
		}
	}
	want := w.capital.Allocation[p]["Cash HUF"]
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("Cash HUF share = %.10f, want %.10f", got, want)
	}
	// 11,000 HUF is 30.80 EUR of a 26,581.66 EUR net worth: about 0.116%. The
	// sheet read 41.4% because it divided raw forint by euros.
	if got > 0.002 {
		t.Fatalf("Cash HUF share = %.6f, want about 0.00116 — the unconverted bug is back", got)
	}
	if math.Abs(w.capital.WorkbookCashHUFShare-0.4138191769) > 1e-9 {
		t.Fatalf("the fixture no longer records the workbook's 41.4%%: %.10f",
			w.capital.WorkbookCashHUFShare)
	}
}

func TestParity_GeneralInOtherCurrencies(t *testing.T) {
	w := seedCapitalParity(t)
	p := period.Period("2026-06")
	svc := capital.NewService(NewSnapshotRepo(w.h.db), w.h.accountService(),
		w.h.fxService(), w.h.Currencies, w.h.clock)

	for code, byPeriod := range w.capital.GeneralIn {
		want := byPeriod[p]
		got, err := svc.GeneralInCurrency(w.h.ctx, w.data, code, p)
		if err != nil {
			t.Fatalf("GeneralInCurrency(%s): %v", code, err)
		}
		if got == nil {
			t.Errorf("net worth in %s is nil, want %d minor", code, want.ExpectedMinor)
			continue
		}
		if got.Minor != want.ExpectedMinor {
			t.Errorf("net worth in %s = %d minor, want %d (sheet %v)",
				code, got.Minor, want.ExpectedMinor, want.Workbook)
		}
	}
}

func TestParity_Runway_LegacyBlend(t *testing.T) {
	w := seedCapitalParity(t)
	p := period.Period("2026-06")

	// The blend needs the spending side, which the capital-only seed does not
	// have; the runway is therefore nil here and the figure itself is asserted in
	// TestParity_Runway_UsesEachPeriodsOwnBurnRate against a full dataset.
	rates := w.data.Burn[p]
	if _, ok := rates[capital.BurnLegacyBlend]; ok {
		if got := capital.Runway(w.data, p, capital.BurnLegacyBlend); got == nil {
			t.Fatal("a burn rate exists but the runway is nil")
		}
	}

	// The sheet's own figure is recorded so the divergence stays visible.
	if got := w.capital.RunwayLegacy[p].Workbook; math.Abs(got-7.136169061) > 1e-9 {
		t.Fatalf("the fixture records the workbook's runway as %.9f, want 7.136169061", got)
	}
}

func ptrMoney(m money.Money) *money.Money { return &m }

// ratString renders a sheet rate for exact parsing. The sheet's own values have
// at most four decimal places.
func ratString(v float64) string {
	return trimFloat(v)
}

func trimFloat(v float64) string {
	s := big.NewFloat(v).Text('f', 10)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
