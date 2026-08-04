package money

import "strings"

// Exponents lists the currencies whose minor unit is not 1/100. Everything not
// named here has exponent 2.
//
// This is the canonical table. `web-ui/src/lib/money.ts` holds the same list so
// the client can format and convert with no network — the two must stay
// identical, and `/api/fx/currencies` exists so a client can notice when they
// are not. Same arrangement as the LWW rule in `sync.Wins` / `lww.ts`: duplicated
// on purpose, because the alternative is a round trip on a path that has to work
// offline.
var Exponents = map[string]int{
	"BIF": 0,
	"CLP": 0,
	"DJF": 0,
	"GNF": 0,
	"ISK": 0,
	"JPY": 0,
	"KMF": 0,
	"KRW": 0,
	"PYG": 0,
	"RWF": 0,
	"UGX": 0,
	"UYI": 0,
	"VND": 0,
	"VUV": 0,
	"XAF": 0,
	"XOF": 0,
	"XPF": 0,
	// Formally 2, but the fillér has not circulated since 1999 and a real
	// Monefy export writes forint as whole units. Following the export is what
	// makes import round-trip.
	"HUF": 0,
	"BHD": 3,
	"IQD": 3,
	"JOD": 3,
	"KWD": 3,
	"LYD": 3,
	"OMR": 3,
	"TND": 3,
}

// DefaultExponent applies to every currency not in Exponents.
const DefaultExponent = 2

// Exponent returns how many decimal places a currency's minor unit implies.
//
// An unknown code gets the default rather than an error: rates arrive for
// hundreds of tickers, including metals and crypto, and refusing to display one
// because it is not in a table would be worse than showing it with two places.
func Exponent(currency string) int {
	if e, ok := Exponents[strings.ToUpper(currency)]; ok {
		return e
	}
	return DefaultExponent
}
