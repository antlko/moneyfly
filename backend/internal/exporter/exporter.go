// Package exporter renders transactions as CSV in one of several shapes.
//
// Each shape is a self-registering Profile (profile_native.go,
// profile_monefy.go) — see docs/ARCHITECTURE.md "Where to make a change" for
// adding another one. This mirrors internal/fx's provider chain: the
// selection is a string id resolved through a small registry, not a switch
// statement callers have to keep in sync with what exists.
package exporter

import (
	"io"
	"sort"
)

// Row is one transaction, in the shape every profile renders from.
type Row struct {
	OccurredOn   string // YYYY-MM-DD
	AccountName  string
	CategoryName string
	// AmountMinor is signed — negative for an expense, positive for income —
	// the same convention the stored row itself uses (RecordView.vue), so
	// Kind is derived from it rather than carried alongside as a second
	// value that could disagree with the sign.
	AmountMinor int64
	Currency    string
	Note        string
	// Transfer marks a move between two of the user's own accounts. A
	// transfer is neither spending nor income, so a profile whose consumers
	// read the sign as one or the other (monefy) leaves it out.
	Transfer bool
	// ConvertedMinor is AmountMinor in ConvertedCurrency — the user's base
	// currency, priced at the rate on OccurredOn. When no rate is known it is
	// the original amount and currency, unconverted, rather than a guess.
	ConvertedMinor    int64
	ConvertedCurrency string
}

// Kind derives expense/income from the sign, the same rule the Monefy CSV
// format itself uses (docs/MONEFY-PARITY.md §5) and the reason this app
// stores expenses negative in the first place.
func (r Row) Kind() string {
	if r.AmountMinor < 0 {
		return "expense"
	}
	return "income"
}

// Profile writes a full CSV — header included — for one destination shape.
type Profile interface {
	ID() string
	Write(w io.Writer, rows []Row) error
}

var profiles = map[string]Profile{}

// register is called from each profile's init(), never from outside this
// package.
func register(p Profile) {
	if _, exists := profiles[p.ID()]; exists {
		panic("exporter: profile " + p.ID() + " registered twice")
	}
	profiles[p.ID()] = p
}

// Get returns a profile by id.
func Get(id string) (Profile, bool) {
	p, ok := profiles[id]
	return p, ok
}

// IDs lists every registered profile, sorted, for an error message that
// tells the caller what it could have asked for instead.
func IDs() []string {
	ids := make([]string, 0, len(profiles))
	for id := range profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
