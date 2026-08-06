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
}

// TxnsForExport returns every live transaction for a user, oldest first —
// the order a spreadsheet or another importer expects a ledger in.
func (d *DB) TxnsForExport(userID string) ([]ExportTxn, error) {
	rows, err := d.Query(`
		SELECT occurred_on, coalesce(account_id, ''), coalesce(category_id, ''),
		       amount_minor, currency, coalesce(json_extract(data, '$.note'), '')
		FROM txn
		WHERE user_id = ? AND deleted = 0
		ORDER BY occurred_on`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: reading transactions for export: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ExportTxn{}
	for rows.Next() {
		var t ExportTxn
		if err := rows.Scan(&t.OccurredOn, &t.AccountID, &t.CategoryID,
			&t.AmountMinor, &t.Currency, &t.Note); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
