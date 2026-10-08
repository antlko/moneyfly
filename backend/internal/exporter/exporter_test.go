package exporter

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"moneyfly/internal/importer"
)

func TestRegistryListsBothProfiles(t *testing.T) {
	ids := IDs()
	if len(ids) != 3 || ids[0] != "monefy" || ids[1] != "monefy-dmy" || ids[2] != "native" {
		t.Fatalf("IDs() = %v, want [monefy monefy-dmy native] (sorted)", ids)
	}
	for _, id := range ids {
		if _, ok := Get(id); !ok {
			t.Errorf("Get(%q) missing", id)
		}
	}
	if _, ok := Get("nope"); ok {
		t.Error("Get(\"nope\") found something")
	}
}

var sampleRows = []Row{
	{OccurredOn: "2021-07-19", AccountName: "Cash", CategoryName: "Food", AmountMinor: -1000, Currency: "EUR", Note: "lunch"},
	{OccurredOn: "2021-07-20", AccountName: "Cash", CategoryName: "Salary", AmountMinor: 200000, Currency: "EUR", Note: ""},
	{OccurredOn: "2021-07-21", AccountName: "HUF", CategoryName: "Communication", AmountMinor: -1000, Currency: "HUF", Note: ""},
}

func TestRowKindFromSign(t *testing.T) {
	if sampleRows[0].Kind() != "expense" {
		t.Error("negative amount should be expense")
	}
	if sampleRows[1].Kind() != "income" {
		t.Error("positive amount should be income")
	}
}

func TestNativeProfileWrite(t *testing.T) {
	var buf bytes.Buffer
	p, _ := Get("native")
	if err := p.Write(&buf, sampleRows); err != nil {
		t.Fatalf("Write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if lines[0] != "date,kind,account,category,amount,currency,note" {
		t.Errorf("header = %q", lines[0])
	}
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (header + 3 rows)", len(lines))
	}
	// HUF has no minor unit — the exponent bug this whole app avoids assuming
	// away, checked again here because a formatter is exactly the kind of
	// code that quietly re-introduces it.
	if !strings.Contains(lines[3], ",-1000,HUF,") {
		t.Errorf("HUF row = %q, want a whole-number amount with no decimal point", lines[3])
	}
	if !strings.Contains(lines[1], "-10.00,EUR") {
		t.Errorf("EUR row = %q, want -10.00", lines[1])
	}
}

// The exact bytes Monefy's own exporter writes, reproduced from a real
// 3,292-row export (docs/MONEFY-PARITY.md §5) with the personal details
// replaced: BOM, CRLF, M/D/YYYY with no leading zeros, amounts with no
// trailing zeros, every converted figure in the base currency, rows grouped by
// account and oldest first within each, and a field quoted only when it has
// to be — including for trailing whitespace, which encoding/csv would not.
// Scripts are written against that file; this one has to read the same.
func TestMonefyProfileWritesMonefysExactBytes(t *testing.T) {
	rows := []Row{
		{OccurredOn: "2021-07-19", AccountName: "UAH", CategoryName: "Food", AmountMinor: -6500, Currency: "UAH",
			ConvertedMinor: -130, ConvertedCurrency: "EUR"},
		{OccurredOn: "2023-12-03", AccountName: "HUF", CategoryName: "Communication", AmountMinor: -1000, Currency: "HUF",
			ConvertedMinor: -260, ConvertedCurrency: "EUR"},
		{OccurredOn: "2023-12-03", AccountName: "EUR", CategoryName: "Eating out", AmountMinor: -1000, Currency: "EUR",
			ConvertedMinor: -1000, ConvertedCurrency: "EUR", Note: "pizza "},
		{OccurredOn: "2024-01-15", AccountName: "EUR", CategoryName: "Studing ", AmountMinor: -39730, Currency: "EUR",
			ConvertedMinor: -39730, ConvertedCurrency: "EUR", Note: `books, "used"`},
		// Moved between accounts: not spending, so not in the file at all.
		{OccurredOn: "2024-01-16", AccountName: "EUR", AmountMinor: -5000, Currency: "EUR", Transfer: true,
			ConvertedMinor: -5000, ConvertedCurrency: "EUR"},
		{OccurredOn: "2024-02-01", AccountName: "EUR", CategoryName: "Salary", AmountMinor: 250000, Currency: "EUR",
			ConvertedMinor: 250000, ConvertedCurrency: "EUR"},
		{OccurredOn: "2026-10-07", AccountName: "UAH", CategoryName: "Taxi", AmountMinor: -1245, Currency: "UAH",
			ConvertedMinor: -25, ConvertedCurrency: "EUR"},
		// No rate known for the day: the original figure, never a guess.
		{OccurredOn: "2026-10-07", AccountName: "UAH", CategoryName: "Food", AmountMinor: -100, Currency: "UAH"},
	}
	want := "\xEF\xBB\xBF" +
		"date,account,category,amount,currency,converted amount,currency,description\r\n" +
		"12/3/2023,EUR,Eating out,-10,EUR,-10,EUR,\"pizza \"\r\n" +
		"1/15/2024,EUR,\"Studing \",-397.3,EUR,-397.3,EUR,\"books, \"\"used\"\"\"\r\n" +
		"2/1/2024,EUR,Salary,2500,EUR,2500,EUR,\r\n" +
		"12/3/2023,HUF,Communication,-1000,HUF,-2.6,EUR,\r\n" +
		"7/19/2021,UAH,Food,-65,UAH,-1.3,EUR,\r\n" +
		"10/7/2026,UAH,Taxi,-12.45,UAH,-0.25,EUR,\r\n" +
		"10/7/2026,UAH,Food,-1,UAH,-1,UAH,\r\n"

	var buf bytes.Buffer
	p, _ := Get("monefy")
	if err := p.Write(&buf, rows); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != want {
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := range max(len(gotLines), len(wantLines)) {
			var g, w string
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if g != w {
				t.Errorf("line %d:\n got %q\nwant %q", i+1, g, w)
			}
		}
	}
}

