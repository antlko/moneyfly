package store

import (
	"context"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/setting"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
	"github.com/antlko/moneyapp/internal/provider"
)

// stubProvider serves a recorded fixture, or an error, without touching the
// network. The chain's behaviour is what these tests are about.
type stubProvider struct {
	key   string
	rates map[string]*big.Rat
	asOf  time.Time
	err   error
	calls int
}

func (s *stubProvider) Key() string { return s.key }

func (s *stubProvider) Fetch(context.Context, string) (map[string]*big.Rat, time.Time, error) {
	s.calls++
	if s.err != nil {
		return nil, time.Time{}, s.err
	}
	return s.rates, s.asOf, nil
}

func rats(t *testing.T, pairs map[string]string) map[string]*big.Rat {
	t.Helper()
	out := map[string]*big.Rat{}
	for code, decimal := range pairs {
		r, ok := new(big.Rat).SetString(decimal)
		if !ok {
			t.Fatalf("bad rate %q", decimal)
		}
		out[code] = r
	}
	return out
}

// settingHarness wires the settings service over the real database with stub
// providers in place of the network.
type settingHarness struct {
	*harness
	settings  *setting.Service
	primary   *stubProvider
	fallback  *stubProvider
	userID    int64
	refresher *fx.Refresher
}

func newSettingHarness(t *testing.T, maxChange float64) settingHarness {
	t.Helper()
	h := newHarness(t)
	u := h.user("owner@example.test")

	primary := &stubProvider{key: "open-er-api", asOf: fixedNow}
	fallback := &stubProvider{key: "fawazahmed0", asOf: fixedNow}
	repo := NewSettingRepo(h.db)
	refresher := fx.NewRefresher(h.fxService(),
		[]fx.Provider{primary, fallback}, maxChange, []string{"USD", "HUF", "UAH"})

	return settingHarness{
		harness:  h,
		settings: setting.NewService(repo, fx.NewSettingFetcher(refresher, h.fxService(), h.clock), repo, h.clock),
		primary:  primary, fallback: fallback, userID: u.ID, refresher: refresher,
	}
}

func TestSetting_SeededForANewUser(t *testing.T) {
	h := newSettingHarness(t, 0.15)

	all, err := h.settings.List(h.ctx, h.userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byKey := map[string]setting.Setting{}
	for _, s := range all {
		byKey[s.Key] = s
	}

	// The workbook's C2 and its hardcoded 2x, now editable.
	if got := byKey[setting.KeyWarnPercent]; got.Effective() == nil || *got.Effective() != "10" {
		t.Errorf("warn percent = %+v, want 10 from the workbook's C2", got.Effective())
	}
	if got := byKey[setting.KeyOverMultiplier]; got.Effective() == nil || *got.Effective() != "2" {
		t.Errorf("over multiplier = %+v, want 2", got.Effective())
	}
	for _, quote := range []string{"USD", "HUF", "UAH"} {
		got, ok := byKey[setting.FXKey(quote)]
		if !ok {
			t.Fatalf("no setting for %s", quote)
		}
		if got.Mode != setting.ModeAuto || got.ProviderKey == nil {
			t.Errorf("%s = %+v, want auto with a provider", quote, got)
		}
	}
	// Gold stays manual until the quantity behind `Gold = 3000` is supplied.
	if got := byKey[setting.KeyPriceXAU]; got.Mode != setting.ModeManual {
		t.Errorf("price.XAU = %q, want manual pending the gold quantity", got.Mode)
	}
}

func TestChain_PrimaryFails_UsesFallback(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.err = errors.New("open-er-api: 503")
	h.fallback.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51.08"})

	updated, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if updated.LastValue == nil || *updated.LastValue != "51.08" {
		t.Fatalf("last value = %+v, want the fallback's 51.08", updated.LastValue)
	}
	if updated.LastError != nil {
		t.Errorf("last error = %q, want none: the fallback answered", *updated.LastError)
	}
	if h.fallback.calls != 1 {
		t.Errorf("fallback called %d times, want 1", h.fallback.calls)
	}

	// And the rate landed in the dated history, where every report reads it.
	rate, err := h.fxService().RateOn(h.ctx, "EUR", "UAH", fixedNow)
	if err != nil || rate == nil {
		t.Fatalf("the rate was not stored: %v", err)
	}
}

func TestChain_BothFail_KeepsLastValue(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51.08"})
	if _, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH")); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	h.primary.err = errors.New("primary down")
	h.fallback.err = errors.New("fallback down")
	updated, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH"))
	if err != nil {
		t.Fatalf("a provider outage must not be an error to the caller: %v", err)
	}
	if updated.LastValue == nil || *updated.LastValue != "51.08" {
		t.Fatalf("last value = %+v, want the previous 51.08 held", updated.LastValue)
	}
	if updated.LastError == nil {
		t.Fatal("the failure must be recorded")
	}

	// Nothing about the reports breaks: the stored rate still answers.
	rate, err := h.fxService().RateOn(h.ctx, "EUR", "UAH", fixedNow)
	if err != nil || rate == nil {
		t.Fatalf("the previous rate must still be readable: %v", err)
	}
}

func TestPlausibility_RejectsImplausibleMove(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51"})
	if _, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH")); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// A tenfold jump is a broken response, not a currency event.
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "510"})
	h.primary.asOf = fixedNow.AddDate(0, 0, 1)
	updated, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if updated.LastError == nil {
		t.Fatal("an implausible move must be flagged")
	}
	if updated.LastValue == nil || *updated.LastValue != "51" {
		t.Fatalf("last value = %+v, want the previous 51 kept", updated.LastValue)
	}

	rate, err := h.fxService().RateOn(h.ctx, "EUR", "UAH", fixedNow.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if rate == nil || fx.FormatRate(rate.Rate) != "51" {
		t.Fatalf("stored rate = %v, want the previous 51 — the jump must not be written", rate)
	}
}

