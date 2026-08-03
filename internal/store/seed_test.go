package store

import (
	"testing"

	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/seed"
)

func TestSeed_CategoryCount(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	cats, err := h.Categories.List(h.ctx, u.ID, "", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cats) != seed.CategoryCount {
		t.Fatalf("seeded %d categories, want %d", len(cats), seed.CategoryCount)
	}
	names := map[string]bool{}
	for _, c := range cats {
		names[c.Name] = true
	}
	// The two that had no spreadsheet row, where 120 transactions went missing.
	for _, want := range []string{"Utilities", "Taxi"} {
		if !names[want] {
			t.Errorf("%q must be a real category, not an orphan", want)
		}
	}
	// Corrected canonical spellings.
	for _, want := range []string{"Appliances", "Toiletry"} {
		if !names[want] {
			t.Errorf("canonical spelling %q missing", want)
		}
	}
	for _, unwanted := range []string{"Applience", "Toilery"} {
		if names[unwanted] {
			t.Errorf("%q is a misspelling and must exist only as an alias", unwanted)
		}
	}
	// Workbook order is preserved: House is first, Services last of the 18.
	if cats[0].Name != "House" {
		t.Errorf("first category = %q, want House (workbook row 9)", cats[0].Name)
	}
	if cats[17].Name != "Services" {
		t.Errorf("18th category = %q, want Services (workbook row 26)", cats[17].Name)
	}
}

func TestSeed_EssentialFlags(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	cats, err := h.Categories.List(h.ctx, u.ID, "", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Exactly the membership of the workbook's B28 formula.
	want := map[string]bool{
		"House": true, "Food": true, "Eating out": true, "Hobby": true, "Clothes": true,
		"Health": true, "Transport": true, "Sport": true, "Bills": true, "Services": true,
		"Toiletry": true, "Communications": true, "Studying": true,
	}
	got := map[string]bool{}
	for _, c := range cats {
		if c.IsEssential {
			got[c.Name] = true
		}
	}
	if len(got) != seed.EssentialCount {
		t.Fatalf("%d essential categories, want %d: %v", len(got), seed.EssentialCount, got)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("%q must be essential", name)
		}
	}
	// The five B28 excluded: Appliances, Hotel/Trip, Family, Gifts, Entertainment.
	for _, name := range []string{"Appliances", "Hotel/Trip", "Family", "Gifts", "Entertainment"} {
		if got[name] {
			t.Errorf("%q is excluded from B28 and must not be essential", name)
		}
	}
}

func TestResolveAlias_KnownVariants(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()

	// Every name observed in the real export, plus the older Russian one. These are
	// the seven that resolved to zero in the old pipeline, costing 237 rows.
	cases := map[string]string{
		"Utilities":      "Utilities",
		"HotelTrip":      "Hotel/Trip",
		"Hotel/Trip":     "Hotel/Trip",
		"Communication":  "Communications",
		"Communications": "Communications",
		"Clouth":         "Clothes",
		"Clothes":        "Clothes",
		"Studing":        "Studying",
		"Studying":       "Studying",
		"Studying ":      "Studying",
		"Sport":          "Sport",
		"Sports":         "Sport",
		"Taxi":           "Taxi",
		"Toiletry":       "Toiletry",
		"Toilery":        "Toiletry",
		"Applience":      "Appliances",
		"Appliance":      "Appliances",
		"Family":         "Family",
		"Family ":        "Family",
		"Счета":          "Bills",
		"Food":           "Food",
		"Eating out":     "Eating out",
		"House":          "House",
		"Health":         "Health",
		"Transport":      "Transport",
		"Bills":          "Bills",
		"Services":       "Services",
		"Gifts":          "Gifts",
		"Entertainment":  "Entertainment",
		"Hobby":          "Hobby",
	}
	for sourceName, canonical := range cases {
		got, err := svc.ResolveAlias(h.ctx, u.ID, category.SourceMonefy, sourceName)
		if err != nil {
			t.Fatalf("ResolveAlias(%q): %v", sourceName, err)
		}
		if got == nil {
			t.Errorf("ResolveAlias(%q) is unmapped; it must resolve to %q with no manual decision", sourceName, canonical)
			continue
		}
		if got.Name != canonical {
			t.Errorf("ResolveAlias(%q) = %q, want %q", sourceName, got.Name, canonical)
		}
	}
}

func TestResolveAlias_Unknown_ReturnsNilNil(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()

	got, err := svc.ResolveAlias(h.ctx, u.ID, category.SourceMonefy, "Крипта")
	if err != nil {
		t.Fatalf("an unknown name is an expected outcome, not an error: %v", err)
	}
	if got != nil {
		t.Fatalf("unknown name resolved to %q; it must never be auto-created", got.Name)
	}
	// And nothing was created as a side effect.
	cats, err := h.Categories.List(h.ctx, u.ID, "", true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cats) != seed.CategoryCount {
		t.Fatalf("resolving an unknown name created a category: now %d", len(cats))
	}
}

