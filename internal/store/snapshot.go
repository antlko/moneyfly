package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// SnapshotRepo stores balance_snapshot rows. It implements capital.Repo.
type SnapshotRepo struct{ db *sql.DB }

// NewSnapshotRepo builds the repository.
func NewSnapshotRepo(db *sql.DB) *SnapshotRepo { return &SnapshotRepo{db: db} }

// snapshotSelect is a projection fragment; every call site appends a WHERE whose
// first predicate is `user_id = ?`.
//
// unscoped-query-ok: projection fragment, scoped by every caller
const snapshotSelect = `
	SELECT id, account_id, period_month, amount_minor, currency, quantity_nano,
	       base_amount_minor, base_currency, fx_rate_id, COALESCE(note, '')
	FROM balance_snapshot`

func scanSnapshot(row interface{ Scan(...any) error }) (capital.Snapshot, error) {
	var (
		s         capital.Snapshot
		month     string
		minor     sql.NullInt64
		curr      sql.NullString
		quantity  sql.NullInt64
		baseMinor sql.NullInt64
		baseCurr  sql.NullString
		rateID    sql.NullInt64
	)
	if err := row.Scan(&s.ID, &s.AccountID, &month, &minor, &curr, &quantity,
		&baseMinor, &baseCurr, &rateID, &s.Note); err != nil {
		return capital.Snapshot{}, err
	}
	s.Period = period.Period(month)
	if minor.Valid && curr.Valid {
		amount := money.New(minor.Int64, curr.String)
		s.Amount = &amount
	}
	s.QuantityNano = scanNullInt64(quantity)
	if baseMinor.Valid && baseCurr.Valid {
		base := money.New(baseMinor.Int64, baseCurr.String)
		s.BaseAmount = &base
	}
	s.FxRateID = scanNullInt64(rateID)
	return s, nil
}

// ListByPeriod returns one month's snapshots.
func (r *SnapshotRepo) ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]capital.Snapshot, error) {
	rows, err := r.db.QueryContext(ctx,
		snapshotSelect+` WHERE user_id = ? AND period_month = ? ORDER BY account_id`,
		userID, string(p))
	if err != nil {
		return nil, fmt.Errorf("store: listing snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []capital.Snapshot{}
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning snapshot: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListRange returns every snapshot in [from, to], keyed by account and period.
func (r *SnapshotRepo) ListRange(
	ctx context.Context, userID int64, from, to period.Period,
) (map[int64]map[period.Period]capital.Snapshot, error) {
	rows, err := r.db.QueryContext(ctx,
		snapshotSelect+` WHERE user_id = ? AND period_month BETWEEN ? AND ?`,
		userID, string(from), string(to))
	if err != nil {
		return nil, fmt.Errorf("store: listing snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]map[period.Period]capital.Snapshot{}
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning snapshot: %w", err)
		}
		if out[s.AccountID] == nil {
			out[s.AccountID] = map[period.Period]capital.Snapshot{}
		}
		out[s.AccountID][s.Period] = s
	}
	return out, rows.Err()
}

// Upsert writes one snapshot.
func (r *SnapshotRepo) Upsert(ctx context.Context, userID int64, s capital.Snapshot, now time.Time) (capital.Snapshot, error) {
	if err := upsertSnapshot(ctx, r.db, userID, s, now); err != nil {
		return capital.Snapshot{}, err
	}
	return r.get(ctx, userID, s.AccountID, s.Period)
}

// UpsertMany writes a whole month in one transaction, so a half-saved snapshot
// screen cannot exist.
func (r *SnapshotRepo) UpsertMany(ctx context.Context, userID int64, snapshots []capital.Snapshot, now time.Time) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: writing snapshots: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, s := range snapshots {
		if err := upsertSnapshot(ctx, tx, userID, s, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: writing snapshots: commit: %w", err)
	}
	return len(snapshots), nil
}

// execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func upsertSnapshot(ctx context.Context, db execer, userID int64, s capital.Snapshot, now time.Time) error {
	var amountMinor, currencyCode, baseMinor, baseCurrency any
	if s.Amount != nil {
		amountMinor = s.Amount.Minor
		currencyCode = s.Amount.Currency
	}
	if s.BaseAmount != nil {
		baseMinor = s.BaseAmount.Minor
		baseCurrency = s.BaseAmount.Currency
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO balance_snapshot
			(user_id, account_id, period_month, amount_minor, currency, quantity_nano,
			 base_amount_minor, base_currency, fx_rate_id, note, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id, account_id, period_month) DO UPDATE SET
			amount_minor = excluded.amount_minor,
			currency = excluded.currency,
			quantity_nano = excluded.quantity_nano,
			base_amount_minor = excluded.base_amount_minor,
			base_currency = excluded.base_currency,
			fx_rate_id = excluded.fx_rate_id,
			note = excluded.note,
			updated_at = excluded.updated_at`,
		userID, s.AccountID, string(s.Period), amountMinor, currencyCode, nullInt64(s.QuantityNano),
		baseMinor, baseCurrency, nullInt64(s.FxRateID), nullText(s.Note), ts(now), ts(now))
	if err != nil {
		return fmt.Errorf("store: writing snapshot for account %d: %w", s.AccountID, err)
	}
	return nil
}

func (r *SnapshotRepo) get(ctx context.Context, userID, accountID int64, p period.Period) (capital.Snapshot, error) {
	s, err := scanSnapshot(r.db.QueryRowContext(ctx,
		snapshotSelect+` WHERE user_id = ? AND account_id = ? AND period_month = ?`,
		userID, accountID, string(p)))
	if err != nil {
		return capital.Snapshot{}, fmt.Errorf("store: reading snapshot: %w", err)
	}
	return s, nil
}

// Delete removes one snapshot. A month with no snapshot is a gap, never zero and
// never interpolated.
func (r *SnapshotRepo) Delete(ctx context.Context, userID, accountID int64, p period.Period) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM balance_snapshot WHERE user_id = ? AND account_id = ? AND period_month = ?`,
		userID, accountID, string(p))
	if err != nil {
		return fmt.Errorf("store: deleting snapshot: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("snapshot for account %d in %s", accountID, p)
	}
	return nil
}

// ImpliedBalances returns the balance each account's transactions imply as at the
// end of the period, in the account's own currency.
//
// It is only ever compared against a snapshot, never substituted for one: the
// snapshot is authoritative and drift is informational
// (docs/adr/0003-snapshot-reconcile-balances.md).
func (r *SnapshotRepo) ImpliedBalances(ctx context.Context, userID int64, p period.Period) (map[int64]money.Money, error) {
	_, last := p.Range()
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.account_id, a.currency,
		       SUM(CASE WHEN t.kind IN ('income','transfer_in')
		                THEN t.amount_minor ELSE -t.amount_minor END)
		FROM transaction_entry t
		JOIN account a ON a.id = t.account_id AND a.user_id = t.user_id
		WHERE t.user_id = ? AND t.deleted_at IS NULL AND t.occurred_on <= ?
		GROUP BY t.account_id, a.currency`,
		userID, date(last))
	if err != nil {
		return nil, fmt.Errorf("store: implied balances: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]money.Money{}
	for rows.Next() {
		var (
			accountID int64
			code      string
			minor     int64
		)
		if err := rows.Scan(&accountID, &code, &minor); err != nil {
			return nil, fmt.Errorf("store: scanning implied balance: %w", err)
		}
		out[accountID] = money.New(minor, code)
	}
	return out, rows.Err()
}
