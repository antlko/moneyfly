package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/db"
	"moneyfly/internal/exporter"
	"moneyfly/internal/fx"
	"moneyfly/internal/money"
)

// handleExportCSV streams every live transaction as CSV in the requested
// profile ("native", the default, or "monefy" — see internal/exporter).
//
// Auth here is either a session cookie or a Bearer token: a plain link works
// from a signed-in browser, and the same URL works from a script carrying an
// API token, which is the whole reason that second auth path exists.
func (s *Server) handleExportCSV(c fiber.Ctx) error {
	profileID := c.Query("profile", "native")
	profile, ok := exporter.Get(profileID)
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest,
			fmt.Sprintf("unknown profile %q, want one of %v", profileID, exporter.IDs()))
	}

	user := userLocal(c)
	txns, err := s.conn().TxnsForExport(user.ID)
	if err != nil {
		return err
	}
	categories, err := s.conn().CategoryNames(user.ID)
	if err != nil {
		return err
	}
	accounts, err := s.conn().AccountNames(user.ID)
	if err != nil {
		return err
	}
	catNames, accNames := namesByID(categories), namesByID(accounts)

	convert := s.baseConverter(c, user.BaseCurrency)
	unconverted := 0
	rows := make([]exporter.Row, 0, len(txns))
	for _, t := range txns {
		row := exporter.Row{
			OccurredOn:   t.OccurredOn,
			AccountName:  accNames[t.AccountID],
			CategoryName: catNames[t.CategoryID],
			AmountMinor:  t.AmountMinor,
			Currency:     t.Currency,
			Note:         t.Note,
			Transfer:     t.Kind == "transfer",
		}
		if minor, ok := convert(t.AmountMinor, t.Currency, t.OccurredOn); ok {
			row.ConvertedMinor, row.ConvertedCurrency = minor, user.BaseCurrency
		} else {
			unconverted++
		}
		rows = append(rows, row)
	}
	// A row with no rate keeps its own currency in the converted columns. Said
	// in a header too, so a script can tell "every figure is in the base
	// currency" from "most of them are".
	c.Set("X-Moneyfly-Unconverted", fmt.Sprint(unconverted))

	var buf bytes.Buffer
	if err := profile.Write(&buf, rows); err != nil {
		return err
	}

	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="moneyfly-%s-%s.csv"`, profileID, time.Now().Format("2006-01-02")))
	return c.Send(buf.Bytes())
}

// baseConverter prices an amount in the base currency at the rate on its own
// day — the nearest earlier rate, never a later one, the same lookup the
// dashboard uses — memoised per currency and day, because an export is
// thousands of rows over a few hundred distinct (currency, day) pairs.
func (s *Server) baseConverter(c fiber.Ctx, base string) func(minor int64, currency, day string) (int64, bool) {
	rates := s.rates()
	type key struct{ currency, day string }
	cache := map[key]*big.Rat{}
	return func(minor int64, currency, day string) (int64, bool) {
		if currency == base {
			return minor, true
		}
		k := key{currency, day}
		rate, seen := cache[k]
		if !seen {
			if on, err := time.Parse("2006-01-02", day); err == nil {
				r, err := rates.RateOn(c.Context(), currency, base, on)
				if err != nil {
					slog.WarnContext(c.Context(), "export: rate lookup", "pair", currency+base, "day", day, "error", err)
				} else if r != nil {
					rate = r.Rate
				}
			}
			cache[k] = rate
		}
		if rate == nil {
			return 0, false
		}
		return fx.ConvertMinor(minor, rate, money.Exponent(currency), money.Exponent(base)), true
	}
}

func namesByID(names []db.NameKind) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n.ID] = n.Name
	}
	return out
}
