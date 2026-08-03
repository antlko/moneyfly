package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

func TestCreate_ConvertsToBaseCurrency(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()

	huf := h.accountByName(u.ID, "Cash HUF")
	comms := h.categoryByName(u.ID, "Communications")

	got, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(huf.ID, comms.ID, "2026-07-15", 4000, "HUF"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Amount.Minor != 4000 || got.Amount.Currency != "HUF" {
		t.Fatalf("native amount = %+v, want 4000 HUF", got.Amount)
	}
	if got.BaseAmount == nil {
		t.Fatal("base amount must be computed on write")
	}
	if got.BaseAmount.Minor != 1120 || got.BaseAmount.Currency != "EUR" {
		t.Fatalf("base amount = %+v, want 1120 EUR minor units", got.BaseAmount)
	}
	if got.FxRateID == nil {
		t.Fatal("the rate used must be recorded, so the figure is traceable")
	}
	if got.Occurrence != 1 {
		t.Fatalf("occurrence = %d, want 1", got.Occurrence)
	}
	if got.NaturalKey == "" {
		t.Fatal("the natural key must be computed on write")
	}
}

func TestCreate_NoRate_StoresNullBase(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()

	huf := h.accountByName(u.ID, "Cash HUF")
	comms := h.categoryByName(u.ID, "Communications")

	// Before every stored rate: the write must still succeed. Losing the
	// transaction would be worse than not knowing its euro value yet.
	got, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(huf.ID, comms.ID, "2019-03-04", 4000, "HUF"))
	if err != nil {
		t.Fatalf("a missing rate must not block the write: %v", err)
	}
	if got.BaseAmount != nil {
		t.Fatalf("base amount = %+v, want nil", got.BaseAmount)
	}
	if got.FxRateID != nil {
		t.Fatal("no rate id should be recorded")
	}
}

func TestCreate_ValidatesKindAgainstCategory(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")
	salary := h.categoryByName(u.ID, "Salary")

	// An expense cannot be filed under an income category.
	in := newExpense(acct.ID, salary.ID, "2026-07-15", 1250, "EUR")
	if _, err := txns.Create(h.ctx, u.ID, "EUR", in); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("expense in an income category must be rejected, got %v", err)
	}
	// And income cannot be filed under an expense category.
	income := newExpense(acct.ID, food.ID, "2026-07-15", 1250, "EUR")
	income.Kind = transaction.KindIncome
	if _, err := txns.Create(h.ctx, u.ID, "EUR", income); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("income in an expense category must be rejected, got %v", err)
	}
	// The right combination works.
	income.CategoryID = &salary.ID
	if _, err := txns.Create(h.ctx, u.ID, "EUR", income); err != nil {
		t.Fatalf("income in an income category must work: %v", err)
	}
}

func TestCreate_RejectsMismatchedCurrency(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()

	eur := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	// A euro cash account cannot hold a forint expense: the native amount is in
	// the account's currency by definition.
	in := newExpense(eur.ID, food.ID, "2026-07-15", 4000, "HUF")
	if _, err := txns.Create(h.ctx, u.ID, "EUR", in); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a currency mismatch must be rejected, got %v", err)
	}
}

func TestCreate_RejectsZeroAndNegativeAmounts(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	for _, minor := range []int64{0, -1250} {
		in := newExpense(acct.ID, food.ID, "2026-07-15", minor, "EUR")
		if _, err := txns.Create(h.ctx, u.ID, "EUR", in); !errors.Is(err, apperr.ErrValidation) {
			t.Errorf("amount %d must be rejected, got %v", minor, err)
		}
	}
}

