package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DueRule is one recurring_rule row ready to be materialised, carrying the
// version the worker's update must build on.
type DueRule struct {
	UserID   string
	ID       string
	Lamport  int64
	DeviceID string
	Data     string
}

// DueRecurringRules returns every live rule, across every user, whose next
// occurrence is on or before `today` (YYYY-MM-DD).
//
// Unlike every other query in this package, this one is not scoped to a
// single user — the worker that calls it runs once for the whole instance.
// Scoping stays structural even so: the caller dispatches each rule's ops
// through ApplyOps(rule.UserID, ...), the same entry point a push handler
// uses, so a rule can never write into another user's rows.
func (d *DB) DueRecurringRules(ctx context.Context, today string) ([]DueRule, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT user_id, id, lamport, device_id, data
		FROM recurring_rule
		WHERE deleted = 0 AND next_on <= ?
		ORDER BY next_on`, today)
	if err != nil {
		return nil, fmt.Errorf("db: reading due recurring rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []DueRule{}
	for rows.Next() {
		var r DueRule
		if err := rows.Scan(&r.UserID, &r.ID, &r.Lamport, &r.DeviceID, &r.Data); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TxnNaturalKeyExists reports whether a transaction carrying this natural key
// has ever existed for the user — live or already deleted.
//
// The recurring worker relies on this for its idempotence: it is what lets a
// restart between "the transaction was created" and "next_on was advanced"
// recover without a duplicate. Deleted rows count on purpose — if someone
// removed a materialised transaction, that is intent, and the next tick must
// not resurrect it under a new id. See naturalkey.go for the bulk form the
// CSV importer uses over the same index for the same reason.
func (d *DB) TxnNaturalKeyExists(ctx context.Context, userID, naturalKey string) (bool, error) {
	var exists int
	err := d.QueryRowContext(ctx, `
		SELECT 1 FROM txn WHERE user_id = ? AND natural_key = ? LIMIT 1`,
		userID, naturalKey).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("db: checking natural key %q: %w", naturalKey, err)
	}
	return true, nil
}
