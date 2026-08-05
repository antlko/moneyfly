package api

import (
	"time"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/fx"
	"moneyfly/internal/money"
)

// RateDTO is one dated rate.
//
// `rate` is a decimal *string*, not a number. JSON numbers are IEEE 754 doubles
// in every browser, and a rate is multiplied into every figure derived from it —
// sending it as text is what lets the client parse it exactly.
type RateDTO struct {
	AsOf   string `json:"asOf"`
	Base   string `json:"base"`
	Quote  string `json:"quote"`
	Rate   string `json:"rate"`
	Source string `json:"source"`
	// AgeDays is how stale this rate is, in days. Non-zero is normal — rates do
	// not move at weekends — but a large value is how a dead provider becomes
	// visible instead of silently freezing the totals.
	AgeDays int `json:"ageDays"`
}

// CurrencyDTO is one currency's formatting facts.
type CurrencyDTO struct {
	Code     string `json:"code"`
	Exponent int    `json:"exponent"`
}

func rateDTO(r fx.Rate, today time.Time) RateDTO {
	return RateDTO{
		AsOf:    r.AsOf.Format(fx.DateLayout),
		Base:    r.Base,
		Quote:   r.Quote,
		Rate:    fx.FormatRate(r.Rate),
		Source:  r.Source,
		AgeDays: int(today.Sub(r.AsOf.UTC().Truncate(24*time.Hour)).Hours() / 24),
	}
}

// handleLatestRates returns the most recent rate for every quote currency.
//
// This is what a client pulls to fill its local cache: one request, everything
// it needs to convert today's screen, and it keeps working from that cache with
// no network afterwards.
func (s *Server) handleLatestRates(c fiber.Ctx) error {
	rates, err := s.rates().Latest(c.Context())
	if err != nil {
		return err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	out := make([]RateDTO, 0, len(rates))
	for _, r := range rates {
		out = append(out, rateDTO(r, today))
	}
	return c.JSON(fiber.Map{"base": fx.StorageBase, "rates": out})
}

// handleRateHistory returns the stored history for one pair.
//
// A client asks for this when it needs to price something that is not from
// today — an expense entered last month has to use last month's rate, or its
// value changes every time the page is opened.
func (s *Server) handleRateHistory(c fiber.Ctx) error {
	quote := c.Query("quote")
	if len(quote) != 3 {
		return fiber.NewError(fiber.StatusBadRequest, "quote: want a 3-letter currency code")
	}
	base := c.Query("base", fx.StorageBase)

	to, err := queryDate(c, "to", time.Now().UTC())
	if err != nil {
		return err
	}
	// Ninety days back by default: enough to cover the visible history on any
	// screen without pulling years of rows a phone will never look at.
	from, err := queryDate(c, "from", to.AddDate(0, 0, -90))
	if err != nil {
		return err
	}

	rates, err := s.rates().History(c.Context(), base, quote, from, to)
	if err != nil {
		return err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	out := make([]RateDTO, 0, len(rates))
	for _, r := range rates {
		out = append(out, rateDTO(r, today))
	}
	return c.JSON(fiber.Map{"rates": out})
}

// setRateRequest is one hand-entered rate: "1 EUR is worth `rate` `quote`".
type setRateRequest struct {
	Quote string `json:"quote"`
	Rate  string `json:"rate"`
	// AsOf defaults to today. It is settable so a rate can be recorded against
	// the day it actually applied — entering last week's rate today must not
	// re-price last week at today's value.
	AsOf string `json:"asOf"`
}

// handleSetRate stores a rate a person typed in.
//
// Only EUR-based rates are accepted, exactly as for a provider: the inverse is
// computed and never stored, which is what makes it impossible to hold
// EUR→USD 1.14 and USD→EUR 0.88 at the same time (docs/ARCHITECTURE.md).
//
// It overrides the provider for that date by ordinary means rather than by
// privilege — same date, different source, written later, and RateOn takes the
// most recent row for the date. A provider rate published on a *later* day then
// takes over again, which is correct: this records what a rate was on a day, not
// a permanent preference. For a currency no provider publishes, no later rate
// ever arrives and the entered one keeps applying.
func (s *Server) handleSetRate(c fiber.Ctx) error {
	var in setRateRequest
	if err := decode(c, &in); err != nil {
		return err
	}
	if len(in.Quote) != 3 {
		return fiber.NewError(fiber.StatusBadRequest, "quote: want a 3-letter currency code")
	}
	rate, err := fx.ParseRate(in.Rate)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "rate: want a decimal number, e.g. 391.5")
	}
	if rate.Sign() <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "rate: must be greater than zero")
	}
	asOf := time.Now().UTC()
	if in.AsOf != "" {
		if asOf, err = time.Parse(fx.DateLayout, in.AsOf); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "asOf: want YYYY-MM-DD")
		}
	}

	stored, err := s.rates().Upsert(c.Context(), fx.Rate{
		AsOf:      asOf.UTC().Truncate(24 * time.Hour),
		Base:      fx.StorageBase,
		Quote:     in.Quote,
		Rate:      rate,
		Source:    fx.SourceManual,
		FetchedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return c.JSON(rateDTO(stored, time.Now().UTC().Truncate(24*time.Hour)))
}

// handleCurrencies returns the exponent table.
//
// The client ships its own copy so it can format offline from the first paint;
// this endpoint is how it notices the two have drifted apart after a server
// upgrade.
func (s *Server) handleCurrencies(c fiber.Ctx) error {
	out := make([]CurrencyDTO, 0, len(money.Exponents))
	for code, exp := range money.Exponents {
		out = append(out, CurrencyDTO{Code: code, Exponent: exp})
	}
	return c.JSON(fiber.Map{
		"default":    money.DefaultExponent,
		"currencies": out,
	})
}

func queryDate(c fiber.Ctx, key string, fallback time.Time) (time.Time, error) {
	raw := c.Query(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.Parse(fx.DateLayout, raw)
	if err != nil {
		return time.Time{}, fiber.NewError(fiber.StatusBadRequest, key+": want YYYY-MM-DD")
	}
	return parsed.UTC(), nil
}
