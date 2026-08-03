package store

import (
	"strings"
	"testing"
)

// These tests assert the DDL itself, not the Go around it. The constraints are the
// last line of defence: if the service layer is ever bypassed or refactored, the
// database still refuses the wrong shape.

// insertRaw writes a transaction_entry row directly, bypassing every service.
func insertRaw(t *testing.T, h *harness, userID, accountID int64, categoryID *int64,
	kind, naturalKey string, occurrence int) error {
	t.Helper()
	_, err := h.Txn.db.Exec(`
		INSERT INTO transaction_entry
			(user_id, account_id, category_id, occurred_on, kind, amount_minor, currency,
			 natural_key, occurrence, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		userID, accountID, categoryID, "2026-07-15", kind, 1250, "EUR",
		naturalKey, occurrence, ts(fixedNow), ts(fixedNow))
	return err
}

func TestDDL_TransferRejectsCategory(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	err := insertRaw(t, h, u.ID, acct.ID, &food.ID, "transfer_out", "key-transfer-with-category", 1)
	if err == nil {
		t.Fatal("a transfer with a category must violate the CHECK constraint")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "constraint") {
		t.Fatalf("expected a constraint violation, got %v", err)
	}
}

func TestDDL_ExpenseRequiresCategory(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Cash EUR")

	err := insertRaw(t, h, u.ID, acct.ID, nil, "expense", "key-expense-without-category", 1)
	if err == nil {
		t.Fatal("an expense with no category must violate the CHECK constraint")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "constraint") {
		t.Fatalf("expected a constraint violation, got %v", err)
	}
}

func TestDDL_NaturalKeyUniquePerOccurrence(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	if err := insertRaw(t, h, u.ID, acct.ID, &food.ID, "expense", "same-key", 1); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := insertRaw(t, h, u.ID, acct.ID, &food.ID, "expense", "same-key", 1)
	if err == nil {
		t.Fatal("the same natural key at the same occurrence must violate UNIQUE")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("expected a uniqueness violation, got %v", err)
	}
}

func TestDDL_SameKeyOccurrence2Allowed(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	// The two-coffees case: two byte-identical transactions on one day are two real
	// transactions, and both must survive. A plain content hash would lose one.
	if err := insertRaw(t, h, u.ID, acct.ID, &food.ID, "expense", "two-coffees", 1); err != nil {
		t.Fatalf("first coffee: %v", err)
	}
	if err := insertRaw(t, h, u.ID, acct.ID, &food.ID, "expense", "two-coffees", 2); err != nil {
		t.Fatalf("the second identical coffee must be storable at occurrence 2: %v", err)
	}

	var n int
	if err := h.Txn.db.QueryRow(
		`SELECT count(*) FROM transaction_entry WHERE user_id = ? AND natural_key = ?`,
		u.ID, "two-coffees").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("%d rows stored, want 2", n)
	}
}

func TestDDL_NaturalKeyUniquenessIgnoresDeleted(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	if err := insertRaw(t, h, u.ID, acct.ID, &food.ID, "expense", "revived", 1); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := h.Txn.db.Exec(
		`UPDATE transaction_entry SET deleted_at = ? WHERE user_id = ? AND natural_key = ?`,
		ts(fixedNow), u.ID, "revived"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// The unique index is partial (WHERE deleted_at IS NULL), so the same key can be
	// stored again once the original is deleted.
	if err := insertRaw(t, h, u.ID, acct.ID, &food.ID, "expense", "revived", 1); err != nil {
		t.Fatalf("re-inserting after a soft delete must be allowed: %v", err)
	}
}

func TestDDL_AmountMinorMustNotBeNegative(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")

	// The sign is carried by kind, never by the amount.
	_, err := h.Txn.db.Exec(`
		INSERT INTO transaction_entry
			(user_id, account_id, category_id, occurred_on, kind, amount_minor, currency,
			 natural_key, occurrence, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, acct.ID, food.ID, "2026-07-15", "expense", -1250, "EUR",
		"negative", 1, ts(fixedNow), ts(fixedNow))
	if err == nil {
		t.Fatal("a negative amount_minor must violate the CHECK constraint")
	}
}

func TestDDL_SnapshotRequiresExactlyOneOfValueOrQuantity(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Gold")

	// Neither.
	_, err := h.Txn.db.Exec(`
		INSERT INTO balance_snapshot (user_id, account_id, period_month, created_at, updated_at)
		VALUES (?,?,?,?,?)`, u.ID, acct.ID, "2026-07", ts(fixedNow), ts(fixedNow))
	if err == nil {
		t.Error("a snapshot with neither a value nor a quantity must be rejected")
	}
	// Both.
	_, err = h.Txn.db.Exec(`
		INSERT INTO balance_snapshot
			(user_id, account_id, period_month, amount_minor, currency, quantity_nano, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		u.ID, acct.ID, "2026-07", 300000, "EUR", 849000000, ts(fixedNow), ts(fixedNow))
	if err == nil {
		t.Error("a snapshot with both a value and a quantity must be rejected")
	}
	// Exactly one.
	if _, err := h.Txn.db.Exec(`
		INSERT INTO balance_snapshot
			(user_id, account_id, period_month, amount_minor, currency, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		u.ID, acct.ID, "2026-07", 300000, "EUR", ts(fixedNow), ts(fixedNow)); err != nil {
		t.Errorf("an asserted value must be accepted: %v", err)
	}
}

func TestDDL_PeriodMonthShapeEnforced(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	food := h.categoryByName(u.ID, "Food")

	_, err := h.Budgets.db.Exec(`
		INSERT INTO budget (user_id, category_id, period_month, planned_minor, currency, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		u.ID, food.ID, "2026-7", 30000, "EUR", ts(fixedNow), ts(fixedNow))
	if err == nil {
		t.Fatal("a malformed period_month must violate the CHECK constraint")
	}
}
