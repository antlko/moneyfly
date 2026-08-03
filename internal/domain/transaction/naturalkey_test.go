package transaction

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// TestNaturalKey_Golden freezes the dedup contract. Stage 04 depends on it
// byte-for-byte, and changing it would invalidate every stored key, so a refactor
// that alters the hash must fail here rather than in production.
func TestNaturalKey_Golden(t *testing.T) {
	// The first data row of the real export:
	//   19.07.2021,UAH,Utilities,-65,UAH,-65,UAH,Коммисия
	got := NaturalKey(day("2021-07-19"), "UAH", "Utilities", 6500, "UAH", "Коммисия")

	// Verified independently of this package:
	//   sha256("2021-07-19\x1fUAH\x1fUtilities\x1f6500\x1fUAH\x1fКоммисия")
	const want = "e381d907b4f357d0872b29873580ec6fe261e8007645d2fa56bc1cd8fd92fdc7"
	if got != want {
		t.Fatalf("natural key changed!\n got: %s\nwant: %s\n\n"+
			"If this change is intentional, it needs a migration that recomputes every\n"+
			"stored natural_key, and docs/adr/0008-natural-key-dedup.md must be updated.", got, want)
	}
}

func TestNaturalKey_UntrimmedDescription(t *testing.T) {
	// The export contains "Продукты " with a trailing space. Trimming it here would
	// silently merge two different rows.
	withSpace := NaturalKey(day("2021-07-20"), "UAH", "Food", 4400, "UAH", "Продукты ")
	without := NaturalKey(day("2021-07-20"), "UAH", "Food", 4400, "UAH", "Продукты")
	if withSpace == without {
		t.Fatal(`"Продукты " and "Продукты" must not collide`)
	}
}

func TestNaturalKey_EveryFieldMatters(t *testing.T) {
	base := NaturalKey(day("2021-07-19"), "UAH", "Utilities", 6500, "UAH", "Коммисия")
	variants := map[string]string{
		"date":        NaturalKey(day("2021-07-20"), "UAH", "Utilities", 6500, "UAH", "Коммисия"),
		"account":     NaturalKey(day("2021-07-19"), "EUR", "Utilities", 6500, "UAH", "Коммисия"),
		"category":    NaturalKey(day("2021-07-19"), "UAH", "Bills", 6500, "UAH", "Коммисия"),
		"amount":      NaturalKey(day("2021-07-19"), "UAH", "Utilities", 6501, "UAH", "Коммисия"),
		"currency":    NaturalKey(day("2021-07-19"), "UAH", "Utilities", 6500, "EUR", "Коммисия"),
		"description": NaturalKey(day("2021-07-19"), "UAH", "Utilities", 6500, "UAH", "Комиссия"),
	}
	for field, got := range variants {
		if got == base {
			t.Errorf("changing %s did not change the key", field)
		}
	}
}

func TestNaturalKey_FieldsCannotBeShiftedAcrossTheSeparator(t *testing.T) {
	// Concatenation attacks: "A" + "B" must not equal "AB" + "". The unit separator
	// cannot occur in any real field, so no pair of inputs can be made to collide.
	a := NaturalKey(day("2021-07-19"), "Cash", "Food", 100, "EUR", "x")
	b := NaturalKey(day("2021-07-19"), "CashFood", "", 100, "EUR", "x")
	if a == b {
		t.Fatal("field boundaries are not preserved")
	}
}

func TestNaturalKey_TimeOfDayIgnored(t *testing.T) {
	// Monefy exports a date with no time, so identity is per day by construction.
	morning := time.Date(2021, 7, 19, 8, 30, 0, 0, time.UTC)
	evening := time.Date(2021, 7, 19, 22, 15, 0, 0, time.UTC)
	if NaturalKey(morning, "UAH", "Food", 100, "UAH", "") !=
		NaturalKey(evening, "UAH", "Food", 100, "UAH", "") {
		t.Fatal("the time of day must not affect the key")
	}
}

func TestNaturalKey_CurrencyCaseNormalised(t *testing.T) {
	if NaturalKey(day("2021-07-19"), "UAH", "Food", 100, "uah", "") !=
		NaturalKey(day("2021-07-19"), "UAH", "Food", 100, "UAH", "") {
		t.Fatal("currency case must be normalised, or the same row imports twice")
	}
}