func TestOccurrence_TwoIdenticalRowsGetOneAndTwo(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	coffee := h.categoryByName(u.ID, "Eating out")

	in := newExpense(acct.ID, coffee.ID, "2026-07-15", 350, "EUR")
	first, err := txns.Create(h.ctx, u.ID, "EUR", in)
	if err != nil {
		t.Fatalf("first coffee: %v", err)
	}
	second, err := txns.Create(h.ctx, u.ID, "EUR", in)
	if err != nil {
		t.Fatalf("the second identical coffee must be storable: %v", err)
	}
	if first.NaturalKey != second.NaturalKey {
		t.Fatal("identical rows must share a natural key")
	}
	if first.Occurrence != 1 || second.Occurrence != 2 {
		t.Fatalf("occurrences = %d and %d, want 1 and 2", first.Occurrence, second.Occurrence)
	}
}

func TestOccurrence_ConcurrentInsert(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	coffee := h.categoryByName(u.ID, "Eating out")
	in := newExpense(acct.ID, coffee.ID, "2026-07-15", 350, "EUR")

	// The occurrence is assigned by a subquery inside the INSERT, so two writers
	// cannot both read the same count and collide.
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []transaction.Transaction
		errs    []error
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := txns.Create(h.ctx, u.ID, "EUR", in)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			results = append(results, got)
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("concurrent inserts of the same key must both succeed: %v", errs)
	}
	if len(results) != 2 {
		t.Fatalf("%d rows created, want 2", len(results))
	}
	got := map[int]bool{results[0].Occurrence: true, results[1].Occurrence: true}
	if !got[1] || !got[2] {
		t.Fatalf("occurrences = %d and %d, want 1 and 2", results[0].Occurrence, results[1].Occurrence)
	}
}

func TestTransfer_CreatesPairAtomically(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()

	from := h.accountByName(u.ID, "Cash EUR")
	to := h.accountByName(u.ID, "Banks EUR")

	pair, err := txns.CreateTransfer(h.ctx, u.ID, "EUR", transaction.TransferInput{
		FromAccountID: from.ID, ToAccountID: to.ID,
		OccurredOn: mustDate("2026-07-15"), Amount: money.New(50000, "EUR"),
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if len(pair) != 2 {
		t.Fatalf("%d rows created, want 2", len(pair))
	}
	kinds := map[transaction.Kind]int64{}
	for _, tr := range pair {
		kinds[tr.Kind] = tr.AccountID
		if tr.CategoryID != nil {
			t.Error("a transfer must never carry a category")
		}
		if tr.TransferGroupID == nil {
			t.Error("both halves must share a transfer_group_id")
		}
	}
	if kinds[transaction.KindTransferOut] != from.ID || kinds[transaction.KindTransferIn] != to.ID {
		t.Fatalf("the pair is mis-assigned: %+v", kinds)
	}
	if *pair[0].TransferGroupID != *pair[1].TransferGroupID {
		t.Fatal("the group ids differ")
	}
}

func TestTransfer_RejectsCrossCurrencyAndSelf(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()

	eur := h.accountByName(u.ID, "Cash EUR")
	huf := h.accountByName(u.ID, "Cash HUF")

	_, err := txns.CreateTransfer(h.ctx, u.ID, "EUR", transaction.TransferInput{
		FromAccountID: eur.ID, ToAccountID: huf.ID,
		OccurredOn: mustDate("2026-07-15"), Amount: money.New(50000, "EUR"),
	})
	if !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a cross-currency transfer needs a rate decision and must be refused, got %v", err)
	}
	_, err = txns.CreateTransfer(h.ctx, u.ID, "EUR", transaction.TransferInput{
		FromAccountID: eur.ID, ToAccountID: eur.ID,
		OccurredOn: mustDate("2026-07-15"), Amount: money.New(50000, "EUR"),
	})
	if !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a transfer to the same account must be refused, got %v", err)
	}
}