func TestPlausibility_AcceptsNormalMove(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51"})
	if _, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH")); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Two percent is an ordinary day.
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "52.02"})
	h.primary.asOf = fixedNow.AddDate(0, 0, 1)
	updated, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH"))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if updated.LastValue == nil || *updated.LastValue != "52.02" {
		t.Fatalf("last value = %+v, want 52.02 accepted", updated.Effective())
	}
	if updated.LastError != nil {
		t.Errorf("last error = %q, want none", *updated.LastError)
	}
}

func TestSetting_ManualOverridesAuto(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51.08"})
	if _, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH")); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	manual := "51.00"
	updated, err := h.settings.Update(h.ctx, h.userID, setting.FXKey("UAH"),
		setting.Input{ManualValue: &manual})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := updated.Effective(); got == nil || *got != "51.00" {
		t.Fatalf("effective = %+v, want the manual 51.00", got)
	}
	// Both stay visible: "auto suggested 51.08 — you set 51.00".
	if updated.LastValue == nil || *updated.LastValue != "51.08" {
		t.Fatalf("last value = %+v, want the provider's 51.08 still visible", updated.LastValue)
	}
	if !updated.Overridden() {
		t.Error("the setting must report itself as overridden")
	}
}

func TestSetting_ResetRestoresAuto(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51.08"})
	if _, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH")); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	manual := "51.00"
	if _, err := h.settings.Update(h.ctx, h.userID, setting.FXKey("UAH"),
		setting.Input{ManualValue: &manual}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	reset, err := h.settings.Update(h.ctx, h.userID, setting.FXKey("UAH"),
		setting.Input{ClearManual: true})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got := reset.Effective(); got == nil || *got != "51.08" {
		t.Fatalf("effective = %+v, want the provider's 51.08 back", got)
	}
	if reset.Overridden() {
		t.Error("the override is gone")
	}
}

func TestSetting_RefreshingAManualSettingIsRefused(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	// price.XAU is manual pending the gold quantity; there is nothing to fetch.
	_, err := h.settings.Refresh(h.ctx, h.userID, setting.KeyPriceXAU)
	if !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("error = %v, want a validation error explaining there is no provider", err)
	}
}

