// Package currency exposes the currency table to the rest of the domain.
//
// Exponents come from here, never from a hardcoded constant: HUF is 0-decimal
// and getting that wrong scales every Hungarian figure by 100
// (docs/adr/0004-integer-money.md).
package currency

import (
	"context"
	"strings"
	"sync"

	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// Currency is one row of the currency table.
type Currency struct {
	Code     string `json:"code"`
	Exponent int    `json:"exponent"`
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
}

// Repo is the storage contract.
type Repo interface {
	List(ctx context.Context) ([]Currency, error)
	Get(ctx context.Context, code string) (Currency, error)
}

// Service answers exponent questions. The set is tiny and changes only by
// migration, so it is loaded once and cached for the process lifetime.
type Service struct {
	repo Repo

	mu     sync.RWMutex
	byCode map[string]Currency
}

// NewService builds the service.
func NewService(repo Repo) *Service { return &Service{repo: repo} }

// List returns every currency.
func (s *Service) List(ctx context.Context) ([]Currency, error) { return s.repo.List(ctx) }

// Get returns one currency, or apperr.ErrNotFound.
func (s *Service) Get(ctx context.Context, code string) (Currency, error) {
	all, err := s.load(ctx)
	if err != nil {
		return Currency{}, err
	}
	c, ok := all[normalise(code)]
	if !ok {
		return Currency{}, apperr.NotFoundf("currency %q", code)
	}
	return c, nil
}

// Exponent returns the number of decimal places for a currency.
func (s *Service) Exponent(ctx context.Context, code string) (int, error) {
	c, err := s.Get(ctx, code)
	if err != nil {
		return 0, err
	}
	return c.Exponent, nil
}

// Exponents returns code -> exponent for every currency, for handlers rendering
// a page full of amounts without a lookup per amount.
func (s *Service) Exponents(ctx context.Context) (map[string]int, error) {
	all, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(all))
	for code, c := range all {
		out[code] = c.Exponent
	}
	return out, nil
}

// Exists reports whether a currency code is known.
func (s *Service) Exists(ctx context.Context, code string) bool {
	_, err := s.Get(ctx, code)
	return err == nil
}

func (s *Service) load(ctx context.Context) (map[string]Currency, error) {
	s.mu.RLock()
	cached := s.byCode
	s.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	list, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]Currency, len(list))
	for _, c := range list {
		m[normalise(c.Code)] = c
	}
	s.mu.Lock()
	s.byCode = m
	s.mu.Unlock()
	return m, nil
}

func normalise(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }
