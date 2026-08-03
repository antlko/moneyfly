package importer

import (
	"os"
	"strings"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/transaction"
)

// exponents are the four currencies the real data uses. HUF is 0-decimal, which
// is the whole reason exponents are looked up rather than assumed.
var exponents = map[string]int{"EUR": 2, "UAH": 2, "USD": 2, "HUF": 0}

func parseFixture(t *testing.T, name string) ([]RawRow, []ParseError) {
	t.Helper()
	f, err := os.Open("../../../testdata/" + name)
	if err != nil {
		t.Fatalf("opening fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	rows, parseErrs, err := NewMonefySource(exponents).Parse(f)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return rows, parseErrs
}

func TestParse_StripsBOM(t *testing.T) {
	// The export is UTF-8 with a BOM. Left in place, the first header becomes a
	// name no map can match — and the file would be rejected as unrecognised.
	rows, parseErrs := parseFixture(t, "bom-crlf.csv")
	if len(parseErrs) != 0 {
		t.Fatalf("parse errors = %+v, want none", parseErrs)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (CRLF must be tolerated too)", len(rows))
	}
	if rows[0].CategoryName != "Utilities" {
		t.Fatalf("category = %q, want Utilities", rows[0].CategoryName)
	}
}

func TestParse_DuplicateCurrencyHeader(t *testing.T) {
	// `currency` appears twice in the header, so columns are addressed
	// positionally: 4-5 are the native pair, 6-7 the converted one we discard.
	rows, _ := parseFixture(t, "duplicate-header.csv")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].Currency != "EUR" || rows[0].AmountMinor != 1000 {
		t.Fatalf("row 0 = %s %d, want EUR 1000 from the native columns",
			rows[0].Currency, rows[0].AmountMinor)
	}
	if rows[0].ConvertedCurrency != "UAH" {
		t.Fatalf("converted currency = %q, want UAH (read but discarded)", rows[0].ConvertedCurrency)
	}
	// HUF is 0-decimal: -1000 HUF is 1000 minor units, not 100000.
	if rows[1].Currency != "HUF" || rows[1].AmountMinor != 1000 {
		t.Fatalf("row 1 = %s %d, want HUF 1000", rows[1].Currency, rows[1].AmountMinor)
	}
}

func TestParse_DottedDate(t *testing.T) {
	rows, parseErrs := parseFixture(t, "dotted-date.csv")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: only the dotted date parses", len(rows))
	}
	if got := rows[0].Date.Format("2006-01-02"); got != "2021-07-19" {
		t.Fatalf("date = %s, want 2021-07-19", got)
	}
	// The slashed form is rejected loudly rather than guessed at.
	if len(parseErrs) != 1 {
		t.Fatalf("parse errors = %d, want 1 for the M/D/YYYY row", len(parseErrs))
	}
	if !strings.Contains(parseErrs[0].Reason, "DD.MM.YYYY") {
		t.Fatalf("reason = %q, want it to name the expected format", parseErrs[0].Reason)
	}
}

func TestParse_HUFZeroDecimal(t *testing.T) {
	rows, _ := parseFixture(t, "huf-zero-decimal.csv")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].AmountMinor != 1000 {
		t.Fatalf("amount_minor = %d, want 1000: HUF has exponent 0", rows[0].AmountMinor)
	}
}

func TestParse_PositiveAmount_Income(t *testing.T) {
	// Guards the amount[1:] shortcut, which turned 450 into 50 and survived only
	// because the observed export contains no income at all.
	rows, _ := parseFixture(t, "positive-amount.csv")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Kind != transaction.KindIncome {
		t.Fatalf("kind = %q, want income", rows[0].Kind)
	}
	if rows[0].AmountMinor != 45000 {
		t.Fatalf("amount_minor = %d, want 45000 (not 50)", rows[0].AmountMinor)
	}
}

func TestParse_MalformedRowDoesNotAbort(t *testing.T) {
	rows, parseErrs := parseFixture(t, "malformed-row.csv")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2: one bad line must not cost the good ones", len(rows))
	}
	if len(parseErrs) != 1 {
		t.Fatalf("parse errors = %d, want 1", len(parseErrs))
	}
	if parseErrs[0].LineNo != 3 {
		t.Fatalf("line_no = %d, want 3", parseErrs[0].LineNo)
	}
}

func TestParse_UntrimmedDescription(t *testing.T) {
	// "Продукты " keeps its trailing space: the natural key is computed from the
	// raw text, and trimming here would collide two distinct transactions.
	rows, _ := parseFixture(t, "bom-crlf.csv")
	if rows[1].Description != "Продукты " {
		t.Fatalf("description = %q, want %q", rows[1].Description, "Продукты ")
	}
}

func TestParse_SemicolonDelimiter(t *testing.T) {
	rows, parseErrs := parseFixture(t, "semicolon.csv")
	if len(parseErrs) != 0 || len(rows) != 1 {
		t.Fatalf("rows = %d, errors = %d, want 1 and 0", len(rows), len(parseErrs))
	}
	if rows[0].AmountMinor != 6500 {
		t.Fatalf("amount_minor = %d, want 6500", rows[0].AmountMinor)
	}
}

func TestParse_RejectsForeignFormat(t *testing.T) {
	f, err := os.Open("../../../testdata/not-monefy.csv")
	if err != nil {
		t.Fatalf("opening fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	if _, _, err := NewMonefySource(exponents).Parse(f); err == nil {
		t.Fatal("a non-Monefy CSV must be refused, not parsed into nonsense")
	}
}

func TestParse_RealExport_LineCount(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/monefy-real-1683.csv")
	if err != nil {
		t.Fatalf("reading the real export: %v", err)
	}
	// 1,684 lines including the header: the fixture is the real file, unedited.
	lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1
	if lines != 1684 {
		t.Fatalf("fixture has %d lines, want 1684 (header + 1683 rows)", lines)
	}

	rows, parseErrs := parseFixture(t, "monefy-real-1683.csv")
	if len(parseErrs) != 0 {
		t.Fatalf("parse errors = %+v, want none", parseErrs)
	}
	if len(rows) != 1683 {
		t.Fatalf("parsed rows = %d, want 1683", len(rows))
	}
}

func TestSuggest_ProposesButNeverApplies(t *testing.T) {
	candidates := []candidate{{ID: 7, Name: "Clothes"}, {ID: 8, Name: "Food"}}

	// Distance 2: proposed with medium confidence.
	got := suggest("Clouth", candidates)
	if got == nil || got.Name != "Clothes" || got.Confidence != "medium" {
		t.Fatalf("suggest(Clouth) = %+v, want a medium-confidence Clothes", got)
	}
	// A case and whitespace variant is high confidence, still only a proposal.
	if got := suggest("  food ", candidates); got == nil || got.Name != "Food" || got.Confidence != "high" {
		t.Fatalf("suggest(' food ') = %+v, want high-confidence Food", got)
	}
	// Distance 3 and beyond is noise.
	if got := suggest("Croissants", candidates); got != nil {
		t.Fatalf("suggest(Croissants) = %+v, want no proposal", got)
	}
}
