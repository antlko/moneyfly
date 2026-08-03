// Package period models a calendar month, the unit every report groups by.
//
// See docs/implementation-plan/00-conventions.md §4. The fiscal year start is a
// user setting affecting grouping and labels only, never storage.
package period

import (
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// Layout is the storage and wire format for a period.
const Layout = "2006-01"

// Period is a calendar month, "YYYY-MM".
type Period string

// ParsePeriod validates and normalises a period string.
func ParsePeriod(s string) (Period, error) {
	t, err := time.Parse(Layout, s)
	if err != nil {
		return "", apperr.Validation("period", "must be a month in YYYY-MM form, got %q", s)
	}
	return Period(t.Format(Layout)), nil
}

// FromTime returns the period containing t.
func FromTime(t time.Time) Period { return Period(t.UTC().Format(Layout)) }

// String implements fmt.Stringer.
func (p Period) String() string { return string(p) }

// Time returns the first instant of the period, in UTC.
func (p Period) Time() time.Time {
	t, err := time.Parse(Layout, string(p))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// Prev returns the previous month, crossing years correctly.
func (p Period) Prev() Period { return FromTime(p.Time().AddDate(0, -1, 0)) }

// Next returns the following month.
func (p Period) Next() Period { return FromTime(p.Time().AddDate(0, 1, 0)) }

// Range returns the inclusive [first day, last day] of the period, as dates.
func (p Period) Range() (from, to time.Time) {
	from = p.Time()
	to = from.AddDate(0, 1, -1)
	return from, to
}

// Contains reports whether a date falls in the period.
func (p Period) Contains(t time.Time) bool { return FromTime(t) == p }

// Valid reports whether the period parses.
func (p Period) Valid() bool {
	_, err := time.Parse(Layout, string(p))
	return err == nil
}

// Until returns every period from p to end inclusive, or an error when end is
// before p. Used by the budget bulk seed, which writes a year from one figure.
func (p Period) Until(end Period) ([]Period, error) {
	if !p.Valid() || !end.Valid() {
		return nil, apperr.Validation("period", "invalid range %s..%s", p, end)
	}
	if end < p {
		return nil, apperr.Validation("to_period", "must not be before from_period (%s < %s)", end, p)
	}
	out := []Period{}
	for cur := p; cur <= end; cur = cur.Next() {
		out = append(out, cur)
		if len(out) > 1200 { // 100 years; a runaway range is a bug, not a request
			return nil, apperr.Validation("to_period", "range too large: %s..%s", p, end)
		}
	}
	return out, nil
}

// FiscalYearLabel renders the fiscal year a period belongs to, given the
// starting month (default 8, from the workbook's Aug->Jul layout).
func (p Period) FiscalYearLabel(startMonth int) string {
	if startMonth < 1 || startMonth > 12 {
		startMonth = 1
	}
	t := p.Time()
	year := t.Year()
	if int(t.Month()) < startMonth {
		year--
	}
	if startMonth == 1 {
		return fmt.Sprintf("%d", year)
	}
	return fmt.Sprintf("%d-%d", year, year+1)
}
