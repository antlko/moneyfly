package store

import (
	"errors"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func TestCategory_ArchiveKeepsHistory(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()
	hobby := h.categoryByName(u.ID, "Hobby")

	if err := svc.Archive(h.ctx, u.ID, hobby.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	active, err := svc.List(h.ctx, u.ID, "", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, c := range active {
		if c.ID == hobby.ID {
			t.Fatal("an archived category must leave the active list")
		}
	}
	// Still readable by id, so a historical transaction can resolve its category.
	got, err := svc.Get(h.ctx, u.ID, hobby.ID)
	if err != nil {
		t.Fatalf("an archived category must remain readable by id: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatal("archived_at was not set")
	}
	withArchived, err := svc.List(h.ctx, u.ID, "", true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(withArchived) != len(active)+1 {
		t.Fatalf("include_archived returned %d, want %d", len(withArchived), len(active)+1)
	}
}

func TestCategory_UniqueNameAmongActive(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()

	_, err := svc.Create(h.ctx, u.ID, category.Input{Name: "Food", Kind: category.KindExpense})
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("a duplicate active name must conflict, got %v", err)
	}

	// Archiving frees the name: the unique index covers non-archived rows only.
	food := h.categoryByName(u.ID, "Food")
	if err := svc.Archive(h.ctx, u.ID, food.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	again, err := svc.Create(h.ctx, u.ID, category.Input{Name: "Food", Kind: category.KindExpense})
	if err != nil {
		t.Fatalf("an archived name must be reusable: %v", err)
	}
	if again.ID == food.ID {
		t.Fatal("a new row was expected, not a revival")
	}
}

func TestCategory_Merge_RewritesAndLeavesAlias(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()
	txns := h.transactionService()

	utilities := h.categoryByName(u.ID, "Utilities")
	bills := h.categoryByName(u.ID, "Bills")
	cash := h.accountByName(u.ID, "Cash UAH")

	created, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(cash.ID, utilities.ID, "2026-07-15", 6500, "UAH"))
	if err != nil {
		t.Fatalf("creating a transaction to move: %v", err)
	}

	if err := svc.Merge(h.ctx, u.ID, utilities.ID, bills.ID); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	moved, err := txns.Get(h.ctx, u.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if moved.CategoryID == nil || *moved.CategoryID != bills.ID {
		t.Fatalf("the transaction was not moved to the target category: %+v", moved.CategoryID)
	}

	// The old name still resolves, so a later import of `Utilities` lands in Bills
	// rather than blocking again.
	resolved, err := svc.ResolveAlias(h.ctx, u.ID, category.SourceMonefy, "Utilities")
	if err != nil {
		t.Fatalf("ResolveAlias: %v", err)
	}
	if resolved == nil || resolved.ID != bills.ID {
		t.Fatalf("the merged name must alias to the target, got %+v", resolved)
	}

	gone, err := svc.Get(h.ctx, u.ID, utilities.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gone.ArchivedAt == nil {
		t.Fatal("the merged-away category must be archived")
	}
}

func TestCategory_MergeRejectsMixedKinds(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()

	salary := h.categoryByName(u.ID, "Salary")
	food := h.categoryByName(u.ID, "Food")
	if err := svc.Merge(h.ctx, u.ID, food.ID, salary.ID); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("merging an expense into an income category must be rejected, got %v", err)
	}
	if err := svc.Merge(h.ctx, u.ID, food.ID, food.ID); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("merging into itself must be rejected, got %v", err)
	}
}

func TestCategory_ParentCycleRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.categoryService()

	food := h.categoryByName(u.ID, "Food")
	eatingOut := h.categoryByName(u.ID, "Eating out")

	if _, err := svc.Update(h.ctx, u.ID, eatingOut.ID, category.Input{ParentID: &food.ID}); err != nil {
		t.Fatalf("setting a parent must work: %v", err)
	}
	// Food -> Eating out -> Food would be a cycle.
	if _, err := svc.Update(h.ctx, u.ID, food.ID, category.Input{ParentID: &eatingOut.ID}); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a parent cycle must be rejected, got %v", err)
	}
	if _, err := svc.Update(h.ctx, u.ID, food.ID, category.Input{ParentID: &food.ID}); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("self-parenting must be rejected, got %v", err)
	}
}

func TestAccount_ParentRejectsDirectValue(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.accountService()
	txns := h.transactionService()

	cash := h.accountByName(u.ID, "Cash") // computed parent of Cash USD/EUR/HUF/UAH
	if !cash.HasChildren {
		t.Fatal("Cash must be a computed parent")
	}
	food := h.categoryByName(u.ID, "Food")

	_, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(cash.ID, food.ID, "2026-07-15", 1250, "EUR"))
	if !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("posting to a computed parent must be rejected, got %v", err)
	}
	if _, err := svc.AssertPostable(h.ctx, u.ID, cash.ID); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("AssertPostable must refuse a parent, got %v", err)
	}

	// A leaf accepts the same posting.
	leaf := h.accountByName(u.ID, "Cash EUR")
	if _, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(leaf.ID, food.ID, "2026-07-15", 1250, "EUR")); err != nil {
		t.Fatalf("posting to a leaf must work: %v", err)
	}
}

