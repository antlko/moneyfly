package api

import (
	"context"
	"log/slog"
	"time"

	"moneyfly/internal/config"
	"moneyfly/internal/fx"
)

// buildRateProviders turns `fx.providers` into clients, in the configured order.
//
// `required` is the set of currencies the instance actually holds; a response
// that omits one of them is rejected whole and the next provider is tried. It is
// read once per refresh rather than at startup, so adding a forint account this
// afternoon is honoured tonight.
func buildRateProviders(cfg *config.Config, required []string) []fx.Provider {
	client := fx.NewClient()
	out := make([]fx.Provider, 0, len(cfg.FX.Providers))
	for _, id := range cfg.FX.Providers {
		switch id {
		case "open-er-api":
			out = append(out, fx.NewOpenErAPI(client, "", required))
		case "fawazahmed0":
			out = append(out, fx.NewFawazahmed0(client, "", required))
		}
	}
	return out
}

// rates returns the FX service over the current database.
func (s *Server) rates() *fx.Service { return fx.NewService(s.conn().Rates()) }

// minWakeInterval bounds how often a client-triggered wake-up may actually hit
// a provider. Someone adding three currencies in a row should cost one fetch,
// not three, and a device pushing in a loop must not be able to drive traffic to
// someone else's free API.
const minWakeInterval = time.Minute

// wakeFX asks the refresh loop to look again, without blocking the caller.
//
// The channel is buffered by one and the send is non-blocking, so this is safe
// to call from a request handler: at worst the signal is already pending, which
// means a look is coming anyway.
func (s *Server) wakeFX() {
	select {
	case s.fxWake <- struct{}{}:
	default:
	}
}

// fxLoop refreshes exchange rates once a day at `fx.refresh_at`, and whenever a
// client does something that might have introduced a currency.
//
// The daily schedule is deliberately not a `time.Ticker(24h)`: a ticker drifts
// against the wall clock across restarts and daylight saving, and "04:00" in the
// config would gradually stop meaning 04:00. Each iteration computes the next
// occurrence instead.
//
// A refresh on startup catches up an instance that was switched off overnight,
// but only when today's rates are actually missing — restarting the container
// five times must not hammer someone else's free API.
func (s *Server) fxLoop() {
	cfg := s.config()
	if !cfg.FX.Enabled {
		slog.Info("fx: refresh disabled")
		return
	}
	hour, minute, err := cfg.FX.RefreshHourMinute()
	if err != nil {
		// Validate() has already run; reaching here means a bug, not bad input.
		slog.Error("fx: bad refresh time", "error", err)
		return
	}

	var lastRun time.Time
	run := func() {
		s.refreshRates()
		lastRun = time.Now()
	}

	if s.ratesMissingToday() {
		run()
	}
	for {
		timer := time.NewTimer(time.Until(nextOccurrence(time.Now(), hour, minute)))
		select {
		case <-s.stop:
			timer.Stop()
			return
		case <-timer.C:
			run()
		case <-s.fxWake:
			timer.Stop()
			// Only when there is genuinely something new to fetch, and not more
			// often than minWakeInterval.
			if time.Since(lastRun) >= minWakeInterval && s.ratesMissingToday() {
				run()
			}
		}
	}
}

// nextOccurrence is the next hh:mm strictly after `from`, in local time.
func nextOccurrence(from time.Time, hour, minute int) time.Time {
	next := time.Date(from.Year(), from.Month(), from.Day(), hour, minute, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (s *Server) ratesMissingToday() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	used, err := s.conn().UsedCurrencies(ctx)
	if err != nil || len(used) == 0 {
		// Nothing but the storage base in use: there is nothing to convert, so
		// there is nothing to fetch.
		return false
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, quote := range used {
		rate, err := s.conn().Rates().RateOn(ctx, fx.StorageBase, quote, today)
		if err != nil || rate == nil || rate.AsOf.Before(today) {
			return true
		}
	}
	return false
}

// refreshRates walks the provider chain once and stores what it gets.
//
// Every outcome is logged and none is fatal. A total outage keeps yesterday's
// rates: the screens carry on, showing values that are a day old, which is what
// the `age` field on /api/fx/latest exists to make visible.
func (s *Server) refreshRates() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	used, err := s.conn().UsedCurrencies(ctx)
	if err != nil {
		slog.Error("fx: reading used currencies", "error", err)
		return
	}
	providers := buildRateProviders(s.config(), used)
	if len(providers) == 0 {
		return
	}

	result, err := fx.NewRefresher(s.rates(), providers, 0, nil).Refresh(ctx, time.Now().UTC())
	if err != nil {
		slog.Error("fx: refresh", "error", err)
		return
	}
	if result.Provider == "" {
		slog.Warn("fx: no provider answered, keeping the stored rates",
			"errors", result.Errors)
		return
	}
	slog.Info("fx: rates refreshed",
		"provider", result.Provider,
		"asOf", result.AsOf.Format(fx.DateLayout),
		"stored", result.Stored,
		"rejected", len(result.Rejected),
		"errors", result.Errors)
	for _, r := range result.Rejected {
		// Loud on purpose: a rejection means either a provider glitch or a real
		// currency event, and both are worth a human look.
		slog.Warn("fx: implausible move rejected, previous rate kept",
			"quote", r.Quote, "previous", r.Previous, "proposed", r.Proposed, "change", r.Change)
	}
}
