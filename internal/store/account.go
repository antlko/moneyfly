package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// AccountRepo stores accounts and their aliases. It implements account.Repo.
type AccountRepo struct{ db *sql.DB }

// NewAccountRepo builds the repository.
func NewAccountRepo(db *sql.DB) *AccountRepo { return &AccountRepo{db: db} }

// has_children is computed, because a parent's value is a sum over its children
// and must never be written directly.
const accountSelect = `
	SELECT a.id, a.user_id, a.name, a.asset_class, a.currency, a.is_liquid,
	       a.counts_toward_net_worth, a.price_ticker, a.cost_basis_minor, a.parent_id,
	       a.sort_order, a.archived_at,
	       EXISTS (SELECT 1 FROM account child
	               WHERE child.user_id = a.user_id AND child.parent_id = a.id
	                 AND child.archived_at IS NULL) AS has_children
	FROM account a`

func scanAccount(row interface{ Scan(...any) error }) (account.Account, error) {
	var (
		a           account.Account
		liquid      int
		counts      int
		ticker      sql.NullString
		costBasis   sql.NullInt64
		parentID    sql.NullInt64
		archived    sql.NullString
		hasChildren int
	)
	if err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.AssetClass, &a.Currency, &liquid,
		&counts, &ticker, &costBasis, &parentID, &a.SortOrder, &archived, &hasChildren); err != nil {
		return account.Account{}, err
	}
	a.IsLiquid = liquid == 1
	a.CountsTowardNetWorth = counts == 1
	a.PriceTicker = scanNullString(ticker)
	if costBasis.Valid {
		cb := money.New(costBasis.Int64, a.Currency)
		a.CostBasis = &cb
	}
	a.ParentID = scanNullInt64(parentID)
	at, err := scanNullTime(archived)
	if err != nil {
		return account.Account{}, err
	}
	a.ArchivedAt = at
	a.HasChildren = hasChildren == 1
	return a, nil
}