func TestResolveAlias_IsCaseAndWhitespaceExact(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()

	// Fuzzy matching belongs to the importer and only ever proposes. The alias
	// lookup itself is exact, because raw text drives the natural key.
	for _, name := range []string{"food", "FOOD", " Food", "Foods"} {
		got, err := svc.ResolveAlias(h.ctx, u.ID, category.SourceMonefy, name)
		if err != nil {
			t.Fatalf("ResolveAlias(%q): %v", name, err)
		}
		if got != nil {
			t.Errorf("ResolveAlias(%q) resolved to %q; the lookup must be exact", name, got.Name)
		}
	}
}

func TestSeed_Accounts(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")

	accounts, err := h.Accounts.List(h.ctx, u.ID, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byName := map[string]struct {
		liquid, counts, computed bool
		class, currency          string
		ticker                   string
	}{}
	for _, a := range accounts {
		ticker := ""
		if a.PriceTicker != nil {
			ticker = *a.PriceTicker
		}
		byName[a.Name] = struct {
			liquid, counts, computed bool
			class, currency          string
			ticker                   string
		}{a.IsLiquid, a.CountsTowardNetWorth, a.HasChildren, string(a.AssetClass), a.Currency, ticker}
	}

	// Workbook rows 36-69, appendix §A.4.
	for _, tc := range []struct {
		name     string
		class    string
		currency string
		liquid   bool
		counts   bool
		ticker   string
	}{
		{"Gold", "metal", "EUR", false, true, "XAU"},
		{"Cash USD", "cash", "USD", true, true, ""},
		{"Cash EUR", "cash", "EUR", true, true, ""},
		{"Cash HUF", "cash", "HUF", true, true, ""},
		{"Cash UAH", "cash", "UAH", true, true, ""},
		{"Banks USD", "bank", "USD", true, true, ""},
		{"Banks EUR", "bank", "EUR", true, true, ""},
		{"Banks HUF", "bank", "HUF", true, true, ""},
		{"Banks UAH", "bank", "UAH", true, true, ""},
		{"Banks FOP", "bank", "UAH", true, true, ""},
		{"Deposits USD", "deposit", "USD", false, true, ""},
		{"Deposits EUR", "deposit", "EUR", false, true, ""},
		{"Invests", "investment", "EUR", false, true, ""},
		{"CSGO Skins", "other", "EUR", false, true, ""},
		{"Ton", "crypto", "EUR", false, true, ""},
		{"USDT (EUR)", "crypto", "EUR", false, true, "USDT"},
	} {
		got, ok := byName[tc.name]
		if !ok {
			t.Errorf("account %q missing", tc.name)
			continue
		}
		if got.class != tc.class || got.currency != tc.currency ||
			got.liquid != tc.liquid || got.counts != tc.counts || got.ticker != tc.ticker {
			t.Errorf("account %q = %+v, want class=%s currency=%s liquid=%v counts=%v ticker=%q",
				tc.name, got, tc.class, tc.currency, tc.liquid, tc.counts, tc.ticker)
		}
	}

	// Cash, Banks and Deposits were computed parents in the sheet; here they are
	// real parents whose value is a sum, so a child can never be forgotten.
	for _, parent := range []string{"Cash", "Banks", "Deposits"} {
		got, ok := byName[parent]
		if !ok {
			t.Errorf("parent account %q missing", parent)
			continue
		}
		if !got.computed {
			t.Errorf("%q must have children and therefore be computed", parent)
		}
	}
	// Banks FOP was omitted from Banks (row 41) while being included in General
	// (row 55). As an ordinary child it is now in both.
	fop := h.accountByName(u.ID, "Banks FOP")
	banks := h.accountByName(u.ID, "Banks")
	if fop.ParentID == nil || *fop.ParentID != banks.ID {
		t.Error("Banks FOP must be an ordinary child of Banks")
	}
}

func TestSeed_AccountAliases(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.accountService()

	// Monefy names accounts after currencies; the older exports are Russian.
	for sourceName, want := range map[string]string{
		"UAH":       "Cash UAH",
		"EUR":       "Cash EUR",
		"HUF":       "Cash HUF",
		"Наличные":  "Cash UAH",
		"Cash EUR":  "Cash EUR",
		"Banks FOP": "Banks FOP",
	} {
		got, err := svc.ResolveAlias(h.ctx, u.ID, "monefy", sourceName)
		if err != nil {
			t.Fatalf("ResolveAlias(%q): %v", sourceName, err)
		}
		if got == nil {
			t.Errorf("account alias %q is unmapped, want %q", sourceName, want)
			continue
		}
		if got.Name != want {
			t.Errorf("account alias %q = %q, want %q", sourceName, got.Name, want)
		}
	}
}

func TestSeed_IsPerUser(t *testing.T) {
	h := newHarness(t)
	a := h.user("a@example.test")
	b := h.user("b@example.test")

	aCats, err := h.Categories.List(h.ctx, a.ID, "", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	bCats, err := h.Categories.List(h.ctx, b.ID, "", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(aCats) != seed.CategoryCount || len(bCats) != seed.CategoryCount {
		t.Fatalf("each user gets their own taxonomy: %d and %d", len(aCats), len(bCats))
	}
	// Distinct rows, so renaming one user's category cannot affect the other's.
	if aCats[0].ID == bCats[0].ID {
		t.Fatal("two users share a category row")
	}
}
