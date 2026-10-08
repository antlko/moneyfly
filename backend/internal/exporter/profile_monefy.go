package exporter

import (
	"bufio"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"moneyfly/internal/money"
)

func init() {
	register(monefyProfile{id: "monefy", dateLayout: "1/2/2006"})
	register(monefyProfile{id: "monefy-dmy", dateLayout: "02.01.2006"})
}

// monefyProfile writes what Monefy's own "Export to CSV" writes, byte for
// byte where this app holds the same information. docs/MONEFY-PARITY.md §5
// has the format; every rule below was checked against a real export
// (3,292 rows, 2021–2026), because people feed these files to their own
// scripts and a script written against Monefy's file must read this one:
//
//   - UTF-8 with a BOM, CRLF line endings, the positional header with
//     `currency` twice.
//   - Dates in the device's locale. Monefy has written both M/D/YYYY with no
//     leading zeros (newer exports) and DD.MM.YYYY (older ones), so both are
//     offered: "monefy" is the former, "monefy-dmy" the latter.
//   - Amounts as plain decimals with no trailing zeros and no grouping:
//     `-2694`, `-397.3`, `-12.45` — never `-2694.00`.
//   - Converted amount and currency in the user's base currency, for every
//     row. Monefy truncates that figure to three decimals using one fixed rate
//     per currency; this app rounds once at the base currency's own exponent
//     using the rate on the record's day (fx.ConvertMinor, the dashboard's own
//     rule), so the file adds up to what the app shows.
//   - Grouped by account, oldest first within each.
//   - A field is quoted only when it must be: a comma, quote or line break in
//     it — or leading or trailing whitespace, which Monefy quotes and Go's
//     encoding/csv does not.
//   - No transfers. A Monefy row is spending or income by its sign, and a
//     transfer is neither; written as a negative row it would be counted as
//     spending in a category with no name.
type monefyProfile struct {
	id         string
	dateLayout string
}

func (p monefyProfile) ID() string { return p.id }

func (p monefyProfile) Write(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString("\xEF\xBB\xBF"); err != nil {
		return err
	}
	writeMonefyRecord(bw, []string{
		"date", "account", "category", "amount", "currency", "converted amount", "currency", "description",
	})

	out := slices.DeleteFunc(slices.Clone(rows), func(r Row) bool { return r.Transfer })
	// Stable, so each account keeps the date order rows arrived in.
	slices.SortStableFunc(out, func(a, b Row) int { return strings.Compare(a.AccountName, b.AccountName) })

	for _, r := range out {
		on, err := time.Parse("2006-01-02", r.OccurredOn)
		if err != nil {
			continue // a row with an unparseable date was already broken before this
		}
		convertedCurrency := r.ConvertedCurrency
		convertedMinor := r.ConvertedMinor
		if convertedCurrency == "" {
			convertedCurrency, convertedMinor = r.Currency, r.AmountMinor
		}
		writeMonefyRecord(bw, []string{
			on.Format(p.dateLayout), r.AccountName, r.CategoryName,
			plainDecimal(r.AmountMinor, r.Currency), r.Currency,
			plainDecimal(convertedMinor, convertedCurrency), convertedCurrency,
			r.Note,
		})
	}
	return bw.Flush()
}

// plainDecimal renders minor units the way Monefy writes an amount: the
// currency's decimals with trailing zeros dropped, and the point too when
// nothing is left after it.
func plainDecimal(minor int64, currency string) string {
	s := money.New(minor, currency).String(money.Exponent(currency))
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// writeMonefyRecord writes one CSV line, CRLF-terminated. bufio.Writer keeps
// the first error and returns it from Flush, which Write checks.
func writeMonefyRecord(w *bufio.Writer, fields []string) {
	for i, f := range fields {
		if i > 0 {
			_ = w.WriteByte(',')
		}
		if needsQuotes(f) {
			_, _ = w.WriteString(`"` + strings.ReplaceAll(f, `"`, `""`) + `"`)
		} else {
			_, _ = w.WriteString(f)
		}
	}
	_, _ = w.WriteString("\r\n")
}

func needsQuotes(f string) bool {
	if f == "" {
		return false
	}
	if strings.ContainsAny(f, ",\"\r\n") {
		return true
	}
	first, _ := utf8.DecodeRuneInString(f)
	last, _ := utf8.DecodeLastRuneInString(f)
	return unicode.IsSpace(first) || unicode.IsSpace(last)
}
