// Package setting is the one mechanism behind every configurable number: rates,
// prices, thresholds and periods (docs/06-fx-and-providers.md §6.4).
//
// A setting is a value plus how it is maintained. `manual` always wins over
// `auto`, and both stay visible — "auto suggested 51.08, you set 51.00" is more
// useful than either number on its own, and it is the only way to tell a
// deliberate override from a stale fetch.
package setting

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

// Mode says how a setting's value is maintained.
type Mode string

// The two modes.
const (
	// ModeManual: the user typed it, and nothing overwrites it.
	ModeManual Mode = "manual"
	// ModeAuto: a provider maintains it. A manual value may still sit on top.
	ModeAuto Mode = "auto"
)

// Valid reports whether the mode is one of the two.
func (m Mode) Valid() bool { return m == ModeManual || m == ModeAuto }

// The setting keys this application knows about. They are strings rather than an
// enum because a user may add their own, but these are the ones seeded and
// referenced by code.
const (
	KeyWarnPercent    = "threshold.warn_percent"
	KeyOverMultiplier = "threshold.over_multiplier"
	KeyBaseCurrency   = "report.base_currency"
	KeyFiscalYear     = "report.fiscal_year_start"
	KeyValuation      = "report.valuation"
	KeyBurnMode       = "report.burn_mode"
	KeyPriceXAU       = "price.XAU"
	KeyPriceUSDT      = "price.USDT"
)

// FXKey is the setting key for one EUR-based rate.
func FXKey(quote string) string { return "fx.EUR_" + strings.ToUpper(quote) }

// QuoteOf returns the currency an fx.* key refers to, or "" for other keys.
func QuoteOf(key string) string {
	const prefix = "fx.EUR_"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return key[len(prefix):]
}

// DefaultRefresh is how often an auto setting is re-fetched when it does not say.
const DefaultRefresh = 24 * time.Hour

// Setting is one configurable value.
type Setting struct {
	ID     int64
	UserID int64
	Key    string
	Mode   Mode
	// ManualValue is the user's own figure. It wins whenever it is present, in
	// either mode: switching to auto does not silently discard what was typed.
	ManualValue *string
	ProviderKey *string
	// LastValue is what the provider last returned, kept even when a manual
	// value overrides it so the UI can show both.
	LastValue       *string
	LastError       *string
	LastFetchedAt   *time.Time
	RefreshInterval time.Duration
	UpdatedAt       time.Time
}

// Effective is the value in force: the manual figure when there is one,
// otherwise the provider's.
func (s Setting) Effective() *string {
	if s.ManualValue != nil && *s.ManualValue != "" {
		return s.ManualValue
	}
	return s.LastValue
}

// Overridden reports whether a manual value is sitting on top of a provider one,
// which is what the UI renders as "auto suggested X — you set Y".
func (s Setting) Overridden() bool {
	return s.Mode == ModeAuto && s.ManualValue != nil && *s.ManualValue != "" && s.LastValue != nil
}

// IsStale reports whether an auto setting has missed its refresh window.
//
// A manual setting is never stale: nobody promised to update it.
func (s Setting) IsStale(now time.Time) bool {
	if s.Mode != ModeAuto {
		return false
	}
	if s.LastFetchedAt == nil {
		return true
	}
	interval := s.RefreshInterval
	if interval <= 0 {
		interval = DefaultRefresh
	}
	// Twice the interval, so an FX provider skipping a weekend is not stale.
	// Weekend and holiday gaps are normal and are not errors.
	return now.Sub(*s.LastFetchedAt) > 2*interval
}

// Input is the update payload.
type Input struct {
	Mode            *Mode
	ManualValue     *string
	ProviderKey     *string
	RefreshInterval *time.Duration
	// ClearManual resets an override back to the provider's value. It is
	// separate from ManualValue: sending null and sending nothing are different
	// requests.
	ClearManual bool
}

// Repo is the storage contract.
type Repo interface {
	List(ctx context.Context, userID int64) ([]Setting, error)
	Get(ctx context.Context, userID int64, key string) (Setting, error)
	Upsert(ctx context.Context, s Setting, now time.Time) (Setting, error)
	// RecordFetch stores the outcome of a provider call without touching the
	// user's own fields.
	RecordFetch(ctx context.Context, userID int64, key string, value, errMessage *string, at time.Time) error
	// DueForRefresh returns every auto setting whose interval has elapsed.
	DueForRefresh(ctx context.Context, now time.Time) ([]Setting, error)
	Audit(ctx context.Context, userID int64, entry AuditEntry, now time.Time) error
}

// AuditEntry records a change for the audit log.
type AuditEntry struct {
	ActorUserID *int64
	Entity      string
	EntityID    *int64
	Action      string
	Before      string
	After       string
}

// Fetcher resolves one auto setting's current value from its provider. It is an
// interface so this package stays free of HTTP.
type Fetcher interface {
	// Fetch returns the provider's value. An error is recorded and the previous
	// value kept: an outage costs freshness, never the number.
	Fetch(ctx context.Context, userID int64, s Setting) (string, error)
}

// ProviderLister exposes the configured providers to the settings screen.
type ProviderLister interface {
	ListProviders(ctx context.Context) ([]Provider, error)
}

// Provider is one configured rate source.
type Provider struct {
	Key      string
	Kind     string
	Endpoint string
	Priority int
	Enabled  bool
}

// Service is the settings use-case layer.
type Service struct {
	repo      Repo
	fetcher   Fetcher
	providers ProviderLister
	clock     clock.Clock
}

