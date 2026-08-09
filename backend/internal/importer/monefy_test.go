package importer

import (
	"strings"
	"testing"
)

const header = "date,account,category,amount,currency,converted amount,currency,description\n"

// The exact three rows from docs/MONEFY-PARITY.md §5, verified against a real
// export — UAH with two decimals, EUR, and HUF with none.
const sample = header +
	"19.07.2021,UAH,Utilities,-65,UAH,-65,UAH,Коммисия\n" +
	"03.12.2023,EUR,Eating out,-10,EUR,-397.3,UAH,\n" +
	"03.12.2023,HUF,Communication,-1000,HUF,-100,UAH,\n"

func TestParseSample(t *testing.T) {
	res, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected row errors: %+v", res.Errors)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(res.Rows))
	}

	want := []Row{
		{Line: 2, OccurredOn: "2021-07-19", Account: "UAH", Category: "Utilities",
			Kind: "expense", AmountMinor: 6500, Currency: "UAH", Note: "Коммисия"},
		{Line: 3, OccurredOn: "2023-12-03", Account: "EUR", Category: "Eating out",
			Kind: "expense", AmountMinor: 1000, Currency: "EUR", Note: ""},
		{Line: 4, OccurredOn: "2023-12-03", Account: "HUF", Category: "Communication",
			Kind: "expense", AmountMinor: 1000, Currency: "HUF", Note: ""},
	}
	for i, w := range want {
		if got := res.Rows[i]; got != w {
			t.Errorf("row %d = %+v, want %+v", i, got, w)
		}
	}
}

// A newer export from the same app, same phone, same account — different
// date format. This exact shape (no leading zeros, slashes, month-first) is
// what a real 3,222-row 2026 export used; before dateLayouts grew a second
// entry, every single row in that file failed with "not DD.MM.YYYY".
func TestParseAcceptsSlashSeparatedMonthFirstDates(t *testing.T) {
	csv := header +
		"7/19/2021,Cash,Bills,-65,UAH,-1.3,EUR,\n" + // single-digit month
		"12/25/2026,Cash,Bills,-65,EUR,-65,EUR,\n" + // double-digit month
		"8/9/2026,Cash,Bills,-65,EUR,-65,EUR,\n" // single-digit month and day

	res, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected row errors: %+v", res.Errors)
	}
	want := []string{"2021-07-19", "2026-12-25", "2026-08-09"}
	for i, w := range want {
		if got := res.Rows[i].OccurredOn; got != w {
			t.Errorf("row %d occurredOn = %q, want %q", i, got, w)
		}
	}
}

// "7/19" can only be July 19th — there is no 19th month — which is the actual
// evidence that a slash-separated Monefy export is month-first, not a
// convention picked because it seemed likely.
func TestSlashDatesAreReadMonthFirstNotDayFirst(t *testing.T) {
	csv := header + "7/19/2021,Cash,Bills,-65,UAH,-65,UAH,\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0].OccurredOn != "2021-07-19" {
		t.Fatalf("got %+v, want a single row on 2021-07-19", res.Rows)
	}
}

// A genuinely invalid date must still be rejected under either layout, not
// coerced into whichever one fails to notice.
func TestParseStillRejectsAnInvalidDate(t *testing.T) {
	csv := header + "31.13.2021,UAH,Utilities,-65,UAH,-65,UAH,bad date\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Rows) != 0 || len(res.Errors) != 1 {
		t.Fatalf("rows=%+v errors=%+v, want the row rejected", res.Rows, res.Errors)
	}
}

// The header names `currency` twice. Addressing columns positionally is the
// whole point — this proves the converted pair (columns 5-6) never leaks into
// the native one, even when the converted currency differs from the native
// one, which is the normal case.
func TestParseDiscardsConvertedColumns(t *testing.T) {
	csv := header + "03.12.2023,EUR,Eating out,-10,EUR,-397.3,UAH,\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("Parse: %v, errors %+v", err, res.Errors)
	}
	row := res.Rows[0]
	if row.Currency != "EUR" || row.AmountMinor != 1000 {
		t.Errorf("row = %+v, want the native EUR pair, not the converted UAH one", row)
	}
}

func TestParseStripsBOM(t *testing.T) {
	// \ufeff, not a literal BOM byte in the source: Go's own scanner
	// rejects a raw BOM sequence anywhere but the first three bytes of a
	// file, in a string literal or not.
	csv := "\ufeff" + sample
	res, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("got %d rows, want 3 — the BOM likely corrupted the header match", len(res.Rows))
	}
}

// A positive amount is income; Monefy carries the type in the sign alone,
// with no separate column.
func TestParseDerivesKindFromSign(t *testing.T) {
	csv := header + "01.01.2026,Cash,Salary,2000,EUR,2000,EUR,\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("Parse: %v, errors %+v", err, res.Errors)
	}
	if res.Rows[0].Kind != "income" || res.Rows[0].AmountMinor != 200000 {
		t.Errorf("row = %+v, want income 200000", res.Rows[0])
	}
}

