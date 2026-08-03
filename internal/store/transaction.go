package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// TransactionRepo stores transaction_entry rows. It implements transaction.Repo
// and budget.ActualsSource.
type TransactionRepo struct{ db *sql.DB }

// NewTransactionRepo builds the repository.
func NewTransactionRepo(db *sql.DB) *TransactionRepo { return &TransactionRepo{db: db} }

// txnSelect is composed with a WHERE clause at each call site. The joins carry
// user_id as well as the foreign key: the FK already guarantees the row belongs to
// the same user, and stating it makes the scoping visible in every statement built
// from this fragment.
const txnSelect = `
	SELECT t.id, t.user_id, t.account_id, t.category_id, t.occurred_on, t.kind,
	       t.amount_minor, t.currency, t.base_amount_minor, t.base_currency, t.fx_rate_id,
	       t.description, t.merchant, t.natural_key, t.occurrence, t.transfer_group_id,
	       t.import_batch_id, t.created_at, t.updated_at,
	       a.name, COALESCE(c.name, ''), f.as_of_date
	FROM transaction_entry t
	JOIN account a ON a.id = t.account_id AND a.user_id = t.user_id
	LEFT JOIN category c ON c.id = t.category_id AND c.user_id = t.user_id
	LEFT JOIN fx_rate f ON f.id = t.fx_rate_id`

func scanTransaction(row interface{ Scan(...any) error }) (transaction.Transaction, error) {
	var (
		t          transaction.Transaction
		categoryID sql.NullInt64
		occurredOn string
		baseMinor  sql.NullInt64
		baseCur    sql.NullString
		fxRateID   sql.NullInt64
		desc       sql.NullString
		merchant   sql.NullString
		groupID    sql.NullInt64
		batchID    sql.NullInt64
		createdAt  string
		updatedAt  string
		fxAsOf     sql.NullString
	)
	err := row.Scan(&t.ID, &t.UserID, &t.AccountID, &categoryID, &occurredOn, &t.Kind,
		&t.Amount.Minor, &t.Amount.Currency, &baseMinor, &baseCur, &fxRateID,
		&desc, &merchant, &t.NaturalKey, &t.Occurrence, &groupID,
		&batchID, &createdAt, &updatedAt,
		&t.AccountName, &t.CategoryName, &fxAsOf)
	if err != nil {
		return transaction.Transaction{}, err
	}
	t.CategoryID = scanNullInt64(categoryID)
	if t.OccurredOn, err = parseDate(occurredOn); err != nil {
		return transaction.Transaction{}, fmt.Errorf("store: occurred_on %q: %w", occurredOn, err)
	}
	if baseMinor.Valid && baseCur.Valid {
		base := money.New(baseMinor.Int64, baseCur.String)
		t.BaseAmount = &base
	}
	t.FxRateID = scanNullInt64(fxRateID)
	if fxAsOf.Valid {
		at, err := parseDate(fxAsOf.String)
		if err == nil {
			t.FxAsOf = &at
		}
	}
	t.Description = scanNullString(desc)
	t.Merchant = scanNullString(merchant)
	t.TransferGroupID = scanNullInt64(groupID)
	t.ImportBatchID = scanNullInt64(batchID)
	if t.CreatedAt, err = parseTS(createdAt); err != nil {
		return transaction.Transaction{}, err
	}
	if t.UpdatedAt, err = parseTS(updatedAt); err != nil {
		return transaction.Transaction{}, err
	}
	return t, nil
}

