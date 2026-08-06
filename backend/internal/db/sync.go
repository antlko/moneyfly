package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	syncproto "moneyfly/internal/sync"
)

// ApplyResult reports what a push did.
type ApplyResult struct {
	Accepted int
	// Applied is the subset of the input ops that actually won their
	// last-write-wins comparison and were written — as opposed to Accepted,
	// which only counts them. A caller that needs to know *which* rows
	// changed (a webhook notification, say) uses this instead of the input
	// ops verbatim: an op that lost a conflict is still perfectly
	// well-formed, so filtering only Rejected out of the input would still
	// report something that never actually happened.
	Applied   []syncproto.Op
	Rejected  []syncproto.Rejection
	ServerSeq int64
	Lamport   int64
}

// emptyObject is what a tombstone stores when the client sends no body.
const emptyObject = "{}"

// ApplyOps applies a batch of operations for one user, atomically.
//
// Conflicts are not errors: an op that loses the last-write-wins comparison is
// simply not applied and not logged, which is what makes the whole protocol safe
// to retry. Only structurally invalid ops are rejected, and they are rejected
// individually — one malformed row must not cost a device the rest of its batch.
func (d *DB) ApplyOps(userID string, ops []syncproto.Op) (ApplyResult, error) {
	var res ApplyResult
	if len(ops) > syncproto.MaxOpsPerPush {
		return res, fmt.Errorf("db: batch of %d exceeds the %d op limit",
			len(ops), syncproto.MaxOpsPerPush)
	}

	tx, err := d.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	if _, err := tx.Exec(
		`INSERT INTO sync_state (user_id) VALUES (?) ON CONFLICT (user_id) DO NOTHING`,
		userID); err != nil {
		return res, err
	}

	now := time.Now().Unix()
	var maxLamport int64

	for _, op := range ops {
		if err := op.Validate(); err != nil {
			res.Rejected = append(res.Rejected, syncproto.Rejection{
				Entity: op.Entity, ID: op.ID, Reason: err.Error(),
			})
			continue
		}
		if op.Lamport > maxLamport {
			maxLamport = op.Lamport
		}

		data := string(op.Data)
		if data == "" {
			data = emptyObject
		}

		applied, err := applyOne(tx, userID, op, data, now)
		if err != nil {
			return res, err
		}
		if !applied {
			continue // lost the comparison, or is a replay of what is stored
		}

		if _, err := tx.Exec(`
			INSERT INTO change_log (user_id, entity, entity_id, lamport, device_id,
			                        deleted, data, server_ts)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, op.Entity, op.ID, op.Lamport, op.DeviceID, op.Deleted, data, now); err != nil {
			return res, err
		}
		res.Accepted++
		res.Applied = append(res.Applied, op)
	}

	// The high-water mark only ever rises, so a device that has been offline
	// cannot drag the shared clock backwards when it finally pushes.
	if _, err := tx.Exec(
		`UPDATE sync_state SET lamport = max(lamport, ?) WHERE user_id = ?`,
		maxLamport, userID); err != nil {
		return res, err
	}
	if err := tx.QueryRow(
		`SELECT lamport FROM sync_state WHERE user_id = ?`, userID).Scan(&res.Lamport); err != nil {
		return res, err
	}
	if err := tx.QueryRow(
		`SELECT coalesce(max(seq), 0) FROM change_log WHERE user_id = ?`,
		userID).Scan(&res.ServerSeq); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

// applyOne upserts a single row, resolving the conflict in SQL so there is no
// read-then-write window. It reports whether the row actually changed.
//
// op.Entity has already been checked against the fixed entity list, so
// interpolating it as a table name cannot inject anything.
func applyOne(tx *sql.Tx, userID string, op syncproto.Op, data string, now int64) (bool, error) {
	stmt := fmt.Sprintf(`
		INSERT INTO %[1]s (user_id, id, lamport, device_id, updated_at, deleted, data)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, id) DO UPDATE SET
			lamport    = excluded.lamport,
			device_id  = excluded.device_id,
			updated_at = excluded.updated_at,
			deleted    = excluded.deleted,
			data       = excluded.data
		WHERE %[1]s.lamport < excluded.lamport
		   OR (%[1]s.lamport = excluded.lamport AND %[1]s.device_id < excluded.device_id)`,
		op.Entity)

	res, err := tx.Exec(stmt, userID, op.ID, op.Lamport, op.DeviceID, now, op.Deleted, data)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ErrResyncRequired means the caller's cursor is older than the surviving change
// log, so replaying deltas would silently skip rows. The client must bootstrap
// from a snapshot instead.
var ErrResyncRequired = errors.New("db: cursor is older than the change log")

// PullChanges returns the deltas after a cursor.
func (d *DB) PullChanges(userID string, since int64, limit int) ([]syncproto.Change, int64, bool, error) {
	if limit <= 0 || limit > syncproto.MaxPullLimit {
		limit = syncproto.DefaultPullLimit
	}

	var trimmedBefore int64
	err := d.QueryRow(`SELECT trimmed_before FROM sync_state WHERE user_id = ?`, userID).
		Scan(&trimmedBefore)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, err
	}
	if since > 0 && since < trimmedBefore {
		return nil, 0, false, ErrResyncRequired
	}

	var serverSeq int64
	if err := d.QueryRow(`SELECT coalesce(max(seq), 0) FROM change_log WHERE user_id = ?`,
		userID).Scan(&serverSeq); err != nil {
		return nil, 0, false, err
	}

	// limit+1 so "is there more" needs no second query.
	rows, err := d.Query(`
		SELECT seq, entity, entity_id, lamport, device_id, deleted, data
		FROM change_log
		WHERE user_id = ? AND seq > ?
		ORDER BY seq
		LIMIT ?`, userID, since, limit+1)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()

	changes := make([]syncproto.Change, 0, limit)
	hasMore := false
	for rows.Next() {
		if len(changes) == limit {
			hasMore = true
			break
		}
		var c syncproto.Change
		var data string
		if err := rows.Scan(&c.Seq, &c.Entity, &c.ID, &c.Lamport,
			&c.DeviceID, &c.Deleted, &data); err != nil {
			return nil, 0, false, err
		}
		c.Data = json.RawMessage(data)
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}

	// When there is more to fetch, the cursor must stop at the last row handed
	// over — reporting the global head here would skip everything in between.
	if hasMore {
		serverSeq = changes[len(changes)-1].Seq
	}
	return changes, serverSeq, hasMore, nil
}

// SnapshotRows returns every live row for a user, plus the sequence the caller
// should continue pulling from.
//
// Tombstones are omitted: a device with nothing has nothing to delete. Deletions
// that happen after the snapshot arrive as normal deltas.
func (d *DB) SnapshotRows(userID string) ([]syncproto.Change, int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	// Read the sequence FIRST. Anything committed between this and the row scan
	// would then be delivered twice — which is harmless, because applying an op
	// twice is a no-op. Reading it last would instead let a change fall through
	// the gap entirely.
	var serverSeq int64
	if err := tx.QueryRow(`SELECT coalesce(max(seq), 0) FROM change_log WHERE user_id = ?`,
		userID).Scan(&serverSeq); err != nil {
		return nil, 0, err
	}

	out := make([]syncproto.Change, 0, 128)
	for _, entity := range syncproto.Entities {
		stmt := fmt.Sprintf(`
			SELECT id, lamport, device_id, data FROM %s
			WHERE user_id = ? AND deleted = 0 ORDER BY id`, entity)
		rows, err := tx.Query(stmt, userID)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			c := syncproto.Change{Op: syncproto.Op{Entity: entity}}
			var data string
			if err := rows.Scan(&c.ID, &c.Lamport, &c.DeviceID, &data); err != nil {
				rows.Close()
				return nil, 0, err
			}
			c.Data = json.RawMessage(data)
			out = append(out, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
	}
	return out, serverSeq, tx.Commit()
}

// SyncLamport returns a user's Lamport high-water mark, so a device that has
// just bootstrapped starts its own clock above everyone else's.
func (d *DB) SyncLamport(userID string) (int64, error) {
	var lamport int64
	err := d.QueryRow(`SELECT lamport FROM sync_state WHERE user_id = ?`, userID).Scan(&lamport)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return lamport, err
}

// TrimChangeLog drops journal entries older than cutoff and records, per user,
// the sequence below which replay is no longer possible.
//
// The domain rows themselves are untouched — this only shortens the journal, so
// the cost of trimming is that a long-absent device re-bootstraps.
func (d *DB) TrimChangeLog(cutoff int64) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	rows, err := tx.Query(`
		SELECT user_id, max(seq) FROM change_log
		WHERE server_ts < ? GROUP BY user_id`, cutoff)
	if err != nil {
		return 0, err
	}
	type mark struct {
		userID string
		maxSeq int64
	}
	var marks []mark
	for rows.Next() {
		var m mark
		if err := rows.Scan(&m.userID, &m.maxSeq); err != nil {
			rows.Close()
			return 0, err
		}
		marks = append(marks, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var removed int64
	for _, m := range marks {
		res, err := tx.Exec(`DELETE FROM change_log WHERE user_id = ? AND seq <= ?`,
			m.userID, m.maxSeq)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		removed += n

		// Everything up to and including maxSeq is gone, so a cursor at or below
		// it can no longer replay.
		if _, err := tx.Exec(`
			INSERT INTO sync_state (user_id, trimmed_before) VALUES (?, ?)
			ON CONFLICT (user_id) DO UPDATE SET trimmed_before = max(trimmed_before, excluded.trimmed_before)`,
			m.userID, m.maxSeq+1); err != nil {
			return 0, err
		}
	}
	return removed, tx.Commit()
}

// TouchDeviceSync records a device's progress, for the devices screen.
func (d *DB) TouchDeviceSync(userID, deviceID string, seq int64) error {
	if deviceID == "" {
		return nil
	}
	_, err := d.Exec(`
		UPDATE devices SET last_seen_at = ?, last_seq = max(last_seq, ?)
		WHERE id = ? AND user_id = ?`, time.Now().Unix(), seq, deviceID, userID)
	return err
}
