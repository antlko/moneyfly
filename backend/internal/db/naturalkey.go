package db

import "fmt"

// TxnNaturalKeys returns every natural key already recorded for a user, live
// or deleted, as a set.
//
// The CSV importer uses this instead of checking one key at a time
// (TxnNaturalKeyExists, in recurring.go): an import is thousands of rows in
// one request, and one query over idx_txn_natural_key followed by in-memory
// lookups is both simpler and faster than thousands of round trips for what
// is otherwise the identical check — has this natural key ever existed for
// this user — that the recurring worker makes per rule, per tick.
func (d *DB) TxnNaturalKeys(userID string) (map[string]bool, error) {
	rows, err := d.Query(
		`SELECT natural_key FROM txn WHERE user_id = ? AND natural_key IS NOT NULL`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: reading natural keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}
