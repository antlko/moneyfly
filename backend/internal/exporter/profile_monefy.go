package exporter

import (
	"encoding/csv"
	"io"
	"time"

	"moneyfly/internal/money"
)

func init() { register(monefyProfile{}) }

// monefyProfile matches the exact positional shape docs/MONEFY-PARITY.md §5
// verified against a real export — date, account, category, amount,
// currency, converted amount, converted currency, description — so a file
// this writes can be read back by this app's own importer, or opened in
// Monefy itself.
//
// The converted-amount pair is filled with the same value and currency as
// the native pair rather than left empty or actually converted: this app has
// no "the base currency on the day of export" concept the way a phone with
// one always-current base does, and internal/importer already discards that
// pair on read, so there is nothing a real conversion here would be used for.
type monefyProfile struct{}

func (monefyProfile) ID() string { return "monefy" }

func (monefyProfile) Write(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"date", "account", "category", "amount", "currency", "converted amount", "currency", "description",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		on, err := time.Parse("2006-01-02", r.OccurredOn)
		if err != nil {
			continue // a row with an unparseable date was already broken before this
		}
		amount := money.New(r.AmountMinor, r.Currency).String(money.Exponent(r.Currency))
		if err := cw.Write([]string{
			on.Format("02.01.2006"), r.AccountName, r.CategoryName,
			amount, r.Currency, amount, r.Currency, r.Note,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
