package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// BudgetRepo stores budget rows. It implements budget.Repo.
type BudgetRepo struct{ db *sql.DB }

// NewBudgetRepo builds the repository.
func NewBudgetRepo(db *sql.DB) *BudgetRepo { return &BudgetRepo{db: db} }

// ListByPeriod returns the plan for one month, with category names for display.
func (r *BudgetRepo) ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]budget.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT b.id, b.user_id, b.category_id, c.name, b.period_month, b.planned_minor, b.currency
		FROM budget b
		JOIN category c ON c.id = b.category_id
		WHERE b.user_id = ? AND b.period_month = ?
		ORDER BY c.sort_order, c.id`, userID, p.String())
	if err != nil {
		return nil, fmt.Errorf("store: listing budgets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []budget.Budget{}
	for rows.Next() {
		var (
			b        budget.Budget
			month    string
			minor    int64
			currency string
		)
		if err := rows.Scan(&b.ID, &b.UserID, &b.CategoryID, &b.CategoryName, &month, &minor, &currency); err != nil {
			return nil, fmt.Errorf("store: scanning budget: %w", err)
		}
		b.Period = period.Period(month)
		b.Planned = money.New(minor, currency)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Upsert writes one budget row.
func (r *BudgetRepo) Upsert(ctx context.Context, b budget.Budget, now time.Time) (budget.Budget, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO budget (user_id, category_id, period_month, planned_minor, currency, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (user_id, category_id, period_month)
		DO UPDATE SET planned_minor = excluded.planned_minor,
		              currency = excluded.currency,
		              updated_at = excluded.updated_at`,
		b.UserID, b.CategoryID, b.Period.String(), b.Planned.Minor, b.Planned.Currency, ts(now), ts(now))
	if err != nil {
		return budget.Budget{}, fmt.Errorf("store: writing budget: %w", err)
	}
	return b, nil
}

// UpsertMany seeds a range in one transaction: the whole year lands or none of it.
func (r *BudgetRepo) UpsertMany(
	ctx context.Context,
	userID int64,
	periods []period.Period,
	items []budget.BulkItem,
	now time.Time,
) (budget.BulkResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return budget.BulkResult{}, fmt.Errorf("store: bulk budgets: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO budget (user_id, category_id, period_month, planned_minor, currency, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (user_id, category_id, period_month)
		DO UPDATE SET planned_minor = excluded.planned_minor,
		              currency = excluded.currency,
		              updated_at = excluded.updated_at`)
	if err != nil {
		return budget.BulkResult{}, fmt.Errorf("store: bulk budgets: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	result := budget.BulkResult{PeriodsWritten: len(periods)}
	for _, p := range periods {
		for _, item := range items {
			if _, err := stmt.ExecContext(ctx, userID, item.CategoryID, p.String(),
				item.Planned.Minor, item.Planned.Currency, ts(now), ts(now)); err != nil {
				return budget.BulkResult{}, fmt.Errorf("store: bulk budgets: writing %s: %w", p, err)
			}
			result.RowsWritten++
		}
	}
	if err := tx.Commit(); err != nil {
		return budget.BulkResult{}, fmt.Errorf("store: bulk budgets: commit: %w", err)
	}
	return result, nil
}

// Delete removes a plan. An absent plan is "not planned", which is not the same
// fact as a plan of zero.
func (r *BudgetRepo) Delete(ctx context.Context, userID, categoryID int64, p period.Period) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM budget WHERE user_id = ? AND category_id = ? AND period_month = ?`,
		userID, categoryID, p.String())
	if err != nil {
		return fmt.Errorf("store: deleting budget: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("budget for category %d in %s", categoryID, p)
	}
	return nil
}
