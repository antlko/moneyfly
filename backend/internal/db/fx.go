package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"moneyfly/internal/fx"
)

// FxRepo stores dated exchange rates. It implements fx.Repo.
//
// Unlike every other repository here it takes no user id: fx_rate carries none,
// because a rate is a fact about the world. See the header of migration 00003.
type FxRepo struct{ db *DB }

// Rates returns the exchange-rate repository.
func (d *DB) Rates() *FxRepo { return &FxRepo{db: d} }

func scanRate(row interface{ Scan(...any) error }) (fx.Rate, error) {
	var (
		r         fx.Rate
		asOf      string
		rateText  string
		fetchedAt int64
	)
	if err := row.Scan(&r.ID, &asOf, &r.Base, &r.Quote, &rateText, &r.Source, &fetchedAt); err != nil {
		return fx.Rate{}, err
	}
	parsed, err := time.Parse(fx.DateLayout, asOf)
	if err != nil {
		return fx.Rate{}, fmt.Errorf("db: fx as_of_date %q: %w", asOf, err)
	}
	r.AsOf = parsed.UTC()
	r.FetchedAt = time.Unix(fetchedAt, 0).UTC()
	if r.Rate, err = fx.ParseRate(rateText); err != nil {
		return fx.Rate{}, fmt.Errorf("db: fx rate %q: %w", rateText, err)
	}
	return r, nil
}

// ratePrecedence orders candidate rates best-first *within* a lookup.
//
// Later date beats earlier — but never later than the day asked about, which the
// WHERE clause enforces, because a figure from last March must not change
// because a rate arrived in April.
//
// Within one date a hand-entered rate beats a fetched one, whichever was written
// first. Ordering on `id` alone made that depend on whether the daily refresh
// happened to run after the person typed their rate, so the override worked
// until it silently didn't. Below that, the later write wins: two providers only
// ever hold the same day after a failover, and the later one is the one that
// answered.
//
// One string, used by every lookup here, so "which rate applies" has a single
// definition rather than one per query.
const sourcePrecedence = `(source = '` + fx.SourceManual + `') DESC, id DESC`

const ratePrecedence = `as_of_date DESC, ` + sourcePrecedence

// RateOn returns the rate for the exact date, else the nearest earlier one, else
// (nil, nil).
func (r *FxRepo) RateOn(ctx context.Context, base, quote string, on time.Time) (*fx.Rate, error) {
	rate, err := scanRate(r.db.QueryRowContext(ctx, `
		SELECT id, as_of_date, base, quote, rate, source, fetched_at
		FROM fx_rate
		WHERE base = ? AND quote = ? AND as_of_date <= ?
		ORDER BY `+ratePrecedence+`
		LIMIT 1`, base, quote, day(on)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("db: looking up rate %s->%s on %s: %w", base, quote, day(on), err)
	}
	return &rate, nil
}

// History returns rates for a pair in ascending date order, one row per date:
// the one that applies, chosen exactly as RateOn chooses it.
//
// One per date rather than all of them, because the caller is a client cache
// keyed on (quote, date) — it can only hold one anyway, and handing it several
// would let it keep whichever arrived last. Deciding here is what keeps a device
// converting with the same rate the server would use.
func (r *FxRepo) History(ctx context.Context, base, quote string, from, to time.Time) ([]fx.Rate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.as_of_date, f.base, f.quote, f.rate, f.source, f.fetched_at
		FROM fx_rate f
		WHERE f.base = ? AND f.quote = ? AND f.as_of_date BETWEEN ? AND ?
			AND f.id = (
				SELECT g.id FROM fx_rate g
				WHERE g.base = f.base AND g.quote = f.quote AND g.as_of_date = f.as_of_date
				ORDER BY `+sourcePrecedence+`
				LIMIT 1
			)
		ORDER BY f.as_of_date`, base, quote, day(from), day(to))
	if err != nil {
		return nil, fmt.Errorf("db: reading rate history: %w", err)
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
// no-op, so the refresh loop is idempotent across restarts.
func (r *FxRepo) Upsert(ctx context.Context, rate fx.Rate) (fx.Rate, error) {
	fetched := rate.FetchedAt
	if fetched.IsZero() {
		fetched = time.Now().UTC()
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO fx_rate (as_of_date, base, quote, rate, source, fetched_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (as_of_date, base, quote, source)
		DO UPDATE SET rate = excluded.rate, fetched_at = excluded.fetched_at`,
		day(rate.AsOf), rate.Base, rate.Quote, fx.FormatRate(rate.Rate), rate.Source,
		fetched.Unix()); err != nil {
		return fx.Rate{}, fmt.Errorf("db: storing rate: %w", err)
	}
	stored, err := scanRate(r.db.QueryRowContext(ctx, `
		SELECT id, as_of_date, base, quote, rate, source, fetched_at
		FROM fx_rate
		WHERE as_of_date = ? AND base = ? AND quote = ? AND source = ?`,
		day(rate.AsOf), rate.Base, rate.Quote, rate.Source))
	if err != nil {
		return fx.Rate{}, fmt.Errorf("db: reading back stored rate: %w", err)
	}
	return stored, nil
}

