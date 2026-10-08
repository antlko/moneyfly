package db

import "fmt"

// ExportTxn is one live transaction, read directly off txn's generated
// columns plus the one field that is not one — note lives only in the JSON
// body, and a query that runs once per export is not a reason to add a
// column for it.
type ExportTxn struct {
	OccurredOn  string
	AccountID   string
	CategoryID  string
	AmountMinor int64
	Currency    string
	Note        string
	Kind        string
}

// TxnsForExport returns every live transaction for a user, oldest first —
// the order a spreadsheet or another importer expects a ledger in. Ties on a
// day fall back to the id, which is time-ordered (UUIDv7), so the same day's
// records come out in the order they were written, and identically every time.
func (d *DB) TxnsForExport(userID string) ([]ExportTxn, error) {
	rows, err := d.Query(`
		SELECT occurred_on, coalesce(account_id, ''), coalesce(category_id, ''),
		       amount_minor, currency, coalesce(json_extract(data, '$.note'), ''),
		       coalesce(kind, '')
		FROM txn
		WHERE user_id = ? AND deleted = 0
		ORDER BY occurred_on, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: reading transactions for export: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ExportTxn{}
	for rows.Next() {
		var t ExportTxn
		if err := rows.Scan(&t.OccurredOn, &t.AccountID, &t.CategoryID,
			&t.AmountMinor, &t.Currency, &t.Note, &t.Kind); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
