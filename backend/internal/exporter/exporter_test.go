package exporter

import (
	"bytes"
	"strings"
	"testing"

	"moneyfly/internal/importer"
)

func TestRegistryListsBothProfiles(t *testing.T) {
	ids := IDs()
	if len(ids) != 2 || ids[0] != "monefy" || ids[1] != "native" {
		t.Fatalf("IDs() = %v, want [monefy native] (sorted)", ids)
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

func TestMonefyProfileMatchesTheDocumentedFormat(t *testing.T) {
	var buf bytes.Buffer
	p, _ := Get("monefy")
	if err := p.Write(&buf, sampleRows); err != nil {
		t.Fatalf("Write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if lines[0] != "date,account,category,amount,currency,converted amount,currency,description" {
		t.Errorf("header = %q, want the exact positional header docs/MONEFY-PARITY.md §5 documents", lines[0])
	}
	// DD.MM.YYYY, not the stored YYYY-MM-DD.
	if !strings.HasPrefix(lines[1], "19.07.2021,") {
		t.Errorf("first data row = %q, want it to start with the DD.MM.YYYY date", lines[1])
	}
}

// The round trip this profile exists for: what it writes, the importer must
// read back as the same transactions — otherwise "Monefy-compatible" is
// aspirational rather than true.
func TestMonefyProfileRoundTripsThroughTheImporter(t *testing.T) {
	var buf bytes.Buffer
	p, _ := Get("monefy")
	if err := p.Write(&buf, sampleRows); err != nil {
		t.Fatalf("Write: %v", err)
	}

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
