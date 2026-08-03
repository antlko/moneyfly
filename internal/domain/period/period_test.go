package period

import (
	"errors"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func TestPeriod_PrevAcrossYear(t *testing.T) {
	if got := Period("2026-01").Prev(); got != "2025-12" {
		t.Fatalf("2026-01.Prev() = %s, want 2025-12", got)
	}
	if got := Period("2025-12").Next(); got != "2026-01" {
		t.Fatalf("2025-12.Next() = %s, want 2026-01", got)
	}
	if got := Period("2026-03").Prev(); got != "2026-02" {
		t.Fatalf("2026-03.Prev() = %s, want 2026-02", got)
	}
}

func TestPeriod_PrevFrom31stMonthIsStable(t *testing.T) {
	// Guards the classic AddDate trap: a naive day-preserving step from a 31-day
	// month into a 30-day one overshoots into the following month.
	for _, p := range []Period{"2026-03", "2026-05", "2026-07", "2026-10", "2026-12"} {
		got := p.Prev()
		want := Period(p.Time().AddDate(0, 0, -1).Format(Layout))
		if got != want {
			t.Fatalf("%s.Prev() = %s, want %s", p, got, want)
		}
	}
}

func TestParsePeriod(t *testing.T) {
	p, err := ParsePeriod("2026-07")
	if err != nil || p != "2026-07" {
		t.Fatalf("ParsePeriod: %s %v", p, err)
	}
	for _, bad := range []string{"", "2026", "2026-13", "2026-7", "26-07", "2026-07-01", "July"} {
		if _, err := ParsePeriod(bad); err == nil {
			t.Fatalf("ParsePeriod(%q) must fail", bad)
		} else if !errors.Is(err, apperr.ErrValidation) {
			t.Fatalf("ParsePeriod(%q) must be a validation error, got %v", bad, err)
		}
	}
}

func TestPeriod_Range(t *testing.T) {
	from, to := Period("2026-02").Range()
	if from.Format("2006-01-02") != "2026-02-01" {
		t.Fatalf("from = %v", from)
	}
	if to.Format("2006-01-02") != "2026-02-28" {
		t.Fatalf("to = %v, want the last day of February", to)
	}
	// Leap year.
	_, to = Period("2028-02").Range()
	if to.Format("2006-01-02") != "2028-02-29" {
		t.Fatalf("leap year to = %v", to)
	}
}

func TestPeriod_Contains(t *testing.T) {
	p := Period("2026-07")
	if !p.Contains(time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("last day must be inside the period")
	}
	if p.Contains(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("next month must be outside the period")
	}
}

func TestPeriod_Until(t *testing.T) {
	got, err := Period("2025-08").Until("2026-07")
	if err != nil {
		t.Fatalf("Until: %v", err)
	}
	if len(got) != 12 {
		t.Fatalf("Aug..Jul must be 12 periods, got %d", len(got))
	}
	if got[0] != "2025-08" || got[11] != "2026-07" {
		t.Fatalf("bounds wrong: %s..%s", got[0], got[11])
	}
	if _, err := Period("2026-07").Until("2026-06"); err == nil {
		t.Fatal("reversed range must fail")
	}
	single, err := Period("2026-07").Until("2026-07")
	if err != nil || len(single) != 1 {
		t.Fatalf("single-month range: %v %v", single, err)
	}
}

func TestPeriod_FiscalYearLabel(t *testing.T) {
	if got := Period("2025-08").FiscalYearLabel(8); got != "2025-2026" {
		t.Fatalf("August with start 8 = %s, want 2025-2026", got)
	}
	if got := Period("2026-07").FiscalYearLabel(8); got != "2025-2026" {
		t.Fatalf("July with start 8 = %s, want 2025-2026", got)
	}
	if got := Period("2026-01").FiscalYearLabel(1); got != "2026" {
		t.Fatalf("calendar year = %s, want 2026", got)
	}
}
