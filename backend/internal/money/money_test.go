package money

import (
	"errors"
	"math"
	"testing"
)

func TestMoney_HUFZeroDecimal(t *testing.T) {
	m, err := Parse("1000", "HUF", 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Minor != 1000 {
		t.Fatalf("HUF 1000 must be 1000 minor units, got %d", m.Minor)
	}
	if got := m.String(0); got != "1000" {
		t.Fatalf("String(0) = %q, want %q", got, "1000")
	}
	// The bug this guards: scaling HUF by 100 as if it had cents.
	if eur, err := Parse("1000", "EUR", 2); err != nil || eur.Minor != 100000 {
		t.Fatalf("EUR 1000 must be 100000 minor units, got %d (%v)", eur.Minor, err)
	}
}

func TestMoney_AddCurrencyMismatch(t *testing.T) {
	a := New(100, "EUR")
	b := New(100, "HUF")
	if _, err := a.Add(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Add: want ErrCurrencyMismatch, got %v", err)
	}
	if _, err := a.Sub(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Sub: want ErrCurrencyMismatch, got %v", err)
	}
	sum, err := a.Add(New(50, "eur"))
	if err != nil {
		t.Fatalf("same currency in lower case must add: %v", err)
	}
	if sum.Minor != 150 {
		t.Fatalf("sum = %d, want 150", sum.Minor)
	}
}

func TestMoney_ParseRoundTrip(t *testing.T) {
	cases := []struct {
		in       string
		currency string
		exponent int
		minor    int64
	}{
		{"0", "EUR", 2, 0},
		{"0.00", "EUR", 2, 0},
		{"12.50", "EUR", 2, 1250},
		{"397.30", "UAH", 2, 39730},
		{"-65", "UAH", 2, -6500},
		{"-1000", "HUF", 0, -1000},
		{"4562000", "HUF", 0, 4562000},
		{"1000", "HUF", 0, 1000},
		{"+3.07", "USD", 2, 307},
		{"0.01", "EUR", 2, 1},
		{".5", "EUR", 2, 50},
		{"26581.66", "EUR", 2, 2658166},
	}
	for _, c := range cases {
		got, err := Parse(c.in, c.currency, c.exponent)
		if err != nil {
			t.Fatalf("Parse(%q,%s,%d): %v", c.in, c.currency, c.exponent, err)
		}
		if got.Minor != c.minor {
			t.Fatalf("Parse(%q,%s,%d).Minor = %d, want %d", c.in, c.currency, c.exponent, got.Minor, c.minor)
		}
		// Round-trip: re-parsing the rendered form must land on the same minor units.
		back, err := Parse(got.String(c.exponent), c.currency, c.exponent)
		if err != nil {
			t.Fatalf("re-Parse(%q): %v", got.String(c.exponent), err)
		}
		if back.Minor != c.minor {
			t.Fatalf("round trip of %q gave %d, want %d", c.in, back.Minor, c.minor)
		}
	}
}

func TestMoney_ParseRejectsInexact(t *testing.T) {
	if _, err := Parse("10.5", "HUF", 0); !errors.Is(err, ErrInexact) {
		t.Fatalf("HUF 10.5: want ErrInexact, got %v", err)
	}
	if _, err := Parse("1.234", "EUR", 2); !errors.Is(err, ErrInexact) {
		t.Fatalf("EUR 1.234: want ErrInexact, got %v", err)
	}
	// Trailing zeros beyond the exponent are representable and must be accepted.
	if m, err := Parse("1.2300", "EUR", 2); err != nil || m.Minor != 123 {
		t.Fatalf("EUR 1.2300: got %d, %v", m.Minor, err)
	}
}

func TestMoney_ParseRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", " ", "-", "abc", "1,50", "1.2.3", "1e5", "€5", "--1"} {
		if _, err := Parse(in, "EUR", 2); err == nil {
			t.Fatalf("Parse(%q) must fail", in)
		}
	}
	if _, err := Parse("1", "EUR", 9); !errors.Is(err, ErrExponent) {
		t.Fatalf("exponent 9: want ErrExponent, got %v", err)
	}
}

func TestMoney_OverflowRejected(t *testing.T) {
	if _, err := Parse("92233720368547758.08", "EUR", 2); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	if _, err := New(math.MaxInt64, "EUR").Add(New(1, "EUR")); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Add overflow: want ErrOverflow, got %v", err)
	}
	if _, err := New(math.MinInt64, "EUR").Sub(New(1, "EUR")); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Sub overflow: want ErrOverflow, got %v", err)
	}
}

func TestMoney_Helpers(t *testing.T) {
	m := New(-1250, "EUR")
	if !m.IsNegative() {
		t.Fatal("IsNegative must be true")
	}
	if m.Abs().Minor != 1250 || m.Neg().Minor != 1250 {
		t.Fatal("Abs/Neg wrong")
	}
	if !New(0, "EUR").IsZero() {
		t.Fatal("IsZero must be true")
	}
	if got := New(-1250, "EUR").String(2); got != "-12.50" {
		t.Fatalf("String = %q, want -12.50", got)
	}
	if got := New(5, "EUR").String(2); got != "0.05" {
		t.Fatalf("String = %q, want 0.05", got)
	}
}
