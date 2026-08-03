package rest

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func (s *Server) registerFXRoutes(api fiber.Router) {
	g := api.Group("/fx", s.requireSession(), s.requirePasswordChanged())
	g.Get("/rates", s.handleFxRates)
	g.Get("/rates/latest", s.handleFxLatest)
}

// FxRateDTO is a rate on the wire. The rate itself is a decimal string, never a
// float: it is stored exactly and parsed into a rational.
type FxRateDTO struct {
	ID        int64  `json:"id"`
	AsOfDate  string `json:"as_of_date"`
	Base      string `json:"base"`
	Quote     string `json:"quote"`
	Rate      string `json:"rate"`
	Source    string `json:"source"`
	FetchedAt string `json:"fetched_at"`
}

func toFxRateDTO(r fx.Rate) FxRateDTO {
	return FxRateDTO{
		ID: r.ID, AsOfDate: r.AsOf.Format(dateLayout), Base: r.Base, Quote: r.Quote,
		Rate: fx.FormatRate(r.Rate), Source: r.Source, FetchedAt: r.FetchedAt.Format(time.RFC3339),
	}
}

func (s *Server) handleFxRates(c *fiber.Ctx) error {
	base := c.Query("base", fx.StorageBase)
	quote := c.Query("quote")
	if quote == "" {
		return apperr.Validation("quote", "is required")
	}
	from := s.deps.Clock.Now().AddDate(0, -1, 0)
	to := s.deps.Clock.Now()
	if v := c.Query("from"); v != "" {
		parsed, err := parseDate("from", v)
		if err != nil {
			return err
		}
		from = parsed
	}
	if v := c.Query("to"); v != "" {
		parsed, err := parseDate("to", v)
		if err != nil {
			return err
		}
		to = parsed
	}
	rates, err := s.deps.FX.History(c.UserContext(), base, quote, from, to)
	if err != nil {
		return err
	}
	out := make([]FxRateDTO, 0, len(rates))
	for _, r := range rates {
		out = append(out, toFxRateDTO(r))
	}
	return c.JSON(out)
}

func (s *Server) handleFxLatest(c *fiber.Ctx) error {
	rates, err := s.deps.FX.Latest(c.UserContext())
	if err != nil {
		return err
	}
	now := s.deps.Clock.Now()
	out := make([]fiber.Map, 0, len(rates))
	for _, r := range rates {
		dto := toFxRateDTO(r)
		out = append(out, fiber.Map{
			"rate": dto,
			// Age is what the UI badges as staleness. FX markets close, so a
			// weekend gap is normal rather than an error.
			"age_days": int(now.Sub(r.AsOf).Hours() / 24),
		})
	}
	return c.JSON(out)
}