// Latest returns the applicable rate today for every quote currency of a base.
//
// "Applicable" rather than "highest id": grouping on MAX(id) picked whichever
// row was *written* last, so entering a rate for an older date made that older
// date look like the newest one everywhere. This asks the same question RateOn
// asks — which single row wins for this pair — with the same ordering.
func (r *FxRepo) Latest(ctx context.Context, base string) ([]fx.Rate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.as_of_date, f.base, f.quote, f.rate, f.source, f.fetched_at
		FROM fx_rate f
		WHERE f.base = ? AND f.id = (
			SELECT g.id FROM fx_rate g
			WHERE g.base = f.base AND g.quote = f.quote
			ORDER BY `+ratePrecedence+`
			LIMIT 1
		)
		ORDER BY f.quote`, base)
	if err != nil {
		return nil, fmt.Errorf("db: reading latest rates: %w", err)
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

// CurrencySetting is the `user_setting` row id holding the currencies a person
// has turned on. It must match SETTING.currencies in web-ui/src/stores/settings.ts.
const CurrencySetting = "currency.enabled"

// UsedCurrencies lists every currency code this instance needs a rate for:
// account currencies, transaction currencies (including the receiving side of a
// cross-currency transfer), users' base currencies, and the currencies people
// have explicitly turned on.
//
// That last source is what makes the feature usable. Deriving the list purely
// from existing rows is circular — a currency could only start updating after
// something already used it, and nothing could use it until it updated — so
// turning one on in the UI has to be enough on its own.
//
// This is also what the refresher requires a provider to publish. Hardcoding a
// list would be one person's currencies.
func (d *DB) UsedCurrencies(ctx context.Context) ([]string, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT DISTINCT currency FROM account WHERE deleted = 0 AND currency IS NOT NULL
		UNION SELECT DISTINCT currency FROM txn WHERE deleted = 0 AND currency IS NOT NULL
		UNION SELECT DISTINCT json_extract(data, '$.toCurrency') FROM txn
			WHERE deleted = 0 AND json_extract(data, '$.toCurrency') IS NOT NULL
		UNION SELECT DISTINCT base_currency FROM users
		UNION SELECT DISTINCT chosen.value
			FROM user_setting, json_each(json_extract(user_setting.data, '$.value')) AS chosen
			WHERE user_setting.id = ? AND user_setting.deleted = 0
		ORDER BY 1`, CurrencySetting)
	if err != nil {
		return nil, fmt.Errorf("db: reading used currencies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		if len(code) == 3 && code != fx.StorageBase {
			out = append(out, code)
		}
	}
	return out, rows.Err()
}

func day(t time.Time) string { return t.UTC().Format(fx.DateLayout) }
