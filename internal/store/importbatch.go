package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// ImportRepo stores import_batch and import_row, and owns the one statement that
// turns an import into transactions. It implements importer.Repo.
type ImportRepo struct{ db *sql.DB }

// NewImportRepo builds the repository.
func NewImportRepo(db *sql.DB) *ImportRepo { return &ImportRepo{db: db} }

// keyChunk bounds the size of an IN list. SQLite caps bound parameters per
// statement, and the real export produces 1,683 keys in one call.
const keyChunk = 400

// batchSelect is a projection fragment; every call site appends a WHERE whose
// first predicate is `user_id = ?`. Unlike txnSelect it joins nothing, so there
// is no ON clause to carry the scoping and the detector cannot see it here.
// TestImportUserIsolation covers what the detector cannot.
//
// unscoped-query-ok: projection fragment, scoped by every caller
const batchSelect = `
	SELECT id, user_id, source, origin, COALESCE(filename,''), COALESCE(file_sha256,''),
	       COALESCE(stored_path,''), status, rows_total, rows_new, rows_duplicate,
	       rows_unmapped, rows_rejected, COALESCE(error,''), created_at, committed_at, reverted_at
	FROM import_batch`

func scanBatch(row interface{ Scan(...any) error }) (importer.Batch, error) {
	var (
		b           importer.Batch
		createdAt   string
		committedAt sql.NullString
		revertedAt  sql.NullString
	)
	err := row.Scan(&b.ID, &b.UserID, &b.Source, &b.Origin, &b.Filename, &b.FileSHA256,
		&b.StoredPath, &b.Status, &b.RowsTotal, &b.RowsNew, &b.RowsDuplicate,
		&b.RowsUnmapped, &b.RowsRejected, &b.Error, &createdAt, &committedAt, &revertedAt)
	if err != nil {
		return importer.Batch{}, err
	}
	if b.CreatedAt, err = parseTS(createdAt); err != nil {
		return importer.Batch{}, err
	}
	if b.CommittedAt, err = scanNullTime(committedAt); err != nil {
		return importer.Batch{}, err
	}
	if b.RevertedAt, err = scanNullTime(revertedAt); err != nil {
		return importer.Batch{}, err
	}
	return b, nil
}

// CreateBatch records an upload.
func (r *ImportRepo) CreateBatch(ctx context.Context, b importer.Batch, now time.Time) (importer.Batch, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO import_batch
			(user_id, source, origin, filename, file_sha256, stored_path, status, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		b.UserID, b.Source, string(b.Origin), b.Filename, b.FileSHA256, b.StoredPath,
		string(b.Status), ts(now))
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: creating import batch: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: creating import batch: %w", err)
	}
	return r.GetBatch(ctx, b.UserID, id)
}

