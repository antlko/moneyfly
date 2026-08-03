package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// MetricsRepo answers the multi-period queries behind the reports. It implements
// metrics.Loader.
//
// Every query here groups by period in SQL rather than looping a single-period
// query, so a twelve-month grid is a handful of statements. None of them
// COALESCE a missing period into zero: absence is carried out of the store and
// decided above it (conventions §3).
type MetricsRepo struct{ db *sql.DB }

// NewMetricsRepo builds the repository.
func NewMetricsRepo(db *sql.DB) *MetricsRepo { return &MetricsRepo{db: db} }

// SpendByPeriod returns one entry per period in [from, to].
func (r *MetricsRepo) SpendByPeriod(
	ctx context.Context, userID int64, from, to period.Period, baseCurrency string,
) ([]metrics.PeriodSpend, error) {
	periods, err := from.Until(to)
	if err != nil {
		return nil, err
	}
	byPeriod := make(map[period.Period]*metrics.PeriodSpend, len(periods))
	out := make([]metrics.PeriodSpend, 0, len(periods))
	for _, p := range periods {
		entry := metrics.PeriodSpend{
			Period:                p,
			ByCategory:            map[int64]money.Money{},
			AverageBaseByCategory: map[int64]money.Money{},
			Income:                money.New(0, baseCurrency),
		}
		out = append(out, entry)
	}
	for i := range out {
		byPeriod[out[i].Period] = &out[i]
	}

	fromDate, _ := from.Range()
	_, toDate := to.Range()

	// A period is recorded when it holds any transaction at all, including a
	// transfer: money moved, the month is not blank.
	if err := r.scan(ctx, `
		SELECT substr(occurred_on, 1, 7) AS period_month, count(*)
		FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?
		GROUP BY period_month`,
		[]any{userID, date(fromDate), date(toDate)},
		func(rows *sql.Rows) error {
			var (
				p string
				n int
			)
			if err := rows.Scan(&p, &n); err != nil {
				return err
			}
			if entry := byPeriod[period.Period(p)]; entry != nil {
				entry.Recorded = n > 0
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("store: metrics: recorded periods: %w", err)
	}

	// Spend per category per period. Rows with no base amount are excluded from
	// the sums and counted separately rather than treated as zero.
	if err := r.scan(ctx, `
		SELECT substr(occurred_on, 1, 7) AS period_month, category_id,
		       SUM(base_amount_minor),
		       SUM(CASE WHEN exclude_from_average = 1 THEN 0 ELSE base_amount_minor END)
		FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?
		  AND kind = 'expense' AND base_amount_minor IS NOT NULL AND category_id IS NOT NULL
		GROUP BY period_month, category_id`,
		[]any{userID, date(fromDate), date(toDate)},
		func(rows *sql.Rows) error {
			var (
				p          string
				categoryID int64
				total      int64
				averaged   int64
			)
			if err := rows.Scan(&p, &categoryID, &total, &averaged); err != nil {
				return err
			}
			entry := byPeriod[period.Period(p)]
			if entry == nil {
				return nil
			}
			entry.ByCategory[categoryID] = money.New(total, baseCurrency)
			entry.AverageBaseByCategory[categoryID] = money.New(averaged, baseCurrency)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("store: metrics: spend by category: %w", err)
	}

	if err := r.scan(ctx, `
		SELECT substr(occurred_on, 1, 7) AS period_month, SUM(base_amount_minor)
		FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?
		  AND kind = 'income' AND base_amount_minor IS NOT NULL
		GROUP BY period_month`,
		[]any{userID, date(fromDate), date(toDate)},
		func(rows *sql.Rows) error {
			var (
				p   string
				sum int64
			)
			if err := rows.Scan(&p, &sum); err != nil {
				return err
			}
			if entry := byPeriod[period.Period(p)]; entry != nil {
				entry.Income = money.New(sum, baseCurrency)
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("store: metrics: income: %w", err)
	}

	if err := r.scan(ctx, `
		SELECT substr(occurred_on, 1, 7) AS period_month, count(*)
		FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL AND occurred_on BETWEEN ? AND ?
		  AND kind IN ('expense','income') AND base_amount_minor IS NULL
		GROUP BY period_month`,
		[]any{userID, date(fromDate), date(toDate)},
		func(rows *sql.Rows) error {
			var (
				p string
				n int
			)
			if err := rows.Scan(&p, &n); err != nil {
				return err
			}
			if entry := byPeriod[period.Period(p)]; entry != nil {
				entry.Unconverted = n
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("store: metrics: unconverted: %w", err)
	}

	return out, nil
}

// RecordedPeriods returns every period the user has any transaction in.
func (r *MetricsRepo) RecordedPeriods(ctx context.Context, userID int64) ([]period.Period, error) {
	out := []period.Period{}
	err := r.scan(ctx, `
		SELECT DISTINCT substr(occurred_on, 1, 7) AS period_month
		FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL
		ORDER BY period_month`,
		[]any{userID},
		func(rows *sql.Rows) error {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			out = append(out, period.Period(p))
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("store: metrics: recorded periods: %w", err)
	}
	return out, nil
}

// PlannedByPeriod returns category -> period -> plan across [from, to].
func (r *MetricsRepo) PlannedByPeriod(
	ctx context.Context, userID int64, from, to period.Period,
) (map[int64]map[period.Period]money.Money, error) {
	out := map[int64]map[period.Period]money.Money{}
	err := r.scan(ctx, `
		SELECT category_id, period_month, planned_minor, currency
		FROM budget
		WHERE user_id = ? AND period_month BETWEEN ? AND ?`,
		[]any{userID, string(from), string(to)},
		func(rows *sql.Rows) error {
			var (
				categoryID int64
				month      string
				minor      int64
				currency   string
			)
			if err := rows.Scan(&categoryID, &month, &minor, &currency); err != nil {
				return err
			}
			if out[categoryID] == nil {
				out[categoryID] = map[period.Period]money.Money{}
			}
			out[categoryID][period.Period(month)] = money.New(minor, currency)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("store: metrics: planned: %w", err)
	}
	return out, nil
}

// scan runs a query and applies fn to every row, so each caller above stays one
// statement and one scan body.
func (r *MetricsRepo) scan(ctx context.Context, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