// Create inserts one transaction.
//
// The occurrence counter is computed by a subquery *inside* the INSERT, so two
// concurrent writers of the same natural key cannot both read the same count and
// collide. SQLite serialises writers, and the UNIQUE index is the backstop.
func (r *TransactionRepo) Create(ctx context.Context, t transaction.Transaction, now time.Time) (transaction.Transaction, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return transaction.Transaction{}, fmt.Errorf("store: creating transaction: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := insertTransaction(ctx, tx, t, now)
	if err != nil {
		return transaction.Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return transaction.Transaction{}, fmt.Errorf("store: creating transaction: commit: %w", err)
	}
	return r.Get(ctx, t.UserID, id)
}

func insertTransaction(ctx context.Context, tx *sql.Tx, t transaction.Transaction, now time.Time) (int64, error) {
	var (
		baseMinor any
		baseCur   any
	)
	if t.BaseAmount != nil {
		baseMinor = t.BaseAmount.Minor
		baseCur = t.BaseAmount.Currency
	}
	// MAX(occurrence)+1 rather than count(*)+1: a soft-deleted row still occupies
	// its occurrence number, and reusing it would collide the moment the partial
	// unique index sees an undeleted row again.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO transaction_entry
			(user_id, account_id, category_id, occurred_on, kind, amount_minor, currency,
			 base_amount_minor, base_currency, fx_rate_id, description, merchant,
			 natural_key, occurrence, transfer_group_id, import_batch_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,
			(SELECT COALESCE(MAX(e.occurrence), 0) + 1
			 FROM transaction_entry e
			 WHERE e.user_id = ? AND e.natural_key = ?),
			?,?,?,?)`,
		t.UserID, t.AccountID, nullInt64(t.CategoryID), date(t.OccurredOn), string(t.Kind),
		t.Amount.Minor, t.Amount.Currency, baseMinor, baseCur, nullInt64(t.FxRateID),
		nullString(t.Description), nullString(t.Merchant), t.NaturalKey,
		t.UserID, t.NaturalKey,
		nullInt64(t.TransferGroupID), nullInt64(t.ImportBatchID), ts(now), ts(now))
	if err != nil {
		if isUniqueViolation(err) {
			return 0, apperr.Conflictf("this transaction is already recorded")
		}
		return 0, fmt.Errorf("store: inserting transaction: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: inserting transaction: %w", err)
	}
	return id, nil
}

// CreateTransferPair writes both halves in one transaction: both rows or neither.
func (r *TransactionRepo) CreateTransferPair(ctx context.Context, out, in transaction.Transaction, now time.Time) ([]transaction.Transaction, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: creating transfer: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	outID, err := insertTransaction(ctx, tx, out, now)
	if err != nil {
		return nil, err
	}
	inID, err := insertTransaction(ctx, tx, in, now)
	if err != nil {
		return nil, err
	}
	// The outgoing row's id names the group, so no separate sequence is needed.
	if _, err := tx.ExecContext(ctx,
		`UPDATE transaction_entry SET transfer_group_id = ? WHERE user_id = ? AND id IN (?, ?)`,
		outID, out.UserID, outID, inID); err != nil {
		return nil, fmt.Errorf("store: linking transfer pair: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: creating transfer: commit: %w", err)
	}

	first, err := r.Get(ctx, out.UserID, outID)
	if err != nil {
		return nil, err
	}
	second, err := r.Get(ctx, in.UserID, inID)
	if err != nil {
		return nil, err
	}
	return []transaction.Transaction{first, second}, nil
}

// Get returns one live transaction.
func (r *TransactionRepo) Get(ctx context.Context, userID, id int64) (transaction.Transaction, error) {
	t, err := scanTransaction(r.db.QueryRowContext(ctx,
		txnSelect+` WHERE t.user_id = ? AND t.id = ? AND t.deleted_at IS NULL`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return transaction.Transaction{}, apperr.NotFoundf("transaction %d", id)
	}
	if err != nil {
		return transaction.Transaction{}, fmt.Errorf("store: reading transaction %d: %w", id, err)
	}
	return t, nil
}

// List returns a page of history, newest first, paginated on (occurred_on, id).
func (r *TransactionRepo) List(ctx context.Context, userID int64, f transaction.Filter) (transaction.Page, error) {
	query := txnSelect + ` WHERE t.user_id = ? AND t.deleted_at IS NULL`
	args := []any{userID}

	if f.From != nil {
		query += ` AND t.occurred_on >= ?`
		args = append(args, date(*f.From))
	}
	if f.To != nil {
		query += ` AND t.occurred_on <= ?`
		args = append(args, date(*f.To))
	}
	if f.CategoryID != nil {
		query += ` AND t.category_id = ?`
		args = append(args, *f.CategoryID)
	}
	if f.AccountID != nil {
		query += ` AND t.account_id = ?`
		args = append(args, *f.AccountID)
	}
	if f.Kind != "" {
		query += ` AND t.kind = ?`
		args = append(args, string(f.Kind))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		query += ` AND (t.description LIKE ? ESCAPE '\' OR t.merchant LIKE ? ESCAPE '\')`
		like := "%" + escapeLike(q) + "%"
		args = append(args, like, like)
	}
	if f.Cursor.Set {
		// Strictly after the cursor in the descending (occurred_on, id) ordering.
		query += ` AND (t.occurred_on < ? OR (t.occurred_on = ? AND t.id < ?))`
		args = append(args, date(f.Cursor.OccurredOn), date(f.Cursor.OccurredOn), f.Cursor.ID)
	}
	query += ` ORDER BY t.occurred_on DESC, t.id DESC LIMIT ?`
	args = append(args, f.Limit+1) // one extra row answers has_more

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return transaction.Page{}, fmt.Errorf("store: listing transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	page := transaction.Page{Items: []transaction.Transaction{}}
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return transaction.Page{}, fmt.Errorf("store: scanning transaction: %w", err)
		}
		page.Items = append(page.Items, t)
	}
	if err := rows.Err(); err != nil {
		return transaction.Page{}, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &transaction.Cursor{OccurredOn: last.OccurredOn, ID: last.ID, Set: true}
		page.HasMore = true
	}
	return page, nil
}