// Older Monefy exports — and phones in most of Europe — write DD.MM.YYYY.
func TestMonefyDMYProfileUsesDottedDates(t *testing.T) {
	var buf bytes.Buffer
	p, _ := Get("monefy-dmy")
	if err := p.Write(&buf, sampleRows[:1]); err != nil {
		t.Fatalf("Write: %v", err)
	}
	lines := strings.Split(buf.String(), "\r\n")
	if lines[1] != "19.07.2021,Cash,Food,-10,EUR,-10,EUR,lunch" {
		t.Errorf("row = %q", lines[1])
	}
}

func TestPlainDecimal(t *testing.T) {
	for _, tc := range []struct {
		minor    int64
		currency string
		want     string
	}{
		{-1000, "EUR", "-10"},
		{-1050, "EUR", "-10.5"},
		{-1055, "EUR", "-10.55"},
		{-5, "EUR", "-0.05"},
		{0, "EUR", "0"},
		{-10524, "HUF", "-10524"},
		{-1000, "HUF", "-1000"}, // a zero-decimal currency must keep its own zeros
		{250000, "EUR", "2500"},
	} {
		if got := plainDecimal(tc.minor, tc.currency); got != tc.want {
			t.Errorf("plainDecimal(%d, %s) = %q, want %q", tc.minor, tc.currency, got, tc.want)
		}
	}
}

// The round trip this profile exists for: what it writes, the importer must
// read back as the same transactions — otherwise "Monefy-compatible" is
// aspirational rather than true.
func TestMonefyProfileRoundTripsThroughTheImporter(t *testing.T) {
	for _, id := range []string{"monefy", "monefy-dmy"} {
		t.Run(id, func(t *testing.T) { roundTrip(t, id) })
	}
}

func roundTrip(t *testing.T, id string) {
	var buf bytes.Buffer
	p, _ := Get(id)
	// Rows already in the order the profile writes them (by account, then
	// date), so the comparison below can go index by index.
	sorted := slices.Clone(sampleRows)
	slices.SortStableFunc(sorted, func(a, b Row) int { return strings.Compare(a.AccountName, b.AccountName) })
	if err := p.Write(&buf, sorted); err != nil {
		t.Fatalf("Write: %v", err)
	}
	sampleRows := sorted

	res, err := importer.Parse(&buf)
	if err != nil {
		t.Fatalf("importer.Parse: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("importer reported errors reading our own export: %+v", res.Errors)
	}
	if len(res.Rows) != len(sampleRows) {
		t.Fatalf("got %d rows back, want %d", len(res.Rows), len(sampleRows))
	}
	for i, want := range sampleRows {
		got := res.Rows[i]
		if got.OccurredOn != want.OccurredOn || got.Account != want.AccountName ||
			got.Category != want.CategoryName || got.Kind != want.Kind() ||
			got.Currency != want.Currency || got.Note != want.Note {
			t.Errorf("row %d = %+v, want it to match source row %+v", i, got, want)
			continue
		}
		wantMinor := want.AmountMinor
		if wantMinor < 0 {
			wantMinor = -wantMinor
		}
		if got.AmountMinor != wantMinor {
			t.Errorf("row %d amountMinor = %d, want %d", i, got.AmountMinor, wantMinor)
		}
	}
}
