package importer

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// SourceMonefy is the key of the Monefy CSV source.
const SourceMonefy = "monefy"

// monefyDateLayout is the observed export format: DD.MM.YYYY with dots. The
// previous pipeline expected M/D/YYYY and called log.Fatal on the first row
// (docs/04-import-monefy.md §4.1).
const monefyDateLayout = "02.01.2006"

// Column positions. The header contains `currency` twice, so a name map is
// impossible and columns are addressed positionally — this is measured from the
// real export, not assumed.
const (
	colDate = iota
	colAccount
	colCategory
	colAmount
	colCurrency
	colConvertedAmount
	colConvertedCurrency
	colDescription

	monefyColumns = 8
	// A row is usable with the first five columns; the converted pair is
	// discarded and the description is optional.
	monefyMinColumns = 5
)

// utf8BOM is present in every observed export. Left in place it turns the first
// header into a name with an invisible prefix, which no name map can match.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// MonefySource parses the Monefy CSV export.
//
// The exponents map comes from the currency table: HUF is 0-decimal, so
// `-1000 HUF` is 1000 minor units and not 100000 (docs/04-import-monefy.md §4.4).
type MonefySource struct {
	exponents map[string]int
}

// NewMonefySource builds the parser for the given currency exponents.
func NewMonefySource(exponents map[string]int) *MonefySource {
	return &MonefySource{exponents: exponents}
}

// Key implements Source.
func (s *MonefySource) Key() string { return SourceMonefy }

// Parse reads the export.
//
// Rows are split on line boundaries and each line is decoded on its own, so one
// malformed line becomes a ParseError carrying its number rather than aborting
// the file. The cost is that a quoted field containing a newline would be
// rejected instead of joined; no observed export contains one, and rejecting
// loudly beats silently mis-joining two transactions.
func (s *MonefySource) Parse(r io.Reader) ([]RawRow, []ParseError, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("importer: reading upload: %w", err)
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, ErrEmptyFile
	}

	lines := splitLines(data)
	if len(lines) == 0 {
		return nil, nil, ErrEmptyFile
	}

	delimiter := sniffDelimiter(lines[0])
	header, err := decodeLine(lines[0], delimiter)
	if err != nil || !looksLikeHeader(header) {
		return nil, nil, fmt.Errorf("%w: expected a Monefy CSV whose first line is %q",
			ErrUnrecognisedFormat, "date,account,category,amount,currency,converted amount,currency,description")
	}

	var (
		rows       []RawRow
		parseErrs  []ParseError
		lineNumber = 1 // the header is line 1; data starts at 2
	)
	for _, raw := range lines[1:] {
		lineNumber++
		if strings.TrimSpace(raw) == "" {
			continue
		}
		row, perr := s.parseRow(lineNumber, raw, delimiter)
		if perr != nil {
			parseErrs = append(parseErrs, *perr)
			continue
		}
		rows = append(rows, row)
	}
	return rows, parseErrs, nil
}

func (s *MonefySource) parseRow(lineNo int, raw string, delimiter rune) (RawRow, *ParseError) {
	reject := func(format string, args ...any) *ParseError {
		return &ParseError{LineNo: lineNo, Raw: raw, Reason: fmt.Sprintf(format, args...)}
	}

	fields, err := decodeLine(raw, delimiter)
	if err != nil {
		return RawRow{}, reject("line is not valid CSV: %v", csvReason(err))
	}
	if len(fields) < monefyMinColumns {
		return RawRow{}, reject("expected at least %d columns, got %d", monefyMinColumns, len(fields))
	}

	occurredOn, err := time.Parse(monefyDateLayout, strings.TrimSpace(fields[colDate]))
	if err != nil {
		return RawRow{}, reject("date %q is not in DD.MM.YYYY form", fields[colDate])
	}

	currency := strings.ToUpper(strings.TrimSpace(fields[colCurrency]))
	if currency == "" {
		return RawRow{}, reject("currency is empty")
	}
	exponent, ok := s.exponents[currency]
	if !ok {
		return RawRow{}, reject("unknown currency %q", currency)
	}

	// The signed decimal is parsed properly. The old shortcut `amount[1:]` turned
	// a positive 450 into 50 and survived only because no income is exported.
	amount, err := money.Parse(strings.TrimSpace(fields[colAmount]), currency, exponent)
	if err != nil {
		return RawRow{}, reject("amount %q is not a decimal representable in %s: %v",
			fields[colAmount], currency, err)
	}
	if amount.Minor == 0 {
		return RawRow{}, reject("amount is zero")
	}

	kind := transaction.KindIncome
	if amount.Minor < 0 {
		kind = transaction.KindExpense
	}

	description := ""
	if len(fields) > colDescription {
		// Deliberately untrimmed: "Продукты " with its trailing space is a
		// distinct natural key from "Продукты".
		description = fields[colDescription]
	}
	convertedCurrency := ""
	if len(fields) > colConvertedCurrency {
		convertedCurrency = strings.ToUpper(strings.TrimSpace(fields[colConvertedCurrency]))
	}

	return RawRow{
		LineNo:            lineNo,
		Raw:               raw,
		Date:              occurredOn.UTC(),
		AccountName:       strings.TrimSpace(fields[colAccount]),
		CategoryName:      fields[colCategory],
		AmountMinor:       amount.Abs().Minor,
		Currency:          currency,
		Kind:              kind,
		Description:       description,
		ConvertedCurrency: convertedCurrency,
	}, nil
}

// sniffDelimiter picks between comma and semicolon. The observed exports use a
// comma; a European locale emitting semicolons is plausible enough to detect
// rather than assume.
func sniffDelimiter(header string) rune {
	if strings.Count(header, ";") > strings.Count(header, ",") {
		return ';'
	}
	return ','
}

func decodeLine(line string, delimiter rune) ([]string, error) {
	reader := csv.NewReader(strings.NewReader(line))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	// Leading spaces are content: a description may begin with one and the
	// natural key is computed from the raw text.
	reader.TrimLeadingSpace = false
	return reader.Read()
}

// looksLikeHeader accepts the verified header. Requiring it means a file from
// some other app is refused with an explanation rather than parsed into
// nonsense.
func looksLikeHeader(header []string) bool {
	if len(header) < monefyMinColumns || len(header) > monefyColumns+2 {
		return false
	}
	want := []string{"date", "account", "category", "amount", "currency"}
	for i, w := range want {
		if !strings.EqualFold(strings.TrimSpace(header[i]), w) {
			return false
		}
	}
	return true
}

func splitLines(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	out := strings.Split(text, "\n")
	// A trailing newline is not a row.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func csvReason(err error) string {
	var perr *csv.ParseError
	if errors.As(err, &perr) {
		return perr.Err.Error()
	}
	return err.Error()
}