// Update writes a transaction.
func (r *TransactionRepo) Update(ctx context.Context, t transaction.Transaction, now time.Time) (transaction.Transaction, error) {
	var (
		baseMinor any
		baseCur   any
	)
	if t.BaseAmount != nil {
		baseMinor = t.BaseAmount.Minor
		baseCur = t.BaseAmount.Currency
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE transaction_entry
		SET account_id = ?, category_id = ?, occurred_on = ?, kind = ?, amount_minor = ?,
		    currency = ?, base_amount_minor = ?, base_currency = ?, fx_rate_id = ?,
		    description = ?, merchant = ?, natural_key = ?, updated_at = ?
		WHERE user_id = ? AND id = ? AND deleted_at IS NULL`,
		t.AccountID, nullInt64(t.CategoryID), date(t.OccurredOn), string(t.Kind), t.Amount.Minor,
		t.Amount.Currency, baseMinor, baseCur, nullInt64(t.FxRateID),
		nullString(t.Description), nullString(t.Merchant), t.NaturalKey, ts(now),
		t.UserID, t.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return transaction.Transaction{}, apperr.Conflictf("an identical transaction is already recorded")
		}
		return transaction.Transaction{}, fmt.Errorf("store: updating transaction %d: %w", t.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return transaction.Transaction{}, apperr.NotFoundf("transaction %d", t.ID)
	}
	return r.Get(ctx, t.UserID, t.ID)
}

// SoftDelete removes a transaction from reports while keeping the row. Deleting
// one half of a transfer deletes both, because half a transfer is not a fact.
func (r *TransactionRepo) SoftDelete(ctx context.Context, userID, id int64, now time.Time) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE transaction_entry
		SET deleted_at = ?, updated_at = ?
		WHERE user_id = ? AND deleted_at IS NULL
		  AND (id = ?
		       OR (transfer_group_id IS NOT NULL
		           AND transfer_group_id = (SELECT transfer_group_id FROM transaction_entry
		                                    WHERE user_id = ? AND id = ?)))`,
		ts(now), ts(now), userID, id, userID, id)
	if err != nil {
		return fmt.Errorf("store: deleting transaction %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("transaction %d", id)
	}
	return nil
}

// PeriodActuals aggregates one month for the budget report. It implements
// budget.ActualsSource.
//
// Transfers are excluded: moving money between your own accounts is not spending,
// and including it would double-count.
//
// Rows whose base amount is NULL — no rate was available for their date — are
// excluded from the totals and counted separately, rather than being treated as
// zero.
func (r *TransactionRepo) PeriodActuals(ctx context.Context, userID int64, p period.Period, baseCurrency string) (budget.Actuals, error) {
	from, to := p.Range()
	out := budget.Actuals{
		SpendByCategory: map[int64]money.Money{},
		SpendTotal:      money.New(0, baseCurrency),
		Income:          money.New(0, baseCurrency),
	}

	var recorded int
	if err := r.db.QueryRowContext(ctx, `
		SELECT count(*) FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?`,
		userID, date(from), date(to)).Scan(&recorded); err != nil {
		return budget.Actuals{}, fmt.Errorf("store: period actuals: counting rows: %w", err)
	}
	out.Recorded = recorded > 0
	if !out.Recorded {
		// Absent, not zero. This is the distinction the workbook's -1 sentinel
		// could not express.
		return out, nil
	}

	if err := r.db.QueryRowContext(ctx, `
		SELECT count(*) FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?
		  AND kind IN ('expense','income') AND base_amount_minor IS NULL`,
		userID, date(from), date(to)).Scan(&out.Unconverted); err != nil {
		return budget.Actuals{}, fmt.Errorf("store: period actuals: counting unconverted: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT category_id, SUM(base_amount_minor)
		FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?
		  AND kind = 'expense' AND base_amount_minor IS NOT NULL
		GROUP BY category_id`, userID, date(from), date(to))
	if err != nil {
		return budget.Actuals{}, fmt.Errorf("store: period actuals: spend by category: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			categoryID sql.NullInt64
			sum        int64
		)
		if err := rows.Scan(&categoryID, &sum); err != nil {
			return budget.Actuals{}, fmt.Errorf("store: period actuals: scanning: %w", err)
		}
		if !categoryID.Valid {
			continue
		}
		out.SpendByCategory[categoryID.Int64] = money.New(sum, baseCurrency)
		out.SpendTotal.Minor += sum
	}
	if err := rows.Err(); err != nil {
		return budget.Actuals{}, err
	}

	var income sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `
		SELECT SUM(base_amount_minor) FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?
		  AND kind = 'income' AND base_amount_minor IS NOT NULL`,
		userID, date(from), date(to)).Scan(&income); err != nil {
		return budget.Actuals{}, fmt.Errorf("store: period actuals: income: %w", err)
	}
	out.Income = money.New(income.Int64, baseCurrency)
	return out, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	return strings.ReplaceAll(s, "_", `\_`)
}
