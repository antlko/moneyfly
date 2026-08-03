package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// CurrencyRepo reads the currency table. It implements currency.Repo.
type CurrencyRepo struct{ db *sql.DB }

// NewCurrencyRepo builds the repository.
func NewCurrencyRepo(db *sql.DB) *CurrencyRepo { return &CurrencyRepo{db: db} }

// List returns every currency, ordered by code.
func (r *CurrencyRepo) List(ctx context.Context) ([]currency.Currency, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT code, exponent, COALESCE(symbol, ''), name FROM currency ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("store: listing currencies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []currency.Currency
	for rows.Next() {
		var c currency.Currency
		if err := rows.Scan(&c.Code, &c.Exponent, &c.Symbol, &c.Name); err != nil {
			return nil, fmt.Errorf("store: scanning currency: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one currency.
func (r *CurrencyRepo) Get(ctx context.Context, code string) (currency.Currency, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	var c currency.Currency
	err := r.db.QueryRowContext(ctx,
		`SELECT code, exponent, COALESCE(symbol, ''), name FROM currency WHERE code = ?`, code,
	).Scan(&c.Code, &c.Exponent, &c.Symbol, &c.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return currency.Currency{}, apperr.NotFoundf("currency %q", code)
	}
	if err != nil {
		return currency.Currency{}, fmt.Errorf("store: reading currency %q: %w", code, err)
	}
	return c, nil
}
