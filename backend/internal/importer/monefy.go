// Package importer reads a Monefy CSV export into transaction rows.
//
// The format is documented, verified against a real 1,683-row export, in
// docs/MONEFY-PARITY.md §5 — read that first if anything here looks odd. Three
// things from it drive every decision in this file:
//
//   - Columns are addressed positionally, never by header name: `currency`
//     appears twice (the native pair and the pair converted to whatever base
//     currency the phone had that day), so a name-keyed map cannot tell them
//     apart, and only the native pair is authoritative.
//   - There is no transaction id and no type column — the sign of the amount
//     is the only thing that says expense from income.
//   - A category or account name this app does not recognise must never be
//     auto-created or silently dropped. A naive importer that did exactly
//     that lost 14% of a real export without raising anything anywhere; see
//     resolve.go.
package importer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"moneyfly/internal/money"
)

// dateLayouts are every date format a real Monefy export has been seen to
// use, tried in order.
//
// DD.MM.YYYY (dots) is the original, documented format — verified against a
// 1,683-row export, see docs/MONEFY-PARITY.md §5. M/D/YYYY (slashes, no
// leading zeros) showed up in a newer export from the same app: the
// exporter's date format follows the device's own locale, not anything this
// app controls, so one layout was never going to cover every install — a
// real 3,222-row export with today's date on it failed on every single row
// with "not DD.MM.YYYY" until this was added.
//
// Slash-separated is read month-first, not day-first, and that is not a
// guess: this format's own rows are the evidence. "7/19/2021" cannot be
// day=7 month=19 — there is no 19th month — so it can only be July 19th. A
// genuinely day-first slash export, if one ever turns up, needs its own
// layout added here rather than a guess at which of the two this is.
var dateLayouts = []string{
	"02.01.2006", // DD.MM.YYYY
	"1/2/2006",   // M/D/YYYY
}

// parseDate tries every known Monefy date format in turn.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("date %q is not a recognised format (DD.MM.YYYY or M/D/YYYY)", s)
}

// columns is the minimum field count a row must have. Trailing empty fields
// (an empty description) are sometimes dropped entirely by the exporter, so
// this is checked as "at least", not "exactly".
const columns = 7

// Row is one successfully parsed line, before category and account names are
// resolved to ids.
type Row struct {
	// Line is 1-based and counts the header, so it matches what a person sees
	// opening the file in a spreadsheet.
	Line        int
	OccurredOn  string // YYYY-MM-DD
	Account     string // trimmed, exactly as written in the CSV
	Category    string // trimmed
	Kind        string // "expense" | "income", derived from the amount's sign
	AmountMinor int64  // always positive; Kind carries the sign
	Currency    string
	Note        string // trimmed description; "" when the column is absent or blank
}

// RowError is one line that could not be read. It is carried alongside a
// successful ParseResult rather than aborting the parse — one malformed row
// must not cost the operator the other 1,682, the same reasoning
// idx_txn_natural_key exists for on the sync side (docs/SYNC.md).
type RowError struct {
	Line   int
	Reason string
}

// ParseResult is a full parse: every row that could be read, and every one
// that could not. Neither list is ever silently dropped by a caller — see
// the import handler, which reports both to the operator.
type ParseResult struct {
	Rows   []Row
	Errors []RowError
}

// Parse reads a Monefy CSV export from r.
func Parse(r io.Reader) (*ParseResult, error) {
	br := bufio.NewReader(r)
	// Strip a UTF-8 BOM — without this the first header cell reads as the BOM
	// followed by "date" and nothing downstream that compares against "date"
	// matches, silently.
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}

	cr := csv.NewReader(br)
	// Rows are checked for length below, individually, so one short row is a
	// RowError rather than aborting the whole file.
	cr.FieldsPerRecord = -1

	if _, err := cr.Read(); err != nil {
		return nil, fmt.Errorf("importer: reading header: %w", err)
	}

	res := &ParseResult{}
	line := 1
	for {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			res.Errors = append(res.Errors, RowError{Line: line, Reason: err.Error()})
			continue
		}
		row, err := parseRow(record)
		if err != nil {
			res.Errors = append(res.Errors, RowError{Line: line, Reason: err.Error()})
			continue
		}
		row.Line = line
		res.Rows = append(res.Rows, *row)
	}
	return res, nil
}

// parseRow reads one CSV record. Columns, positionally:
//
//	0 date | 1 account | 2 category | 3 amount | 4 currency |
//	5 converted amount (discarded) | 6 converted currency (discarded) | 7 description
func parseRow(record []string) (*Row, error) {
	if len(record) < columns {
		return nil, fmt.Errorf("expected at least %d columns, got %d", columns, len(record))
	}
	date, account, category, amountStr, currency := record[0], record[1], record[2], record[3], record[4]

	on, err := parseDate(date)
	if err != nil {
		return nil, err
	}

	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return nil, fmt.Errorf("currency %q is not a three-letter code", currency)
	}

	amt, err := money.Parse(amountStr, currency, money.Exponent(currency))
	if err != nil {
		return nil, fmt.Errorf("amount %q: %w", amountStr, err)
	}

	// No type column: Monefy carries expense/income entirely in the sign, and
	// this app stores expenses negative for the same reason (RecordView.vue) —
	// summing a month needs no separate lookup of which kind a row is.
	kind := "expense"
	if !amt.IsNegative() {
		kind = "income"
	}

	description := ""
	if len(record) > columns {
		description = record[columns]
	}

	return &Row{
		OccurredOn:  on.Format("2006-01-02"),
		Account:     strings.TrimSpace(account),
		Category:    strings.TrimSpace(category),
		Kind:        kind,
		AmountMinor: amt.Abs().Minor,
		Currency:    currency,
		Note:        strings.TrimSpace(description),
	}, nil
}

// NaturalKey identifies one occurrence of a row for de-duplication, the same
// structural mechanism idx_txn_natural_key exists for (docs/SYNC.md) — never a
// constraint, because the write path must not be able to fail on data.
//
// It hashes everything about the row except `occurrence`, so importing an
// unchanged export a second time — or one that overlaps an earlier one, which
// is the normal way someone re-exports "since last time" — reproduces the
// identical key for every row that appears in both, and the existence check
// in the commit handler turns that into a no-op rather than a duplicate.
// `occurrence` is the 0-based count of identical-looking rows seen so far in
// this file, in order, which is what keeps two genuinely separate purchases
// on the same day for the same amount from collapsing into one.
func NaturalKey(row Row, occurrence int) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s|%d|%s|%s",
		row.OccurredOn, row.Account, row.Category, row.Kind, row.AmountMinor, row.Currency, row.Note))
	return "csv:" + hex.EncodeToString(h[:8]) + ":" + fmt.Sprint(occurrence)
}