// HUF has no minor unit at all — the exponent bug this whole app is written
// to avoid assuming away.
func TestParseZeroDecimalCurrency(t *testing.T) {
	csv := header + "03.12.2023,HUF,Communication,-1000,HUF,-100,UAH,\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("Parse: %v, errors %+v", err, res.Errors)
	}
	if res.Rows[0].AmountMinor != 1000 {
		t.Errorf("HUF amountMinor = %d, want 1000 (HUF has no decimals)", res.Rows[0].AmountMinor)
	}
}

// A quoted description containing the field separator must not be split into
// extra columns — this is encoding/csv's job, but the positional column
// count depends on it being done right.
func TestParseQuotedDescriptionWithComma(t *testing.T) {
	csv := header + `19.07.2021,UAH,Food,-20,UAH,-20,UAH,"Lunch, with a colleague"` + "\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("Parse: %v, errors %+v", err, res.Errors)
	}
	if res.Rows[0].Note != "Lunch, with a colleague" {
		t.Errorf("note = %q", res.Rows[0].Note)
	}
}

// The description column is sometimes absent entirely, not merely empty,
// when every row above it in the export also had none. A missing optional
// trailing column must not cost the whole row.
func TestParseMissingTrailingDescription(t *testing.T) {
	csv := header + "19.07.2021,UAH,Utilities,-65,UAH,-65,UAH\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("Parse: %v, errors %+v", err, res.Errors)
	}
	if res.Rows[0].Note != "" {
		t.Errorf("note = %q, want empty", res.Rows[0].Note)
	}
}

// A malformed row is reported, never silently dropped, and never aborts the
// rows around it — the exact failure mode docs/MONEFY-PARITY.md §5 documents
// a naive importer having.
func TestParseReportsRowErrorsWithoutAbortingTheFile(t *testing.T) {
	csv := header +
		"19.07.2021,UAH,Utilities,-65,UAH,-65,UAH,good row 1\n" +
		"31.13.2021,UAH,Utilities,-65,UAH,-65,UAH,bad date\n" +
		"19.07.2021,UAH,Utilities,not-a-number,UAH,-65,UAH,bad amount\n" +
		"19.07.2021,UAH,Utilities,-10.005,EUR,-65,UAH,too precise for EUR\n" +
		"19.07.2021,UAH,Utilities,-65,XX,-65,UAH,bad currency\n" +
		"19.07.2021,UAH,Utilities,-65,UAH,-65,UAH,good row 2\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("got %d good rows, want 2: %+v", len(res.Rows), res.Rows)
	}
	if len(res.Errors) != 4 {
		t.Fatalf("got %d row errors, want 4: %+v", len(res.Errors), res.Errors)
	}
	if res.Rows[0].Note != "good row 1" || res.Rows[1].Note != "good row 2" {
		t.Errorf("rows = %+v, want the two good ones in file order", res.Rows)
	}
	// Line numbers must point at the actual file line (1 = header) so they
	// mean something when the operator opens the CSV in a spreadsheet.
	if res.Errors[0].Line != 3 {
		t.Errorf("first error line = %d, want 3", res.Errors[0].Line)
	}
}

func TestParseRejectsShortRows(t *testing.T) {
	csv := header + "19.07.2021,UAH,Utilities,-65,UAH\n"
	res, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Rows) != 0 || len(res.Errors) != 1 {
		t.Fatalf("rows=%+v errors=%+v, want one row error and no rows", res.Rows, res.Errors)
	}
}

// --- NaturalKey --------------------------------------------------------------

func TestNaturalKeyIsStableAcrossReimports(t *testing.T) {
	res1, _ := Parse(strings.NewReader(sample))
	res2, _ := Parse(strings.NewReader(sample))

	for i := range res1.Rows {
		k1 := NaturalKey(res1.Rows[i], 0)
		k2 := NaturalKey(res2.Rows[i], 0)
		if k1 != k2 {
			t.Errorf("row %d: key %q on first parse, %q on second — re-importing an unchanged export must produce identical keys", i, k1, k2)
		}
	}
}

func TestNaturalKeyDistinguishesDuplicateLookingRows(t *testing.T) {
	csv := header +
		"19.07.2021,Cash,Food,-5,EUR,-5,EUR,coffee\n" +
		"19.07.2021,Cash,Food,-5,EUR,-5,EUR,coffee\n" // a second, genuine, identical purchase
	res, err := Parse(strings.NewReader(csv))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("Parse: %v, errors %+v", err, res.Errors)
	}

	k0 := NaturalKey(res.Rows[0], 0)
	k1 := NaturalKey(res.Rows[1], 1)
	if k0 == k1 {
		t.Fatal("two genuinely separate purchases collapsed onto one natural key")
	}

	// But the SAME occurrence index for identical content must reproduce the
	// same key — this is what makes de-duplication across two imports work.
	if NaturalKey(res.Rows[0], 0) != NaturalKey(res.Rows[1], 0) {
		t.Error("identical row content at the same occurrence index produced different keys")
	}
}

// --- Resolution ---------------------------------------------------------------

