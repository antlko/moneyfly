// Package budget holds the plan and the plan-versus-actual report.
//
// One row per category per month, so a seasonal budget works; a year is seeded
// from one figure per category, because nobody should type 216 values
// (docs/03-data-model.md §3.4).
package budget

import (
	"context"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// Budget is one planned figure for one category in one month.
type Budget struct {
	ID           int64
	UserID       int64
	CategoryID   int64
	CategoryName string
	Period       period.Period
	Planned      money.Money
}

// BulkItem is one category's figure in a bulk seed.
type BulkItem struct {
	CategoryID int64
	Planned    money.Money
}

// BulkResult reports what a bulk seed wrote.
type BulkResult struct {
	PeriodsWritten int `json:"periods_written"`
	RowsWritten    int `json:"rows_written"`
}

// Repo is the storage contract.
type Repo interface {
	ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]Budget, error)
	Upsert(ctx context.Context, b Budget, now time.Time) (Budget, error)
	// UpsertMany writes a whole range in one transaction.
	UpsertMany(ctx context.Context, userID int64, periods []period.Period, items []BulkItem, now time.Time) (BulkResult, error)
	Delete(ctx context.Context, userID, categoryID int64, p period.Period) error
}

// Service is the budget use-case layer.
type Service struct {
	repo       Repo
	categories *category.Service
	currencies *currency.Service
	clock      clock.Clock
}

// NewService builds the service.
func NewService(repo Repo, categories *category.Service, currencies *currency.Service, clk clock.Clock) *Service {
	return &Service{repo: repo, categories: categories, currencies: currencies, clock: clk}
}

// ListByPeriod returns the plan for one month.
func (s *Service) ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]Budget, error) {
	if !p.Valid() {
		return nil, apperr.Validation("period", "must be a month in YYYY-MM form, got %q", p)
	}
	return s.repo.ListByPeriod(ctx, userID, p)
}

// Upsert sets one category's plan for one month.
func (s *Service) Upsert(ctx context.Context, userID, categoryID int64, p period.Period, planned money.Money) (Budget, error) {
	if err := s.validate(ctx, userID, categoryID, p, planned); err != nil {
		return Budget{}, err
	}
	return s.repo.Upsert(ctx, Budget{
		UserID: userID, CategoryID: categoryID, Period: p, Planned: planned,
	}, s.clock.Now())
}

// Delete removes a plan, which is different from setting it to zero: an absent
// plan is "not planned", a zero plan is "planned to spend nothing".
func (s *Service) Delete(ctx context.Context, userID, categoryID int64, p period.Period) error {
	if !p.Valid() {
		return apperr.Validation("period", "must be a month in YYYY-MM form, got %q", p)
	}
	return s.repo.Delete(ctx, userID, categoryID, p)
}

// Bulk applies one figure per category to every month in [from, to] — the
// workbook holds a single annual figure per category, and this is how it lands
// without 216 keystrokes.
func (s *Service) Bulk(ctx context.Context, userID int64, from, to period.Period, items []BulkItem) (BulkResult, error) {
	periods, err := from.Until(to)
	if err != nil {
		return BulkResult{}, err
	}
	if len(items) == 0 {
		return BulkResult{}, apperr.Validation("items", "must contain at least one category")
	}
	seen := map[int64]bool{}
	for _, it := range items {
		if seen[it.CategoryID] {
			return BulkResult{}, apperr.Validation("items", "category %d appears twice", it.CategoryID)
		}
		seen[it.CategoryID] = true
		if err := s.validate(ctx, userID, it.CategoryID, from, it.Planned); err != nil {
			return BulkResult{}, err
		}
	}
	return s.repo.UpsertMany(ctx, userID, periods, items, s.clock.Now())
}

func (s *Service) validate(ctx context.Context, userID, categoryID int64, p period.Period, planned money.Money) error {
	v := &apperr.ValidationError{}
	if !p.Valid() {
		v.Add("period", "must be a month in YYYY-MM form, got %q", p)
	}
	if planned.Minor < 0 {
		v.Add("planned.amount_minor", "must not be negative")
	}
	if strings.TrimSpace(planned.Currency) == "" {
		v.Add("planned.currency", "is required")
	} else if !s.currencies.Exists(ctx, planned.Currency) {
		v.Add("planned.currency", "unknown currency %q", planned.Currency)
	}
	if err := v.OrNil(); err != nil {
		return err
	}
	cat, err := s.categories.Get(ctx, userID, categoryID)
	if err != nil {
		return err
	}
	if cat.ArchivedAt != nil {
		return apperr.Validation("category_id", "%q is archived", cat.Name)
	}
	return nil
}
