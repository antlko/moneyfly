// Package money holds the exact-money type used everywhere above the SQL layer.
//
// Amounts are integer minor units plus a currency code, never floats, and
// cross-currency arithmetic is refused rather than silently converted — see the
// invariant in CLAUDE.md. The exponent is always a parameter, never assumed to
// be 2: HUF has none, and a real Monefy export contains it.
//
// Ported from the earlier implementation (commit 6fbaa56) along with its tests.

package money

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Errors returned by this package. Callers match with errors.Is, never by string.
var (
	// ErrCurrencyMismatch is returned by Add and Sub when the operands are in
	// different currencies. There is no implicit conversion anywhere here.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	// ErrOverflow is returned when a result cannot be held in an int64.
	ErrOverflow = errors.New("money: overflow")
	// ErrSyntax is returned by Parse for input that is not a plain decimal.
	ErrSyntax = errors.New("money: invalid decimal")
	// ErrInexact is returned by Parse when the input carries more precision
	// than the currency's exponent can represent.
	ErrInexact = errors.New("money: value not representable at currency exponent")
	// ErrExponent is returned for an exponent outside the supported range.
	ErrExponent = errors.New("money: unsupported exponent")
)

// MaxExponent is the largest currency exponent supported. ISO-4217 tops out at 4.
const MaxExponent = 4

// Money is an exact amount in one currency, stored in minor units.
// HUF has exponent 0, so Money{1000,"HUF"} is 1000 forint, not 10.
type Money struct {
	Minor    int64
	Currency string // ISO-4217, uppercase
}

// New builds a Money. The currency is upper-cased so comparisons are stable.
func New(minor int64, currency string) Money {
	return Money{Minor: minor, Currency: strings.ToUpper(currency)}
}

// Add returns m+o, or ErrCurrencyMismatch when the currencies differ.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: %s + %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}
	sum := m.Minor + o.Minor
	// Overflow iff the operands share a sign and the result flipped it.
	if (m.Minor > 0 && o.Minor > 0 && sum < 0) || (m.Minor < 0 && o.Minor < 0 && sum >= 0) {
		return Money{}, fmt.Errorf("%w: %d + %d", ErrOverflow, m.Minor, o.Minor)
	}
	return Money{Minor: sum, Currency: m.Currency}, nil
}

// Sub returns m-o, or ErrCurrencyMismatch when the currencies differ.
func (m Money) Sub(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: %s - %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}
	if o.Minor == math.MinInt64 {
		return Money{}, fmt.Errorf("%w: negating %d", ErrOverflow, o.Minor)
	}
	return m.Add(Money{Minor: -o.Minor, Currency: o.Currency})
}

// Neg returns the amount with the opposite sign.
func (m Money) Neg() Money {
	return Money{Minor: -m.Minor, Currency: m.Currency}
}

// IsZero reports whether the amount is exactly zero.
func (m Money) IsZero() bool { return m.Minor == 0 }

// IsNegative reports whether the amount is below zero.
func (m Money) IsNegative() bool { return m.Minor < 0 }

// Abs returns the amount without its sign.
func (m Money) Abs() Money {
	if m.Minor < 0 {
		return Money{Minor: -m.Minor, Currency: m.Currency}
	}
	return m
}

// Parse reads a decimal string ("-65", "397.30") into minor units for the
// given currency's exponent. It rejects anything it cannot represent exactly,
// so "10.5" in HUF (exponent 0) is an error rather than a silent 10 or 11.
func Parse(s, currency string, exponent int) (Money, error) {
	if exponent < 0 || exponent > MaxExponent {
		return Money{}, fmt.Errorf("%w: %d", ErrExponent, exponent)
	}
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Money{}, fmt.Errorf("%w: empty", ErrSyntax)
	}

	neg := false
	switch raw[0] {
	case '-':
		neg = true
		raw = raw[1:]
	case '+':
		raw = raw[1:]
	}
	if raw == "" {
		return Money{}, fmt.Errorf("%w: sign without digits", ErrSyntax)
	}

	intPart, fracPart := raw, ""
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		intPart, fracPart = raw[:i], raw[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return Money{}, fmt.Errorf("%w: %q has two decimal points", ErrSyntax, s)
		}
	}
	if intPart == "" && fracPart == "" {
		return Money{}, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	if err := digitsOnly(intPart, s); err != nil {
		return Money{}, err
	}
	if err := digitsOnly(fracPart, s); err != nil {
		return Money{}, err
	}

	// Anything beyond the currency's exponent must be zero, otherwise the value
	// is not representable and we refuse rather than round.
	if len(fracPart) > exponent {
		if strings.Trim(fracPart[exponent:], "0") != "" {
			return Money{}, fmt.Errorf("%w: %q in %s (exponent %d)", ErrInexact, s, currency, exponent)
		}
		fracPart = fracPart[:exponent]
	}

	minor, err := parseUint(intPart)
	if err != nil {
		return Money{}, err
	}
	for i := 0; i < exponent; i++ {
		var ok bool
		if minor, ok = mul10(minor); !ok {
			return Money{}, fmt.Errorf("%w: %q", ErrOverflow, s)
		}
	}
	for i := 0; i < exponent; i++ {
		var digit int64
		if i < len(fracPart) {
			digit = int64(fracPart[i] - '0')
		}
		scale := pow10(exponent - 1 - i)
		add, ok := mulInt64(digit, scale)
		if !ok {
			return Money{}, fmt.Errorf("%w: %q", ErrOverflow, s)
		}
		if minor, ok = addInt64(minor, add); !ok {
			return Money{}, fmt.Errorf("%w: %q", ErrOverflow, s)
		}
	}
	if neg {
		minor = -minor
	}
	return New(minor, currency), nil
}

// String renders with the currency's exponent. Money{1000,"HUF"} -> "1000".
// An exponent outside the supported range is clamped to 0 rather than panicking,
// because String is called from log and error paths.
func (m Money) String(exponent int) string {
	if exponent <= 0 || exponent > MaxExponent {
		return fmt.Sprintf("%d", m.Minor)
	}
	sign := ""
	minor := m.Minor
	if minor < 0 {
		sign = "-"
		// -MinInt64 overflows; render via the unsigned magnitude instead.
		if minor == math.MinInt64 {
			return sign + splitDecimal(uint64(math.MaxInt64)+1, exponent)
		}
		minor = -minor
	}
	return sign + splitDecimal(uint64(minor), exponent)
}

func splitDecimal(magnitude uint64, exponent int) string {
	scale := uint64(pow10(exponent))
	whole := magnitude / scale
	frac := magnitude % scale
	return fmt.Sprintf("%d.%0*d", whole, exponent, frac)
}

func digitsOnly(s, orig string) error {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return fmt.Errorf("%w: %q", ErrSyntax, orig)
		}
	}
	return nil
}

func parseUint(s string) (int64, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		var ok bool
		if n, ok = mul10(n); !ok {
			return 0, fmt.Errorf("%w: %q", ErrOverflow, s)
		}
		if n, ok = addInt64(n, int64(s[i]-'0')); !ok {
			return 0, fmt.Errorf("%w: %q", ErrOverflow, s)
		}
	}
	return n, nil
}

func pow10(n int) int64 {
	out := int64(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}

func mul10(n int64) (int64, bool) { return mulInt64(n, 10) }

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	out := a * b
	if out/b != a {
		return 0, false
	}
	return out, true
}

func addInt64(a, b int64) (int64, bool) {
	out := a + b
	if (a > 0 && b > 0 && out < 0) || (a < 0 && b < 0 && out >= 0) {
		return 0, false
	}
	return out, true
}