// NewService builds the settings service. fetcher may be nil, in which case a
// refresh reports that nothing is configured rather than pretending to work.
func NewService(repo Repo, fetcher Fetcher, providers ProviderLister, clk clock.Clock) *Service {
	return &Service{repo: repo, fetcher: fetcher, providers: providers, clock: clk}
}

// ListProviders returns the configured rate sources.
func (s *Service) ListProviders(ctx context.Context) ([]Provider, error) {
	if s.providers == nil {
		return []Provider{}, nil
	}
	return s.providers.ListProviders(ctx)
}

// Refresh forces a fetch for one setting.
//
// A provider failure is not an error to the caller: the last value stands, the
// message is recorded, and the setting reads as stale. That is the whole point of
// the chain — nothing the user is looking at breaks because a free API is down.
func (s *Service) Refresh(ctx context.Context, userID int64, key string) (*Setting, error) {
	found, err := s.repo.Get(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if found.Mode != ModeAuto {
		return nil, apperr.Validation("mode",
			"%q is manual; there is no provider to refresh from", key)
	}
	if s.fetcher == nil {
		return nil, apperr.Validation("provider_key", "no provider is configured in this build")
	}

	now := s.clock.Now()
	value, fetchErr := s.fetcher.Fetch(ctx, userID, found)
	if fetchErr != nil {
		message := fetchErr.Error()
		if err := s.repo.RecordFetch(ctx, userID, key, nil, &message, now); err != nil {
			return nil, err
		}
	} else if err := s.repo.RecordFetch(ctx, userID, key, &value, nil, now); err != nil {
		return nil, err
	}

	updated, err := s.repo.Get(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// RefreshDue refreshes every auto setting whose interval has elapsed. The
// scheduler calls it; it returns the number refreshed and never fails the run for
// one bad provider.
func (s *Service) RefreshDue(ctx context.Context) (int, error) {
	due, err := s.repo.DueForRefresh(ctx, s.clock.Now())
	if err != nil {
		return 0, err
	}
	refreshed := 0
	for _, v := range due {
		if _, err := s.Refresh(ctx, v.UserID, v.Key); err != nil {
			continue
		}
		refreshed++
	}
	return refreshed, nil
}

// List returns every setting for a user.
func (s *Service) List(ctx context.Context, userID int64) ([]Setting, error) {
	return s.repo.List(ctx, userID)
}

// Get returns one setting.
func (s *Service) Get(ctx context.Context, userID int64, key string) (Setting, error) {
	return s.repo.Get(ctx, userID, key)
}

// Effective returns the value in force, or nil when the setting has none.
func (s *Service) Effective(ctx context.Context, userID int64, key string) (*string, error) {
	found, err := s.repo.Get(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	return found.Effective(), nil
}

// Float reads a numeric setting, falling back to the supplied default when it is
// absent or unparseable. Reports must render even when a setting is nonsense.
func (s *Service) Float(ctx context.Context, userID int64, key string, fallback float64) float64 {
	value, err := s.Effective(ctx, userID, key)
	if err != nil || value == nil {
		return fallback
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(*value), 64)
	if err != nil {
		return fallback
	}
	return parsed
}

// String reads a text setting with a fallback.
func (s *Service) String(ctx context.Context, userID int64, key, fallback string) string {
	value, err := s.Effective(ctx, userID, key)
	if err != nil || value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return strings.TrimSpace(*value)
}

// Update changes a setting and records who did it.
func (s *Service) Update(ctx context.Context, userID int64, key string, in Input) (Setting, error) {
	existing, err := s.repo.Get(ctx, userID, key)
	if err != nil {
		return Setting{}, err
	}
	before := describe(existing)

	if in.Mode != nil {
		if !in.Mode.Valid() {
			return Setting{}, apperr.Validation("mode", "must be manual or auto, got %q", *in.Mode)
		}
		existing.Mode = *in.Mode
	}
	if in.ClearManual {
		existing.ManualValue = nil
	} else if in.ManualValue != nil {
		value := strings.TrimSpace(*in.ManualValue)
		existing.ManualValue = &value
	}
	if in.ProviderKey != nil {
		existing.ProviderKey = in.ProviderKey
	}
	if in.RefreshInterval != nil {
		existing.RefreshInterval = *in.RefreshInterval
	}
	if existing.Mode == ModeAuto && existing.ProviderKey == nil {
		return Setting{}, apperr.Validation("provider_key",
			"an auto setting needs a provider; nothing would maintain it otherwise")
	}

	saved, err := s.repo.Upsert(ctx, existing, s.clock.Now())
	if err != nil {
		return Setting{}, err
	}
	if err := s.repo.Audit(ctx, userID, AuditEntry{
		ActorUserID: &userID, Entity: "setting", EntityID: &saved.ID,
		Action: "update", Before: before, After: describe(saved),
	}, s.clock.Now()); err != nil {
		return Setting{}, err
	}
	return saved, nil
}

// IsStale exposes the staleness rule to callers that already hold a Setting.
func (s *Service) IsStale(v Setting) bool { return v.IsStale(s.clock.Now()) }

func describe(v Setting) string {
	parts := []string{"mode=" + string(v.Mode)}
	if v.ManualValue != nil {
		parts = append(parts, "manual="+*v.ManualValue)
	}
	if v.LastValue != nil {
		parts = append(parts, "auto="+*v.LastValue)
	}
	if v.ProviderKey != nil {
		parts = append(parts, "provider="+*v.ProviderKey)
	}
	return strings.Join(parts, " ")
}
