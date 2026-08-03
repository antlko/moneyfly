package seed

import (
	"time"

	"github.com/antlko/moneyapp/internal/domain/setting"
)

// SettingSeed is one setting a new user starts with.
type SettingSeed struct {
	Key         string
	Mode        setting.Mode
	ManualValue string
	ProviderKey string
	Refresh     time.Duration
}

// The provider keys seeded by migration 0008.
const (
	providerFX     = "open-er-api"
	providerMetal  = "fawazahmed0-metal"
	providerCrypto = "fawazahmed0-crypto"
)

// Settings returns the settings installed for a new user.
//
// The thresholds come from the workbook's own settings block: `C2` = 10 for the
// amber band, and the 2x multiplier that was hardcoded in a conditional format.
// They live here rather than in config so they can be changed without a restart
// (docs/implementation-plan/07-fx-automation.md §9).
//
// Two workbook cells are deliberately **not** migrated: `E2` (EUR/USD = 1.14) is
// referenced by no formula and contradicts `E3`, and `G2` (Month = 12) is dead.
func Settings() []SettingSeed {
	return []SettingSeed{
		{Key: setting.KeyWarnPercent, Mode: setting.ModeManual, ManualValue: "10"},
		{Key: setting.KeyOverMultiplier, Mode: setting.ModeManual, ManualValue: "2"},
		{Key: setting.KeyBaseCurrency, Mode: setting.ModeManual, ManualValue: "EUR"},
		{Key: setting.KeyFiscalYear, Mode: setting.ModeManual, ManualValue: "8"},
		{Key: setting.KeyValuation, Mode: setting.ModeManual, ManualValue: "contemporaneous"},
		{Key: setting.KeyBurnMode, Mode: setting.ModeManual, ManualValue: "actual_trailing_3"},

		// The three rates the data actually uses. Only EUR->X is stored; the
		// inverse is computed, which is what removes the sheet's 1.14 / 0.88
		// contradiction.
		{Key: setting.FXKey("USD"), Mode: setting.ModeAuto, ProviderKey: providerFX, Refresh: 24 * time.Hour},
		{Key: setting.FXKey("HUF"), Mode: setting.ModeAuto, ProviderKey: providerFX, Refresh: 24 * time.Hour},
		{Key: setting.FXKey("UAH"), Mode: setting.ModeAuto, ProviderKey: providerFX, Refresh: 24 * time.Hour},

		// TODO(anatol): the gold quantity behind `Gold = 3000` EUR was never
		// supplied. 3000 is a *value*, not a holding, and inferring roughly 0.85
		// troy ounces from a possibly stale valuation would be fabrication — so
		// this stays manual until the number arrives, at which point flipping it
		// to auto against fawazahmed0-metal is a one-field change.
		{Key: setting.KeyPriceXAU, Mode: setting.ModeManual},
		{Key: setting.KeyPriceUSDT, Mode: setting.ModeAuto, ProviderKey: providerCrypto, Refresh: 24 * time.Hour},
	}
}

// MetalProviderKey is the provider `price.XAU` will use once a quantity exists.
const MetalProviderKey = providerMetal
