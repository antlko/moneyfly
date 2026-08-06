package api

import (
	"bytes"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/db"
	"moneyfly/internal/exporter"
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

	rows := make([]exporter.Row, 0, len(txns))
	for _, t := range txns {
		rows = append(rows, exporter.Row{
			OccurredOn:   t.OccurredOn,
			AccountName:  accNames[t.AccountID],
			CategoryName: catNames[t.CategoryID],
			AmountMinor:  t.AmountMinor,
			Currency:     t.Currency,
			Note:         t.Note,
		})
	}

	var buf bytes.Buffer
	if err := profile.Write(&buf, rows); err != nil {
		return err
	}

	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="moneyfly-%s-%s.csv"`, profileID, time.Now().Format("2006-01-02")))
	return c.Send(buf.Bytes())
}

func namesByID(names []db.NameKind) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n.ID] = n.Name
	}
	return out
}