// List returns the user's accounts in sort order.
func (r *AccountRepo) List(ctx context.Context, userID int64, includeArchived bool) ([]account.Account, error) {
	query := accountSelect + ` WHERE a.user_id = ?`
	if !includeArchived {
		query += ` AND a.archived_at IS NULL`
	}
	query += ` ORDER BY a.sort_order, a.id`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listing accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []account.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Get returns one account.
func (r *AccountRepo) Get(ctx context.Context, userID, id int64) (account.Account, error) {
	a, err := scanAccount(r.db.QueryRowContext(ctx, accountSelect+` WHERE a.user_id = ? AND a.id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return account.Account{}, apperr.NotFoundf("account %d", id)
	}
	if err != nil {
		return account.Account{}, fmt.Errorf("store: reading account %d: %w", id, err)
	}
	return a, nil
}

// Create inserts an account.
func (r *AccountRepo) Create(ctx context.Context, a account.Account, now time.Time) (account.Account, error) {
	var costBasis any
	if a.CostBasis != nil {
		costBasis = a.CostBasis.Minor
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO account (user_id, name, asset_class, currency, is_liquid, counts_toward_net_worth,
		                     price_ticker, cost_basis_minor, parent_id, sort_order, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.UserID, a.Name, string(a.AssetClass), a.Currency, boolToInt(a.IsLiquid),
		boolToInt(a.CountsTowardNetWorth), nullString(a.PriceTicker), costBasis,
		nullInt64(a.ParentID), a.SortOrder, ts(now), ts(now))
	if err != nil {
		if isUniqueViolation(err) {
			return account.Account{}, apperr.Conflictf("an account named %q already exists", a.Name)
		}
		return account.Account{}, fmt.Errorf("store: creating account: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return account.Account{}, fmt.Errorf("store: creating account: %w", err)
	}
	a.ID = id
	return a, nil
}

// Update writes an account.
func (r *AccountRepo) Update(ctx context.Context, a account.Account, now time.Time) (account.Account, error) {
	var costBasis any
	if a.CostBasis != nil {
		costBasis = a.CostBasis.Minor
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE account
		SET name = ?, asset_class = ?, currency = ?, is_liquid = ?, counts_toward_net_worth = ?,
		    price_ticker = ?, cost_basis_minor = ?, parent_id = ?, sort_order = ?, updated_at = ?
		WHERE user_id = ? AND id = ?`,
		a.Name, string(a.AssetClass), a.Currency, boolToInt(a.IsLiquid), boolToInt(a.CountsTowardNetWorth),
		nullString(a.PriceTicker), costBasis, nullInt64(a.ParentID), a.SortOrder, ts(now), a.UserID, a.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return account.Account{}, apperr.Conflictf("an account named %q already exists", a.Name)
		}
		return account.Account{}, fmt.Errorf("store: updating account %d: %w", a.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return account.Account{}, apperr.NotFoundf("account %d", a.ID)
	}
	return r.Get(ctx, a.UserID, a.ID)
}

// Archive hides an account while keeping its history.
func (r *AccountRepo) Archive(ctx context.Context, userID, id int64, now time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE account SET archived_at = ?, updated_at = ? WHERE user_id = ? AND id = ? AND archived_at IS NULL`,
		ts(now), ts(now), userID, id)
	if err != nil {
		return fmt.Errorf("store: archiving account %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("active account %d", id)
	}
	return nil
}

// NextSortOrder returns one past the highest sort order in use.
func (r *AccountRepo) NextSortOrder(ctx context.Context, userID int64) (int, error) {
	var n sql.NullInt64
	if err := r.db.QueryRowContext(ctx,
		`SELECT MAX(sort_order) FROM account WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: next account sort order: %w", err)
	}
	return int(n.Int64) + 1, nil
}

// HasValues reports whether anything is posted to the account. A parent must hold
// no value of its own, so this is what makes a parent assignment illegal.
func (r *AccountRepo) HasValues(ctx context.Context, userID, id int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM transaction_entry
		        WHERE user_id = ? AND account_id = ? AND deleted_at IS NULL)
		     + (SELECT count(*) FROM balance_snapshot
		        WHERE user_id = ? AND account_id = ?)`,
		userID, id, userID, id).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: checking account %d for values: %w", id, err)
	}
	return n > 0, nil
}

// ListAliases returns the account mapping table.
func (r *AccountRepo) ListAliases(ctx context.Context, userID int64) ([]account.Alias, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT al.id, al.user_id, al.account_id, al.source, al.source_name, a.name
		FROM account_alias al
		JOIN account a ON a.id = al.account_id
		WHERE al.user_id = ?
		ORDER BY a.name, al.source_name`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listing account aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []account.Alias{}
	for rows.Next() {
		var a account.Alias
		if err := rows.Scan(&a.ID, &a.UserID, &a.AccountID, &a.Source, &a.SourceName, &a.TargetName); err != nil {
			return nil, fmt.Errorf("store: scanning account alias: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateAlias records a mapping, replacing any previous target for that name.
func (r *AccountRepo) CreateAlias(ctx context.Context, a account.Alias, now time.Time) (account.Alias, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO account_alias (user_id, account_id, source, source_name, created_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT (user_id, source, source_name) DO UPDATE SET account_id = excluded.account_id`,
		a.UserID, a.AccountID, a.Source, a.SourceName, ts(now))
	if err != nil {
		return account.Alias{}, fmt.Errorf("store: creating account alias: %w", err)
	}
	if id, err := res.LastInsertId(); err == nil {
		a.ID = id
	}
	return a, nil
}

// DeleteAlias removes a mapping.
func (r *AccountRepo) DeleteAlias(ctx context.Context, userID, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM account_alias WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return fmt.Errorf("store: deleting account alias %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("account alias %d", id)
	}
	return nil
}

// FindAlias resolves a source account name, or (nil, nil) when unknown.
func (r *AccountRepo) FindAlias(ctx context.Context, userID int64, source, sourceName string) (*account.Account, error) {
	a, err := scanAccount(r.db.QueryRowContext(ctx, `
		SELECT a.id, a.user_id, a.name, a.asset_class, a.currency, a.is_liquid,
		       a.counts_toward_net_worth, a.price_ticker, a.cost_basis_minor, a.parent_id,
		       a.sort_order, a.archived_at,
		       EXISTS (SELECT 1 FROM account child
		               WHERE child.user_id = a.user_id AND child.parent_id = a.id
		                 AND child.archived_at IS NULL)
		FROM account_alias al
		JOIN account a ON a.id = al.account_id
		WHERE al.user_id = ? AND al.source = ? AND al.source_name = ?`, userID, source, sourceName))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: resolving account alias %q: %w", sourceName, err)
	}
	return &a, nil
}