func TestTransfer_DeletingOneDeletesBoth(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()

	from := h.accountByName(u.ID, "Cash EUR")
	to := h.accountByName(u.ID, "Banks EUR")
	pair, err := txns.CreateTransfer(h.ctx, u.ID, "EUR", transaction.TransferInput{
		FromAccountID: from.ID, ToAccountID: to.ID,
		OccurredOn: mustDate("2026-07-15"), Amount: money.New(50000, "EUR"),
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	if err := txns.Delete(h.ctx, u.ID, pair[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, tr := range pair {
		if _, err := txns.Get(h.ctx, u.ID, tr.ID); !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("half a transfer is not a fact: row %d must also be deleted (%v)", tr.ID, err)
		}
	}
}

func TestTransfer_ExcludedFromCategoryTotals(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()

	from := h.accountByName(u.ID, "Cash EUR")
	to := h.accountByName(u.ID, "Banks EUR")
	food := h.categoryByName(u.ID, "Food")

	if _, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(from.ID, food.ID, "2026-07-15", 1250, "EUR")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := txns.CreateTransfer(h.ctx, u.ID, "EUR", transaction.TransferInput{
		FromAccountID: from.ID, ToAccountID: to.ID,
		OccurredOn: mustDate("2026-07-15"), Amount: money.New(50000, "EUR"),
	}); err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	actuals, err := h.Txn.PeriodActuals(h.ctx, u.ID, period.Period("2026-07"), "EUR")
	if err != nil {
		t.Fatalf("PeriodActuals: %v", err)
	}
	// Moving money between your own accounts is not spending.
	if actuals.SpendTotal.Minor != 1250 {
		t.Fatalf("spend total = %d, want 1250 — the transfer must not be counted", actuals.SpendTotal.Minor)
	}
}

func TestSoftDelete_ExcludedFromReports(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	created, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(acct.ID, food.ID, "2026-07-15", 1250, "EUR"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := txns.Delete(h.ctx, u.ID, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	actuals, err := h.Txn.PeriodActuals(h.ctx, u.ID, period.Period("2026-07"), "EUR")
	if err != nil {
		t.Fatalf("PeriodActuals: %v", err)
	}
	if actuals.Recorded {
		t.Fatal("a month whose only transaction was deleted has no recorded data")
	}
	// The row is still in the database: nothing is ever destroyed.
	var n int
	if err := h.Txn.db.QueryRow(
		`SELECT count(*) FROM transaction_entry WHERE user_id = ? AND id = ? AND deleted_at IS NOT NULL`,
		u.ID, created.ID).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Fatal("the row must remain in the database, soft-deleted")
	}
}

func TestList_FiltersAndCursorPagination(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")
	transport := h.categoryByName(u.ID, "Transport")

	for i, spec := range []struct {
		category int64
		date     string
		desc     string
	}{
		{food.ID, "2026-07-01", "Groceries at the market"},
		{food.ID, "2026-07-05", "Bakery"},
		{transport.ID, "2026-07-10", "Monthly pass"},
		{food.ID, "2026-06-20", "Last month groceries"},
	} {
		in := newExpense(acct.ID, spec.category, spec.date, int64(100+i), "EUR")
		desc := spec.desc
		in.Description = &desc
		if _, err := txns.Create(h.ctx, u.ID, "EUR", in); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	// Date range.
	from, to := mustDate("2026-07-01"), mustDate("2026-07-31")
	page, err := txns.List(h.ctx, u.ID, transaction.Filter{From: &from, To: &to})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("%d rows in July, want 3", len(page.Items))
	}
	// Newest first.
	if page.Items[0].OccurredOn.Format(DateLayout) != "2026-07-10" {
		t.Fatalf("first row = %s, want the newest", page.Items[0].OccurredOn.Format(DateLayout))
	}

	// Category filter.
	page, err = txns.List(h.ctx, u.ID, transaction.Filter{CategoryID: &transport.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].CategoryName != "Transport" {
		t.Fatalf("category filter returned %d rows", len(page.Items))
	}

	// Search over description.
	page, err = txns.List(h.ctx, u.ID, transaction.Filter{Query: "grocer"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("search returned %d rows, want 2", len(page.Items))
	}

	// Cursor pagination walks every row exactly once.
	seen := map[int64]bool{}
	filter := transaction.Filter{Limit: 2}
	for {
		page, err := txns.List(h.ctx, u.ID, filter)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("row %d returned twice", item.ID)
			}
			seen[item.ID] = true
		}
		if !page.HasMore {
			break
		}
		filter.Cursor = *page.NextCursor
	}
	if len(seen) != 4 {
		t.Fatalf("pagination saw %d rows, want 4", len(seen))
	}
}

func TestList_CursorRoundTrip(t *testing.T) {
	original := transaction.Cursor{OccurredOn: mustDate("2026-07-15"), ID: 91, Set: true}
	encoded := transaction.EncodeCursor(original)
	decoded, err := transaction.DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !decoded.OccurredOn.Equal(original.OccurredOn) || decoded.ID != original.ID || !decoded.Set {
		t.Fatalf("round trip gave %+v, want %+v", decoded, original)
	}
	if _, err := transaction.DecodeCursor("not-base64!"); !errors.Is(err, apperr.ErrValidation) {
		t.Fatalf("a malformed cursor must be a validation error, got %v", err)
	}
	empty, err := transaction.DecodeCursor("")
	if err != nil || empty.Set {
		t.Fatalf("an empty cursor means start at the top: %+v %v", empty, err)
	}
}

func TestUpdate_RecomputesNaturalKeyAndBase(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	txns := h.transactionService()
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	created, err := txns.Create(h.ctx, u.ID, "EUR", newExpense(acct.ID, food.ID, "2026-07-15", 1250, "EUR"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	in := newExpense(acct.ID, food.ID, "2026-07-15", 2500, "EUR")
	updated, err := txns.Update(h.ctx, u.ID, created.ID, "EUR", in)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Amount.Minor != 2500 {
		t.Fatalf("amount = %d, want 2500", updated.Amount.Minor)
	}
	if updated.NaturalKey == created.NaturalKey {
		t.Fatal("identity follows content: changing the amount must change the natural key")
	}
	if updated.BaseAmount == nil || updated.BaseAmount.Minor != 2500 {
		t.Fatalf("base amount = %+v, want 2500", updated.BaseAmount)
	}
}

func TestUserIsolation_Transactions(t *testing.T) {
	h := newHarness(t)
	a := h.user("a@example.test")
	b := h.user("b@example.test")
	txns := h.transactionService()

	bAccount := h.accountByName(b.ID, "Cash EUR")
	bFood := h.categoryByName(b.ID, "Food")
	bTxn, err := txns.Create(h.ctx, b.ID, "EUR", newExpense(bAccount.ID, bFood.ID, "2026-07-15", 1250, "EUR"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := txns.Get(h.ctx, a.ID, bTxn.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not read user B's transaction, got %v", err)
	}
	if err := txns.Delete(h.ctx, a.ID, bTxn.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not delete user B's transaction, got %v", err)
	}
	if _, err := txns.Update(h.ctx, a.ID, bTxn.ID, "EUR",
		newExpense(bAccount.ID, bFood.ID, "2026-07-15", 9999, "EUR")); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not edit user B's transaction, got %v", err)
	}
	// A's list is empty; B's is not.
	aPage, err := txns.List(h.ctx, a.ID, transaction.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(aPage.Items) != 0 {
		t.Fatalf("user A sees %d of user B's transactions", len(aPage.Items))
	}
	// And B's row survived every attempt.
	if _, err := txns.Get(h.ctx, b.ID, bTxn.ID); err != nil {
		t.Fatalf("user B's transaction was damaged: %v", err)
	}
	// Posting to another user's account is refused.
	aFood := h.categoryByName(a.ID, "Food")
	if _, err := txns.Create(h.ctx, a.ID, "EUR",
		newExpense(bAccount.ID, aFood.ID, "2026-07-15", 1250, "EUR")); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("user A must not post to user B's account, got %v", err)
	}
}