func TestResolveCategoryExactMatch(t *testing.T) {
	existing := []NamedRow{{ID: "cat:food", Name: "Food", Kind: "expense"}}
	got := ResolveCategory("food", "expense", existing, nil)
	if !got.Resolved() || got.ID != "cat:food" || got.ViaAlias {
		t.Errorf("got %+v", got)
	}
}

// The exact renames documented as having silently dropped 237 of 1,683 rows
// in a real export (docs/MONEFY-PARITY.md §5) — this table is the regression
// test for that incident.
func TestResolveCategoryAliasTable(t *testing.T) {
	existing := []NamedRow{
		{ID: "cat:hotel", Name: "Hotel/Trip", Kind: "expense"},
		{ID: "cat:clothes", Name: "Clothes", Kind: "expense"},
		{ID: "cat:studying", Name: "Studying", Kind: "expense"},
		{ID: "cat:sports", Name: "Sports", Kind: "expense"},
	}
	tests := []struct{ csvName, wantID string }{
		{"HotelTrip", "cat:hotel"},
		{"Clouth", "cat:clothes"},
		{"Studing", "cat:studying"},
		{"Sport", "cat:sports"},
	}
	for _, tt := range tests {
		got := ResolveCategory(tt.csvName, "expense", existing, nil)
		if got.ID != tt.wantID || !got.ViaAlias {
			t.Errorf("ResolveCategory(%q) = %+v, want id %q via alias", tt.csvName, got, tt.wantID)
		}
	}
}

// The failure the whole design exists to prevent: an unrecognised name is
// never auto-created and never silently matched to something else. It comes
// back unresolved, full stop.
func TestResolveCategoryNeverGuesses(t *testing.T) {
	existing := []NamedRow{{ID: "cat:bills", Name: "Bills", Kind: "expense"}}
	got := ResolveCategory("Utilities", "expense", existing, nil)
	if got.Resolved() {
		t.Errorf("an unrecognised category resolved to %+v instead of blocking", got)
	}
}

// A category and an income category sharing a name must not cross-match —
// the two are different rows a person set up on purpose.
func TestResolveCategoryScopedByKind(t *testing.T) {
	existing := []NamedRow{{ID: "cat:other-income", Name: "Other", Kind: "income"}}
	got := ResolveCategory("Other", "expense", existing, nil)
	if got.Resolved() {
		t.Errorf("matched an income category for an expense row: %+v", got)
	}
}

func TestResolveCategoryOverrideWins(t *testing.T) {
	existing := []NamedRow{{ID: "cat:bills", Name: "Bills", Kind: "expense"}}
	overrides := map[string]string{"Utilities": "cat:new-utilities"}
	got := ResolveCategory("Utilities", "expense", existing, overrides)
	if got.ID != "cat:new-utilities" {
		t.Errorf("got %+v, want the operator's own mapping to win", got)
	}
}

func TestResolveAccountIsCaseAndSpaceInsensitive(t *testing.T) {
	existing := []NamedRow{{ID: "acc:eur", Name: "EUR", Currency: "EUR"}}
	got := ResolveAccount(" eur ", "EUR", existing, nil)
	if got.ID != "acc:eur" {
		t.Errorf("got %+v", got)
	}
}

// The account equivalent of TestResolveCategoryNeverGuesses: a name that
// matches but a currency that does not must never resolve to the wrong
// wallet — two accounts can share a name (the reference export names them
// after their currency), and importing into the wrong one would misattribute
// every row to a balance in a currency it was never recorded in.
func TestResolveAccountRequiresMatchingCurrency(t *testing.T) {
	existing := []NamedRow{{ID: "acc:cash-eur", Name: "Cash", Currency: "EUR"}}
	got := ResolveAccount("Cash", "HUF", existing, nil)
	if got.Resolved() {
		t.Errorf("matched a EUR account for a HUF row: %+v", got)
	}
	if !got.CurrencyMismatch {
		t.Errorf("got %+v, want CurrencyMismatch so the operator sees why", got)
	}
}

// A name that does not exist at all — as opposed to existing in the wrong
// currency — must not claim a currency mismatch; the two need different UI.
func TestResolveAccountNoCurrencyMismatchWhenNameIsUnknown(t *testing.T) {
	existing := []NamedRow{{ID: "acc:cash-eur", Name: "Cash", Currency: "EUR"}}
	got := ResolveAccount("Wallet", "HUF", existing, nil)
	if got.Resolved() || got.CurrencyMismatch {
		t.Errorf("got %+v, want neither resolved nor a currency mismatch", got)
	}
}

func TestResolveAccountOverrideWinsRegardlessOfCurrency(t *testing.T) {
	existing := []NamedRow{{ID: "acc:cash-eur", Name: "Cash", Currency: "EUR"}}
	overrides := map[string]string{"Cash": "acc:cash-eur"}
	got := ResolveAccount("Cash", "HUF", existing, overrides)
	if got.ID != "acc:cash-eur" {
		t.Errorf("got %+v, want the operator's own mapping to win despite the currency mismatch", got)
	}
}