// GetBatch returns one batch.
func (r *ImportRepo) GetBatch(ctx context.Context, userID, id int64) (importer.Batch, error) {
	b, err := scanBatch(r.db.QueryRowContext(ctx,
		batchSelect+` WHERE user_id = ? AND id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return importer.Batch{}, apperr.NotFoundf("import batch %d", id)
	}
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: reading import batch %d: %w", id, err)
	}
	return b, nil
}

// ListBatches returns the batch history, newest first.
func (r *ImportRepo) ListBatches(ctx context.Context, userID int64, limit int) ([]importer.Batch, error) {
	rows, err := r.db.QueryContext(ctx,
		batchSelect+` WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing import batches: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []importer.Batch{}
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning import batch: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpdateBatch writes the status, counts and error text.
func (r *ImportRepo) UpdateBatch(ctx context.Context, b importer.Batch) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE import_batch
		SET status = ?, rows_total = ?, rows_new = ?, rows_duplicate = ?,
		    rows_unmapped = ?, rows_rejected = ?, error = ?
		WHERE user_id = ? AND id = ?`,
		string(b.Status), b.RowsTotal, b.RowsNew, b.RowsDuplicate,
		b.RowsUnmapped, b.RowsRejected, nullText(b.Error), b.UserID, b.ID)
	if err != nil {
		return fmt.Errorf("store: updating import batch %d: %w", b.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("import batch %d", b.ID)
	}
	return nil
}

// FindCommittedByChecksum finds a committed batch for an identical file.
func (r *ImportRepo) FindCommittedByChecksum(ctx context.Context, userID int64, source, sha256 string) (*importer.Batch, error) {
	b, err := scanBatch(r.db.QueryRowContext(ctx,
		batchSelect+` WHERE user_id = ? AND source = ? AND file_sha256 = ? AND status = 'committed'
		ORDER BY id DESC LIMIT 1`, userID, source, sha256))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: looking up import batch by checksum: %w", err)
	}
	return &b, nil
}

// ReplaceRows rewrites a batch's rows. An import is re-analysed after every
// mapping decision, so the previous analysis is replaced rather than merged.
func (r *ImportRepo) ReplaceRows(ctx context.Context, userID, batchID int64, rows []importer.Row) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: replacing import rows: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// import_row is scoped through its batch, so the delete subselects the batch
	// rather than trusting the id it was handed.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM import_row
		WHERE batch_id IN (SELECT id FROM import_batch WHERE user_id = ? AND id = ?)`,
		userID, batchID); err != nil {
		return fmt.Errorf("store: clearing import rows: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO import_row
			(batch_id, line_no, raw_line, parsed_date, parsed_account, parsed_category,
			 parsed_amount_minor, parsed_currency, parsed_description, status, reason)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("store: preparing import row insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, row := range rows {
		var parsedDate any
		if row.Date != nil {
			parsedDate = date(*row.Date)
		}
		if _, err := stmt.ExecContext(ctx,
			batchID, row.LineNo, row.Raw, parsedDate, nullText(row.AccountName),
			nullText(row.CategoryName), nullInt64(row.AmountMinor), nullText(row.Currency),
			nullString(row.Description), string(row.Status), nullText(row.Reason),
		); err != nil {
			return fmt.Errorf("store: inserting import row %d: %w", row.LineNo, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: replacing import rows: commit: %w", err)
	}
	return nil
}

// ListRows returns a batch's rows, optionally filtered by status.
func (r *ImportRepo) ListRows(ctx context.Context, userID, batchID int64, status importer.RowStatus, limit int) ([]importer.Row, error) {
	query := `
		SELECT r.id, r.batch_id, r.line_no, r.raw_line, r.parsed_date, r.parsed_account,
		       r.parsed_category, r.parsed_amount_minor, r.parsed_currency,
		       r.parsed_description, r.status, r.reason, r.transaction_id
		FROM import_row r
		JOIN import_batch b ON b.id = r.batch_id
		WHERE b.user_id = ? AND r.batch_id = ?`
	args := []any{userID, batchID}
	if status != "" {
		query += ` AND r.status = ?`
		args = append(args, string(status))
	}
	query += ` ORDER BY r.line_no`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing import rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []importer.Row{}
	for rows.Next() {
		var (
			row      importer.Row
			parsedOn sql.NullString
			acct     sql.NullString
			cat      sql.NullString
			amount   sql.NullInt64
			cur      sql.NullString
			desc     sql.NullString
			reason   sql.NullString
			txnID    sql.NullInt64
		)
		if err := rows.Scan(&row.ID, &row.BatchID, &row.LineNo, &row.Raw, &parsedOn, &acct,
			&cat, &amount, &cur, &desc, &row.Status, &reason, &txnID); err != nil {
			return nil, fmt.Errorf("store: scanning import row: %w", err)
		}
		if parsedOn.Valid {
			on, err := parseDate(parsedOn.String)
			if err != nil {
				return nil, fmt.Errorf("store: import row date %q: %w", parsedOn.String, err)
			}
			row.Date = &on
		}
		row.AccountName = acct.String
		row.CategoryName = cat.String
		row.AmountMinor = scanNullInt64(amount)
		row.Currency = cur.String
		row.Description = scanNullString(desc)
		row.Reason = reason.String
		row.TransactionID = scanNullInt64(txnID)
		out = append(out, row)
	}
	return out, rows.Err()
}

// MaxOccurrences returns the highest live occurrence stored per natural key.
//
// MAX rather than COUNT: a soft-deleted row still occupies its occurrence
// number, and reusing it would collide the moment the partial unique index sees
// an undeleted row again.
func (r *ImportRepo) MaxOccurrences(ctx context.Context, userID int64, keys []string) (map[string]int, error) {
	out := make(map[string]int, len(keys))
	unique := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}

	for start := 0; start < len(unique); start += keyChunk {
		end := start + keyChunk
		if end > len(unique) {
			end = len(unique)
		}
		if err := r.occurrencesOf(ctx, userID, unique[start:end], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// occurrencesOf reads one chunk. It is a function of its own so the rows can be
// closed with defer inside the loop.
func (r *ImportRepo) occurrencesOf(ctx context.Context, userID int64, chunk []string, into map[string]int) error {
	args := make([]any, 0, len(chunk)+1)
	args = append(args, userID)
	for _, k := range chunk {
		args = append(args, k)
	}
	query := `
		SELECT natural_key, MAX(occurrence)
		FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND natural_key IN (` +
		placeholders(len(chunk)) + `)
		GROUP BY natural_key`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: reading stored occurrences: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			key     string
			highest int
		)
		if err := rows.Scan(&key, &highest); err != nil {
			return fmt.Errorf("store: scanning stored occurrence: %w", err)
		}
		into[key] = highest
	}
	return rows.Err()
}

// ImportedInRange returns rows previously produced by an import of this source
// inside [from, to].
func (r *ImportRepo) ImportedInRange(ctx context.Context, userID int64, source string, from, to time.Time) ([]importer.StoredRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.natural_key, t.occurrence, t.occurred_on, a.name,
		       COALESCE(c.name,''), t.amount_minor, t.currency, COALESCE(t.description,'')
		FROM transaction_entry t
		JOIN import_batch b ON b.id = t.import_batch_id AND b.user_id = t.user_id
		JOIN account a ON a.id = t.account_id AND a.user_id = t.user_id
		LEFT JOIN category c ON c.id = t.category_id AND c.user_id = t.user_id
		WHERE t.user_id = ? AND b.source = ? AND t.deleted_at IS NULL
		  AND t.occurred_on BETWEEN ? AND ?
		ORDER BY t.occurred_on, t.id`,
		userID, source, date(from), date(to))
	if err != nil {
		return nil, fmt.Errorf("store: reading imported rows in range: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []importer.StoredRow{}
	for rows.Next() {
		var (
			row        importer.StoredRow
			occurredOn string
		)
		if err := rows.Scan(&row.TransactionID, &row.NaturalKey, &row.Occurrence, &occurredOn,
			&row.AccountName, &row.CategoryName, &row.AmountMinor, &row.Currency, &row.Description); err != nil {
			return nil, fmt.Errorf("store: scanning imported row: %w", err)
		}
		on, err := parseDate(occurredOn)
		if err != nil {
			return nil, fmt.Errorf("store: imported row date %q: %w", occurredOn, err)
		}
		row.OccurredOn = on
		out = append(out, row)
	}
	return out, rows.Err()
}

// Commit inserts every entry, stamps import_batch_id, marks the source rows
// committed and closes the batch — in one SQL transaction. A failure part-way
// stores nothing, which is what makes a failed import safe to retry.
func (r *ImportRepo) Commit(
	ctx context.Context, userID, batchID int64,
	entries []importer.CommitEntry, counts importer.Counts, now time.Time,
) (importer.Batch, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: committing import: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insert, err := tx.PrepareContext(ctx, `
		INSERT INTO transaction_entry
			(user_id, account_id, category_id, occurred_on, kind, amount_minor, currency,
			 base_amount_minor, base_currency, fx_rate_id, description, merchant,
			 natural_key, occurrence, import_batch_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: preparing import insert: %w", err)
	}
	defer func() { _ = insert.Close() }()

	// The row is located through its batch, which is already scoped to the user.
	markRow, err := tx.PrepareContext(ctx, `
		UPDATE import_row SET status = 'committed', transaction_id = ?
		WHERE line_no = ?
		  AND batch_id IN (SELECT id FROM import_batch WHERE user_id = ? AND id = ?)`)
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: preparing import row update: %w", err)
	}
	defer func() { _ = markRow.Close() }()

	for _, e := range entries {
		t := e.Transaction
		var baseMinor, baseCur any
		if t.BaseAmount != nil {
			baseMinor = t.BaseAmount.Minor
			baseCur = t.BaseAmount.Currency
		}
		res, err := insert.ExecContext(ctx,
			userID, t.AccountID, nullInt64(t.CategoryID), date(t.OccurredOn), string(t.Kind),
			t.Amount.Minor, t.Amount.Currency, baseMinor, baseCur, nullInt64(t.FxRateID),
			nullString(t.Description), nullString(t.Merchant), t.NaturalKey, t.Occurrence,
			batchID, ts(now), ts(now))
		if err != nil {
			if isUniqueViolation(err) {
				return importer.Batch{}, apperr.Conflictf(
					"line %d duplicates a transaction already recorded; re-run the preview", e.LineNo)
			}
			return importer.Batch{}, fmt.Errorf("store: inserting imported line %d: %w", e.LineNo, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return importer.Batch{}, fmt.Errorf("store: inserting imported line %d: %w", e.LineNo, err)
		}
		if _, err := markRow.ExecContext(ctx, id, e.LineNo, userID, batchID); err != nil {
			return importer.Batch{}, fmt.Errorf("store: marking import row %d: %w", e.LineNo, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE import_batch
		SET status = 'committed', rows_total = ?, rows_new = ?, rows_duplicate = ?,
		    rows_unmapped = ?, rows_rejected = ?, committed_at = ?, error = NULL
		WHERE user_id = ? AND id = ?`,
		counts.Total, counts.New, counts.Duplicate, counts.Unmapped, counts.Rejected,
		ts(now), userID, batchID); err != nil {
		return importer.Batch{}, fmt.Errorf("store: closing import batch %d: %w", batchID, err)
	}

	if err := tx.Commit(); err != nil {
		return importer.Batch{}, fmt.Errorf("store: committing import: commit: %w", err)
	}
	return r.GetBatch(ctx, userID, batchID)
}

// Revert soft-deletes exactly the rows carrying this batch id.
func (r *ImportRepo) Revert(ctx context.Context, userID, batchID int64, now time.Time) (importer.Batch, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: reverting import: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		UPDATE transaction_entry SET deleted_at = ?, updated_at = ?
		WHERE user_id = ? AND import_batch_id = ? AND deleted_at IS NULL`,
		ts(now), ts(now), userID, batchID); err != nil {
		return importer.Batch{}, fmt.Errorf("store: reverting import batch %d: %w", batchID, err)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE import_batch SET status = 'reverted', reverted_at = ?
		WHERE user_id = ? AND id = ? AND status = 'committed'`,
		ts(now), userID, batchID)
	if err != nil {
		return importer.Batch{}, fmt.Errorf("store: reverting import batch %d: %w", batchID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return importer.Batch{}, apperr.Conflictf("import batch %d is not committed", batchID)
	}
	if err := tx.Commit(); err != nil {
		return importer.Batch{}, fmt.Errorf("store: reverting import: commit: %w", err)
	}
	return r.GetBatch(ctx, userID, batchID)
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// nullText stores an empty string as NULL: not recorded is not the empty string.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