func TestThreshold_ChangeAffectsStateNoRestart(t *testing.T) {
	h := newSettingHarness(t, 0.15)

	planned := money.New(10000, "EUR")
	actual := money.New(8500, "EUR") // 85% of plan

	warn := h.settings.Float(h.ctx, h.userID, setting.KeyWarnPercent, 10)
	over := h.settings.Float(h.ctx, h.userID, setting.KeyOverMultiplier, 2)
	if got := budget.Classify(&actual, &planned, budget.Thresholds{
		WarnPercent: warn, OverMultiplier: over,
	}); got != budget.StateWithin {
		t.Fatalf("state = %q at a 10%% band, want within", got)
	}

	// Widen the amber band. No restart, no redeploy: the next read sees it.
	wider := "20"
	if _, err := h.settings.Update(h.ctx, h.userID, setting.KeyWarnPercent,
		setting.Input{ManualValue: &wider}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	warn = h.settings.Float(h.ctx, h.userID, setting.KeyWarnPercent, 10)
	if got := budget.Classify(&actual, &planned, budget.Thresholds{
		WarnPercent: warn, OverMultiplier: over,
	}); got != budget.StateApproaching {
		t.Fatalf("state = %q at a 20%% band, want approaching", got)
	}
}

func TestSetting_ChangeIsAudited(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	value := "15"
	if _, err := h.settings.Update(h.ctx, h.userID, setting.KeyWarnPercent,
		setting.Input{ManualValue: &value}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	var (
		entity string
		before string
		after  string
	)
	if err := h.db.QueryRow(`
		SELECT entity, COALESCE(before_json,''), COALESCE(after_json,'')
		FROM audit_log WHERE user_id = ? ORDER BY id DESC LIMIT 1`, h.userID).
		Scan(&entity, &before, &after); err != nil {
		t.Fatalf("reading the audit log: %v", err)
	}
	if entity != "setting" {
		t.Fatalf("entity = %q, want setting", entity)
	}
	if before == after {
		t.Fatalf("the audit entry records no change: %q -> %q", before, after)
	}
}

func TestScheduler_BootCatchUp(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51.08"})

	// Nothing has ever been fetched, so every auto setting is due.
	refreshed, err := h.settings.RefreshDue(h.ctx)
	if err != nil {
		t.Fatalf("RefreshDue: %v", err)
	}
	if refreshed == 0 {
		t.Fatal("a restart must not skip a day; the boot sweep refreshes what is stale")
	}

	got, err := h.settings.Get(h.ctx, h.userID, setting.FXKey("UAH"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastFetchedAt == nil {
		t.Fatal("the setting was not refreshed")
	}
	if got.IsStale(fixedNow) {
		t.Error("a just-fetched setting must not read as stale")
	}
}

func TestScheduler_Idempotent(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51.08"})

	for i := 0; i < 3; i++ {
		if _, err := h.settings.Refresh(h.ctx, h.userID, setting.FXKey("UAH")); err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
	}

	var rows int
	if err := h.db.QueryRow(`
		SELECT count(*) FROM fx_rate
		WHERE base = 'EUR' AND quote = 'UAH' AND source = 'open-er-api' AND as_of_date = ?`,
		fixedNow.Format("2006-01-02")).Scan(&rows); err != nil {
		t.Fatalf("counting rates: %v", err)
	}
	if rows != 1 {
		t.Fatalf("stored %d rows for one day and one source, want 1", rows)
	}
}

// TestScheduler_WeekendGapNotAnError checks that a missing day is ordinary. FX
// providers do not publish at weekends, and the nearest-earlier lookup covers it.
func TestScheduler_WeekendGapNotAnError(t *testing.T) {
	h := newSettingHarness(t, 0.15)
	h.primary.rates = rats(t, map[string]string{"USD": "1.1", "HUF": "360", "UAH": "51"})
	friday := fixedNow

	result, err := h.refresher.Refresh(h.ctx, friday)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.Provider == "" {
		t.Fatal("the primary answered and must be recorded")
	}

	// Monday: the provider still reports Friday's date. Nothing new is stored and
	// nothing is wrong.
	monday := friday.AddDate(0, 0, 3)
	if _, err := h.refresher.Refresh(h.ctx, monday); err != nil {
		t.Fatalf("Refresh over the weekend gap: %v", err)
	}
	rate, err := h.fxService().RateOn(h.ctx, "EUR", "UAH", monday)
	if err != nil || rate == nil {
		t.Fatalf("Monday must still find Friday's rate: %v", err)
	}
	if rate.AsOf.After(monday) {
		t.Fatalf("as-of = %s, want Friday's date or earlier", rate.AsOf)
	}
}

func TestNoBrokerProviderKind(t *testing.T) {
	h := newHarness(t)
	// Trading212 was dropped, not deferred. The CHECK keeps it dropped.
	_, err := h.db.Exec(
		`INSERT INTO provider (key, kind, endpoint) VALUES ('trading212', 'broker', 'https://example.test')`)
	if err == nil {
		t.Fatal("kind='broker' must be rejected by the schema")
	}
}

func TestProvider_TableSeeded(t *testing.T) {
	h := newHarness(t)
	repo := NewSettingRepo(h.db)

	providers, err := repo.ListProviders(h.ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	byKey := map[string]setting.Provider{}
	for _, p := range providers {
		byKey[p.Key] = p
	}
	for _, key := range []string{"open-er-api", "fawazahmed0", "fawazahmed0-metal", "fawazahmed0-crypto"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("provider %q is not seeded", key)
		}
	}
	// Frankfurter/ECB is deliberately absent: 30 currencies and no UAH.
	if _, ok := byKey["frankfurter"]; ok {
		t.Error("frankfurter must not be seeded; it does not publish UAH")
	}
}

// TestProviderFixturesParse keeps the recorded fixtures honest against the
// parsers they exist to drive.
func TestProviderFixturesParse(t *testing.T) {
	for _, name := range []string{"open-er-api-eur.json", "fawazahmed0-eur.json"} {
		body, err := os.ReadFile("../../testdata/providers/" + name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		var perr error
		if name == "open-er-api-eur.json" {
			_, _, perr = provider.ParseOpenErAPI(body, "EUR")
		} else {
			_, _, perr = provider.ParseFawazahmed0(body, "EUR")
		}
		if perr != nil {
			t.Errorf("%s no longer parses: %v", name, perr)
		}
	}
}