func TestAccount_ParentCycleRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.accountService()

	cash := h.accountByName(u.ID, "Cash")
	cashEUR := h.accountByName(u.ID, "Cash EUR")

	// Cash -> Cash EUR -> Cash is a cycle.
	if _, err := svc.Update(h.ctx, u.ID, cash.ID, account.Input{ParentID: &cashEUR.ID}); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a parent cycle must be rejected, got %v", err)
	}
	if _, err := svc.Update(h.ctx, u.ID, cashEUR.ID, account.Input{ParentID: &cashEUR.ID}); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("self-parenting must be rejected, got %v", err)
	}
}

func TestAccount_ParentWithValuesRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.accountService()
	txns := h.transactionService()

	cashEUR := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")
	if _, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(cashEUR.ID, food.ID, "2026-07-15", 1250, "EUR")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Cash EUR now holds a transaction, so it cannot become a computed parent:
	// a parent's value is the sum of its children and nothing of its own.
	fresh, err := svc.Create(h.ctx, u.ID, account.Input{
		Name: "Cash EUR Wallet", AssetClass: account.ClassCash, Currency: "EUR",
		IsLiquid: true, CountsTowardNetWorth: true, ParentID: &cashEUR.ID,
	})
	if !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("an account holding values must not become a parent, got %v (%+v)", err, fresh)
	}
}

func TestAccount_CreateValidates(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.accountService()

	for name, in := range map[string]account.Input{
		"empty name":     {Name: "  ", AssetClass: account.ClassCash, Currency: "EUR"},
		"bad class":      {Name: "X", AssetClass: account.AssetClass("gold-bar"), Currency: "EUR"},
		"bad currency":   {Name: "X", AssetClass: account.ClassCash, Currency: "ZZZ"},
		"negative basis": {Name: "X", AssetClass: account.ClassCash, Currency: "EUR", CostBasisMinor: ptrInt64(-1)},
	} {
		if _, err := svc.Create(h.ctx, u.ID, in); !errors.Is(err, apperr.ErrValidation) {
			t.Errorf("%s must be rejected, got %v", name, err)
		}
	}

	created, err := svc.Create(h.ctx, u.ID, account.Input{
		Name: "Cash PLN", AssetClass: account.ClassCash, Currency: "eur", IsLiquid: true, CountsTowardNetWorth: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Currency != "EUR" {
		t.Fatalf("currency = %q, want normalised EUR", created.Currency)
	}
}

func TestUserIsolation_Taxonomy(t *testing.T) {
	h := newHarness(t)
	a := h.user("a@example.test")
	b := h.user("b@example.test")

	cats := h.categoryService()
	accounts := h.accountService()

	bFood := h.categoryByName(b.ID, "Food")
	bAccount := h.accountByName(b.ID, "Cash EUR")

	// Reads.
	if _, err := cats.Get(h.ctx, a.ID, bFood.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not read user B's category, got %v", err)
	}
	if _, err := accounts.Get(h.ctx, a.ID, bAccount.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not read user B's account, got %v", err)
	}
	// Mutations.
	if _, err := cats.Update(h.ctx, a.ID, bFood.ID, category.Input{Name: "Hijacked"}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not rename user B's category, got %v", err)
	}
	if err := cats.Archive(h.ctx, a.ID, bFood.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not archive user B's category, got %v", err)
	}
	if _, err := accounts.Update(h.ctx, a.ID, bAccount.ID, account.Input{Name: "Hijacked"}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not rename user B's account, got %v", err)
	}
	if err := accounts.Archive(h.ctx, a.ID, bAccount.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not archive user B's account, got %v", err)
	}
	// Aliases.
	if _, err := cats.CreateAlias(h.ctx, a.ID, bFood.ID, "monefy", "Hijack"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not alias to user B's category, got %v", err)
	}
	if _, err := accounts.CreateAlias(h.ctx, a.ID, bAccount.ID, "monefy", "Hijack"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not alias to user B's account, got %v", err)
	}
	// And B is untouched.
	if got, err := cats.Get(h.ctx, b.ID, bFood.ID); err != nil || got.Name != "Food" {
		t.Errorf("user B's category was disturbed: %+v %v", got, err)
	}
}

func ptrInt64(v int64) *int64 { return &v }
