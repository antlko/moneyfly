package fx

import (
	"context"
	"fmt"
	"strings"

	"github.com/antlko/moneyapp/internal/domain/setting"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

// SettingFetcher resolves an `fx.EUR_X` or `price.X` setting from the provider
// chain and, for rates, writes the result into the dated history.
//
// One fetch fills the whole table — the providers return every currency in one
// document — so refreshing three rate settings costs one HTTP call, not three.
type SettingFetcher struct {
	refresher *Refresher
	service   *Service
	clock     clock.Clock
}

// NewSettingFetcher builds the fetcher.
func NewSettingFetcher(refresher *Refresher, service *Service, clk clock.Clock) *SettingFetcher {
	return &SettingFetcher{refresher: refresher, service: service, clock: clk}
}

// Fetch implements setting.Fetcher.
func (f *SettingFetcher) Fetch(ctx context.Context, _ int64, s setting.Setting) (string, error) {
	quote := setting.QuoteOf(s.Key)
	if quote == "" {
		quote = priceTicker(s.Key)
	}
	if quote == "" {
		return "", fmt.Errorf("fx: %q is not a rate or price setting", s.Key)
	}

	now := f.clock.Now()
	result, err := f.refresher.Refresh(ctx, now)
	if err != nil {
		return "", err
	}
	if result.Provider == "" {
		// Every provider failed. Say so plainly; the caller keeps the last value
		// and badges it stale.
		if len(result.Errors) > 0 {
			return "", fmt.Errorf("fx: no provider answered: %s", strings.Join(result.Errors, "; "))
		}
		return "", fmt.Errorf("fx: no provider answered")
	}
	for _, rejected := range result.Rejected {
		if rejected.Quote == quote {
			return "", fmt.Errorf(
				"fx: %s moved from %s to %s (%.0f%%), which is beyond the plausibility limit; "+
					"the previous rate is kept",
				quote, rejected.Previous, rejected.Proposed, rejected.Change*100)
		}
	}

	// Read at the date the refresh actually stored, not at "now": a provider may
	// publish a day ahead or a day behind, and nearest-earlier lookup from the
	// wrong date would return yesterday's figure as though it were today's.
	asOf := result.AsOf
	if asOf.IsZero() {
		asOf = now
	}
	rate, err := f.service.RateOn(ctx, StorageBase, quote, asOf)
	if err != nil {
		return "", err
	}
	if rate == nil {
		return "", fmt.Errorf("fx: no %s rate was stored", quote)
	}
	return FormatRate(rate.Rate), nil
}

// priceTicker returns the ticker a `price.X` setting refers to.
func priceTicker(key string) string {
	const prefix = "price."
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return strings.ToUpper(key[len(prefix):])
}

// RefreshJob is the scheduler's daily work: refresh every due setting.
//
// The scheduler is the only network caller in the process, and a failure here is
// logged rather than raised — the last stored rate still answers every query.
func RefreshJob(settings *setting.Service) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := settings.RefreshDue(ctx)
		return err
	}
}

// Quotes are the currencies the refresher stores. It is the currency table's
// contents minus the storage base; passing them explicitly keeps a provider's 338
// tickers from filling the history with rates nobody asked for.
func Quotes(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		if up(code) == StorageBase {
			continue
		}
		out = append(out, up(code))
	}
	return out
}
