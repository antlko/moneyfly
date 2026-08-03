package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/fx"
)

// FxRepo stores dated exchange rates. It implements fx.Repo.
//
// fx_rate carries no user_id: a rate is a fact about the world, not about a user,
// so it is outside the scoping rule.
type FxRepo struct{ db *sql.DB }

// NewFxRepo builds the repository.
func NewFxRepo(db *sql.DB) *FxRepo { return &FxRepo{db: db} }

func scanRate(row interface{ Scan(...any) error }) (fx.Rate, error) {
	var (
		r         fx.Rate
		asOf      string
		rateText  string
		fetchedAt string
	)
	if err := row.Scan(&r.ID, &asOf, &r.Base, &r.Quote, &rateText, &r.Source, &fetchedAt); err != nil {
		return fx.Rate{}, err
	}
	var err error
	if r.AsOf, err = parseDate(asOf); err != nil {
		return fx.Rate{}, fmt.Errorf("store: fx as_of_date %q: %w", asOf, err)
	}
	if r.FetchedAt, err = parseTS(fetchedAt); err != nil {
		return fx.Rate{}, fmt.Errorf("store: fx fetched_at %q: %w", fetchedAt, err)
	}
	if r.Rate, err = fx.ParseRate(rateText); err != nil {
		return fx.Rate{}, fmt.Errorf("store: fx rate %q: %w", rateText, err)
	}
	return r, nil
}

// RateOn returns the rate for the exact date, else the nearest earlier one, else
// (nil, nil).
//
// Never a later rate: a figure computed last March must not change because a rate
// arrived in April. Provider priority breaks ties within one date, so two
// providers can hold an opinion about the same day without conflict.
func (r *FxRepo) RateOn(ctx context.Context, base, quote string, on time.Time) (*fx.Rate, error) {
	rate, err := scanRate(r.db.QueryRowContext(ctx, `
		SELECT f.id, f.as_of_date, f.base, f.quote, f.rate, f.source, f.fetched_at
		FROM fx_rate f
		LEFT JOIN provider p ON p.key = f.source
		WHERE f.base = ? AND f.quote = ? AND f.as_of_date <= ?
		ORDER BY f.as_of_date DESC, COALESCE(p.priority, 1000) ASC, f.id DESC
		LIMIT 1`, base, quote, date(on)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: looking up rate %s->%s on %s: %w", base, quote, date(on), err)
	}
	return &rate, nil
}

// History returns rates for a pair in ascending date order.
func (r *FxRepo) History(ctx context.Context, base, quote string, from, to time.Time) ([]fx.Rate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.as_of_date, f.base, f.quote, f.rate, f.source, f.fetched_at
		FROM fx_rate f
		WHERE f.base = ? AND f.quote = ? AND f.as_of_date BETWEEN ? AND ?
		ORDER BY f.as_of_date, f.source`, base, quote, date(from), date(to))
	if err != nil {
		return nil, fmt.Errorf("store: reading rate history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []fx.Rate{}
	for rows.Next() {
		rate, err := scanRate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rate)
	}
	return out, rows.Err()
}

// Upsert stores one rate. Re-running a fetch for the same day and provider is a
// no-op, so the scheduler is idempotent.
func (r *FxRepo) Upsert(ctx context.Context, rate fx.Rate) (fx.Rate, error) {
	fetched := rate.FetchedAt
	if fetched.IsZero() {
		fetched = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO fx_rate (as_of_date, base, quote, rate, source, fetched_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (as_of_date, base, quote, source)
		DO UPDATE SET rate = excluded.rate, fetched_at = excluded.fetched_at`,
		date(rate.AsOf), rate.Base, rate.Quote, fx.FormatRate(rate.Rate), rate.Source, ts(fetched))
	if err != nil {
		return fx.Rate{}, fmt.Errorf("store: storing rate: %w", err)
	}
	stored, err := scanRate(r.db.QueryRowContext(ctx, `
		SELECT id, as_of_date, base, quote, rate, source, fetched_at
		FROM fx_rate
		WHERE as_of_date = ? AND base = ? AND quote = ? AND source = ?`,
		date(rate.AsOf), rate.Base, rate.Quote, rate.Source))
	if err != nil {
		return fx.Rate{}, fmt.Errorf("store: reading back stored rate: %w", err)
	}
	return stored, nil
}

// Latest returns the most recent rate for every quote currency of a base.
func (r *FxRepo) Latest(ctx context.Context, base string) ([]fx.Rate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.as_of_date, f.base, f.quote, f.rate, f.source, f.fetched_at
		FROM fx_rate f
		JOIN (
			SELECT quote, MAX(as_of_date) AS as_of_date
			FROM fx_rate WHERE base = ?
			GROUP BY quote
		) latest ON latest.quote = f.quote AND latest.as_of_date = f.as_of_date
		LEFT JOIN provider p ON p.key = f.source
		WHERE f.base = ?
		GROUP BY f.quote
		HAVING COALESCE(p.priority, 1000) = MIN(COALESCE(p.priority, 1000))
		ORDER BY f.quote`, base, base)
	if err != nil {
		return nil, fmt.Errorf("store: reading latest rates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []fx.Rate{}
	for rows.Next() {
		rate, err := scanRate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rate)
	}
	return out, rows.Err()
}
