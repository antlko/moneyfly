package exporter

import (
	"encoding/csv"
	"io"

	"moneyfly/internal/money"
)

func init() { register(nativeProfile{}) }

// nativeProfile is this app's own shape: one column per field, kind spelled
// out rather than left for the reader to infer from a sign, amount signed
// and formatted at the currency's own exponent — the complete row, readable
// without cross-referencing anything else.
type nativeProfile struct{}

func (nativeProfile) ID() string { return "native" }

func (nativeProfile) Write(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"date", "kind", "account", "category", "amount", "currency", "note"}); err != nil {
		return err
	}
	for _, r := range rows {
		amount := money.New(r.AmountMinor, r.Currency).String(money.Exponent(r.Currency))
		if err := cw.Write([]string{
			r.OccurredOn, r.Kind(), r.AccountName, r.CategoryName, amount, r.Currency, r.Note,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
