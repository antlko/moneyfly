package importer

import "strings"

// NamedRow is an existing category or account, the shape a CSV name is
// matched against. Kind is only meaningful for categories ("expense" |
// "income"); Currency is only meaningful for accounts — each is empty on
// the other.
type NamedRow struct {
	ID       string
	Name     string
	Kind     string
	Currency string
}

// categoryAlias maps a handful of category names verified to have changed
// across real Monefy exports — see the table in docs/MONEFY-PARITY.md §5 —
// to the name this app's own default categories use.
//
// It stays deliberately small. Everything not listed here, including every
// non-English name (older exports are Russian: "Наличные", "Счета" —
// docs/MONEFY-PARITY.md §5), surfaces in the mapping step instead of being
// guessed at. A wrong guess here is worse than asking: it fails exactly like
// a correct match, silently, and nobody notices until a total is wrong.
// Keys are normalised (normalize below); values are canonical category names,
// also normalised.
var categoryAlias = map[string]string{
	"hoteltrip": "hotel/trip",
	"clouth":    "clothes",
	"studing":   "studying",
	"sport":     "sports",
}

// normalize is the comparison this package uses everywhere a CSV name meets
// an existing one: trimmed and case-folded, because "Studing " (a real
// example — note the trailing space) must still find "Studying".
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Resolution is the outcome of matching one CSV name against what already
// exists. ID is empty when nothing matched — the row blocks rather than
// falling back to anything, per the package doc.
type Resolution struct {
	Name     string
	ID       string
	ViaAlias bool
	// CurrencyMismatch is set when an account name matched but no account of
	// that name has the row's currency — never set for categories, which have
	// no currency to disagree on.
	CurrencyMismatch bool
}

func (r Resolution) Resolved() bool { return r.ID != "" }

// CategoryKey and AccountKey are how a CSV name is identified in the mapping
// the client sends back.
//
// A bare name is not an identity. One export can use "Gifts" as both an expense
// and an income category, and one account name can appear in two currencies —
// each of those is two distinct things that resolve against different existing
// rows, and a name-keyed map cannot tell them apart. Mapping one then silently
// mapped the other, onto a category of the wrong kind or an account in the
// wrong currency: rows written to the wrong place rather than skipped, which is
// the failure the import is otherwise careful to avoid.
//
// The scoping half — kind for categories, currency for accounts — matches what
// the resolvers below already use to *match*, so preview and commit agree by
// construction.
func CategoryKey(name, kind string) string { return kind + ":" + name }

func AccountKey(name, currency string) string { return currency + ":" + name }

// override looks up the composite key first and falls back to the bare name,
// so a caller written against the older name-keyed shape keeps working.
func override(overrides map[string]string, key, name string) string {
	if id, ok := overrides[key]; ok && id != "" {
		return id
	}
	if id, ok := overrides[name]; ok && id != "" {
		return id
	}
	return ""
}

// ResolveCategory finds the existing category id a CSV category name refers
// to, scoped to kind so an expense row can never match an income category of
// the same name. Checked in order: an explicit override (the operator's own
// mapping, from a previous unresolved run), an exact case-insensitive match,
// then the small alias table above. It never creates a category — see the
// package doc for why an unmatched name blocks instead.
func ResolveCategory(name, kind string, existing []NamedRow, overrides map[string]string) Resolution {
	if id := override(overrides, CategoryKey(name, kind), name); id != "" {
		return Resolution{Name: name, ID: id}
	}
	norm := normalize(name)
	for _, c := range existing {
		if c.Kind == kind && normalize(c.Name) == norm {
			return Resolution{Name: name, ID: c.ID}
		}
	}
	if canon, ok := categoryAlias[norm]; ok {
		for _, c := range existing {
			if c.Kind == kind && normalize(c.Name) == canon {
				return Resolution{Name: name, ID: c.ID, ViaAlias: true}
			}
		}
	}
	return Resolution{Name: name}
}

// ResolveAccount is ResolveCategory without the kind scope — an account has
// none — and with no alias table: the documented renames are category names
// only. In its place it is scoped by currency: a CSV account name that
// matches an existing account by name only, not currency, is not resolved.
// Two wallets can coincidentally share a name (accounts in the reference
// export are just currency codes — "EUR", "HUF" — so this is exactly what a
// rename, or a second wallet opened in a new currency, produces), and
// importing silently into the wrong one would misattribute every one of its
// rows to a balance in a currency they were never recorded in. An explicit
// override always wins regardless of currency — that is the operator seeing
// the mismatch and choosing anyway.
func ResolveAccount(name, currency string, existing []NamedRow, overrides map[string]string) Resolution {
	if id := override(overrides, AccountKey(name, currency), name); id != "" {
		return Resolution{Name: name, ID: id}
	}
	norm := normalize(name)
	mismatch := false
	for _, a := range existing {
		if normalize(a.Name) != norm {
			continue
		}
		if a.Currency == currency {
			return Resolution{Name: name, ID: a.ID}
		}
		mismatch = true
	}
	return Resolution{Name: name, CurrencyMismatch: mismatch}
}
