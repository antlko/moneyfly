package db

import (
	"fmt"
	"time"

	syncproto "moneyfly/internal/sync"
)

// LegacyImportID stands for every transaction a CSV import wrote before
// imports were tracked as batches: they carry a "csv:" natural key but no
// importId, so they can only be taken back together.
const LegacyImportID = "legacy"

// ImportBatch is one committed CSV import and how many of its transactions
// are still live.
type ImportBatch struct {
	ID        string
	FileName  string
	CreatedAt int64
	Rows      int
}

// RowVersion is the identity and current lamport of one live row — what it
// takes to write a tombstone that beats it.
type RowVersion struct {
	ID      string
	Lamport int64
}

// CreateImportBatch records an import about to be written.
func (d *DB) CreateImportBatch(id, userID, fileName string) error {
	_, err := d.Exec(`
		INSERT INTO import_batch (id, user_id, file_name, created_at) VALUES (?, ?, ?, ?)`,
		id, userID, fileName, time.Now().Unix())
	return err
}

// ImportBatches lists a user's imports that still have live transactions,
// newest first, with the untracked pre-batch imports (LegacyImportID) last.
//
// A batch whose rows are all gone — undone, erased, or deleted one by one —
// is simply not listed: there is nothing left to take back.
func (d *DB) ImportBatches(userID string) ([]ImportBatch, error) {
	rows, err := d.Query(`
		SELECT b.id, b.file_name, b.created_at,
		       (SELECT count(*) FROM txn t
		         WHERE t.user_id = b.user_id AND t.import_id = b.id AND t.deleted = 0)
		  FROM import_batch b
		 WHERE b.user_id = ?
		 ORDER BY b.created_at DESC, b.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: listing imports: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ImportBatch{}
	for rows.Next() {
		var b ImportBatch
		if err := rows.Scan(&b.ID, &b.FileName, &b.CreatedAt, &b.Rows); err != nil {
			return nil, err
		}
		if b.Rows > 0 {
			out = append(out, b)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	legacy := ImportBatch{ID: LegacyImportID}
	if err := d.QueryRow(`
		SELECT count(*), coalesce(min(updated_at), 0) FROM txn
		 WHERE user_id = ? AND deleted = 0 AND import_id IS NULL AND natural_key LIKE 'csv:%'`,
		userID).Scan(&legacy.Rows, &legacy.CreatedAt); err != nil {
		return nil, fmt.Errorf("db: counting untracked imports: %w", err)
	}
	if legacy.Rows > 0 {
		out = append(out, legacy)
	}
	return out, nil
}

// ImportBatchRows returns the live transactions one import wrote.
// LegacyImportID selects the untracked ones.
func (d *DB) ImportBatchRows(userID, batchID string) ([]RowVersion, error) {
	if batchID == LegacyImportID {
		return d.rowVersions(`
			SELECT id, lamport FROM txn
			 WHERE user_id = ? AND deleted = 0 AND import_id IS NULL AND natural_key LIKE 'csv:%'`,
			userID)
	}
	return d.rowVersions(`
		SELECT id, lamport FROM txn WHERE user_id = ? AND import_id = ? AND deleted = 0`,
		userID, batchID)
}

// ImportBatchExists reports whether the user owns an import with this id.
func (d *DB) ImportBatchExists(userID, batchID string) (bool, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM import_batch WHERE user_id = ? AND id = ?`,
		userID, batchID).Scan(&n)
	return n > 0, err
}

// LiveRows returns every non-deleted row of one synced entity for a user.
func (d *DB) LiveRows(userID, entity string) ([]RowVersion, error) {
	if !syncproto.IsEntity(entity) {
		return nil, fmt.Errorf("db: unknown entity %q", entity)
	}
	// entity is checked against the fixed list above, so interpolating it as a
	// table name cannot inject anything — the same argument applyOne makes.
	return d.rowVersions(fmt.Sprintf(
		`SELECT id, lamport FROM %s WHERE user_id = ? AND deleted = 0`, entity), userID)
}

func (d *DB) rowVersions(query string, args ...any) ([]RowVersion, error) {
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: reading row versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []RowVersion{}
	for rows.Next() {
		var r RowVersion
		if err := rows.Scan(&r.ID, &r.Lamport); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
