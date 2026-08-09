package importer

import "testing"

// A CSV name is not an identity on its own. These are the cases where treating
// it as one wrote rows to the wrong place rather than skipping them — the one
// failure mode the importer is otherwise built to avoid.

func TestCategoryOverrideIsScopedToKind(t *testing.T) {
	existing := []NamedRow{}
	// One export, "Gifts" used both ways: money spent on presents, and money
	// received as one. Two different categories.
	overrides := map[string]string{
		CategoryKey("Gifts", "expense"): "cat:gifts-out",
		CategoryKey("Gifts", "income"):  "cat:gifts-in",
	}

	out := ResolveCategory("Gifts", "expense", existing, overrides)
	if out.ID != "cat:gifts-out" {
		t.Errorf("expense Gifts resolved to %q, want cat:gifts-out", out.ID)
	}
	in := ResolveCategory("Gifts", "income", existing, overrides)
	if in.ID != "cat:gifts-in" {
		t.Errorf("income Gifts resolved to %q, want cat:gifts-in", in.ID)
	}
}

// Mapping one kind must not drag the other along with it.
func TestCategoryOverrideForOneKindLeavesTheOtherUnresolved(t *testing.T) {
	overrides := map[string]string{CategoryKey("Gifts", "expense"): "cat:gifts-out"}

	if got := ResolveCategory("Gifts", "income", nil, overrides); got.Resolved() {
		t.Errorf("income Gifts resolved to %q on an expense-only mapping — "+
			"the row would be written to a category of the wrong kind", got.ID)
	}
}

func TestAccountOverrideIsScopedToCurrency(t *testing.T) {
	overrides := map[string]string{
		AccountKey("Cash", "EUR"): "acc:cash-eur",
		AccountKey("Cash", "HUF"): "acc:cash-huf",
	}

	if got := ResolveAccount("Cash", "EUR", nil, overrides); got.ID != "acc:cash-eur" {
		t.Errorf("EUR Cash resolved to %q, want acc:cash-eur", got.ID)
	}
	if got := ResolveAccount("Cash", "HUF", nil, overrides); got.ID != "acc:cash-huf" {
		t.Errorf("HUF Cash resolved to %q, want acc:cash-huf", got.ID)
	}
}

func TestAccountOverrideForOneCurrencyLeavesTheOtherUnresolved(t *testing.T) {
	overrides := map[string]string{AccountKey("Cash", "EUR"): "acc:cash-eur"}

	if got := ResolveAccount("Cash", "HUF", nil, overrides); got.Resolved() {
		t.Errorf("HUF Cash resolved to %q on a EUR-only mapping — the HUF rows "+
			"would land in a EUR wallet", got.ID)
	}
}

// The older name-keyed shape still works, so an existing scripted caller does
// not break on the day the keys changed.
func TestBareNameOverrideStillResolves(t *testing.T) {
	if got := ResolveCategory("Utilities", "expense", nil,
		map[string]string{"Utilities": "cat:utils"}); got.ID != "cat:utils" {
		t.Errorf("category resolved to %q, want cat:utils from the bare-name key", got.ID)
	}
	if got := ResolveAccount("Wallet", "EUR", nil,
		map[string]string{"Wallet": "acc:wallet"}); got.ID != "acc:wallet" {
		t.Errorf("account resolved to %q, want acc:wallet from the bare-name key", got.ID)
	}
}

// …but a composite key wins over one, so the precise answer is preferred when
// both are present.
func TestCompositeKeyWinsOverTheBareName(t *testing.T) {
	overrides := map[string]string{
		"Gifts":                        "cat:wrong",
		CategoryKey("Gifts", "income"): "cat:right",
	}
	if got := ResolveCategory("Gifts", "income", nil, overrides); got.ID != "cat:right" {
		t.Errorf("resolved to %q, want the composite key's cat:right", got.ID)
	}
}

// An empty mapping value is not a mapping — it must fall through to matching
// rather than resolving to "".
func TestEmptyOverrideDoesNotCountAsResolved(t *testing.T) {
	existing := []NamedRow{{ID: "cat:food", Name: "Food", Kind: "expense"}}
	overrides := map[string]string{CategoryKey("Food", "expense"): ""}

	if got := ResolveCategory("Food", "expense", existing, overrides); got.ID != "cat:food" {
		t.Errorf("resolved to %q, want the existing cat:food — an empty override "+
			"must not shadow a real match", got.ID)
	}
}

func TestKeysAreDistinctPerScope(t *testing.T) {
	if CategoryKey("Gifts", "expense") == CategoryKey("Gifts", "income") {
		t.Error("category keys collide across kinds")
	}
	if AccountKey("Cash", "EUR") == AccountKey("Cash", "HUF") {
		t.Error("account keys collide across currencies")
	}
}
