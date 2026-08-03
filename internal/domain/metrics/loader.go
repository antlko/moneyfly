package metrics

import (
	"context"

	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// PeriodSpend is one period's aggregate for one user, as the store returns it.
//
// Recorded is carried explicitly rather than inferred from a zero total: a month
// in which every transaction was a transfer has data and no spend, and that is
// not the same as a month with nothing in it.
type PeriodSpend struct {
	Period   period.Period
	Recorded bool
	// ByCategory is base-currency spend per category, present only for
	// categories with at least one row. A missing key inside a recorded period is
	// a recorded zero.
	ByCategory map[int64]money.Money
	// AverageBaseByCategory is the same, less the rows flagged
	// exclude_from_average.
	AverageBaseByCategory map[int64]money.Money
	Income                money.Money
	// Unconverted counts rows with no base amount because no rate covered their
	// date. They are excluded from the totals and reported, never treated as zero.
	Unconverted int
}

// Loader is the storage contract. Every method must report an unrecorded period
// as unrecorded — never COALESCE it to zero.
type Loader interface {
	// SpendByPeriod returns one entry per period in [from, to].
	SpendByPeriod(ctx context.Context, userID int64, from, to period.Period, baseCurrency string) ([]PeriodSpend, error)
	// RecordedPeriods returns every period the user has any transaction in.
	RecordedPeriods(ctx context.Context, userID int64) ([]period.Period, error)
	// PlannedByPeriod returns category -> period -> plan across [from, to].
	PlannedByPeriod(ctx context.Context, userID int64, from, to period.Period) (map[int64]map[period.Period]money.Money, error)
}

// CategorySource supplies the chart of categories.
type CategorySource interface {
	List(ctx context.Context, userID int64, kind category.Kind, includeArchived bool) ([]category.Category, error)
}

// Service loads a Data snapshot. It is the only part of this package that
// touches a collaborator; every metric above is pure over what it returns.
type Service struct {
	loader     Loader
	categories CategorySource
}

// NewService builds the metrics service.
func NewService(loader Loader, categories CategorySource) *Service {
	return &Service{loader: loader, categories: categories}
}

// MaxPeriods bounds a report range. Ten years of months is far past any useful
// screen and well short of a runaway query.
const MaxPeriods = 120

// Load builds the snapshot for [from, to] inclusive.
//
// Archived categories are included: a category archived in March still holds
// January's spending, and a year grid that dropped it would silently lose money.
func (s *Service) Load(ctx context.Context, userID int64, from, to period.Period, baseCurrency string) (Data, Summary, error) {
	periods, err := from.Until(to)
	if err != nil {
		return Data{}, Summary{}, err
	}
	if len(periods) > MaxPeriods {
		return Data{}, Summary{}, apperr.Validation("to",
			"a report may span at most %d months, got %d", MaxPeriods, len(periods))
	}

	cats, err := s.categories.List(ctx, userID, "", true)
	if err != nil {
		return Data{}, Summary{}, err
	}
	rows, err := s.loader.SpendByPeriod(ctx, userID, from, to, baseCurrency)
	if err != nil {
		return Data{}, Summary{}, err
	}
	plans, err := s.loader.PlannedByPeriod(ctx, userID, from, to)
	if err != nil {
		return Data{}, Summary{}, err
	}

	data := Data{
		BaseCurrency: baseCurrency,
		Periods:      periods,
		Recorded:     make(map[period.Period]bool, len(periods)),
		Categories:   cats,
		Spend:        map[int64]map[period.Period]money.Money{},
		AverageBase:  map[int64]map[period.Period]money.Money{},
		Income:       map[period.Period]money.Money{},
		Planned:      plans,
	}
	if data.Planned == nil {
		data.Planned = map[int64]map[period.Period]money.Money{}
	}

	summary := Summary{Unconverted: map[period.Period]int{}}
	for _, row := range rows {
		data.Recorded[row.Period] = row.Recorded
		if !row.Recorded {
			continue
		}
		for id, amount := range row.ByCategory {
			if data.Spend[id] == nil {
				data.Spend[id] = map[period.Period]money.Money{}
			}
			data.Spend[id][row.Period] = amount
		}
		for id, amount := range row.AverageBaseByCategory {
			if data.AverageBase[id] == nil {
				data.AverageBase[id] = map[period.Period]money.Money{}
			}
			data.AverageBase[id][row.Period] = amount
		}
		data.Income[row.Period] = row.Income
		summary.Unconverted[row.Period] = row.Unconverted
	}
	return data, summary, nil
}

// Summary carries the facts about a load that are not metrics: how many rows
// could not be converted, per period.
type Summary struct {
	Unconverted map[period.Period]int
}

// RecordedRange returns the first and last period the user has any data in.
//
// ok is false when there is no data at all — which is a fact about the account,
// not an error, and the caller renders an empty state rather than a failure.
func (s *Service) RecordedRange(ctx context.Context, userID int64) (from, to period.Period, ok bool, err error) {
	periods, err := s.loader.RecordedPeriods(ctx, userID)
	if err != nil {
		return "", "", false, err
	}
	if len(periods) == 0 {
		return "", "", false, nil
	}
	return periods[0], periods[len(periods)-1], true, nil
}

// AverageWindow is the range averages are taken over: the whole recorded
// history, capped to the most recent MaxPeriods and always including anchor so
// the period on screen is part of its own average.
func (s *Service) AverageWindow(ctx context.Context, userID int64, anchor period.Period) (from, to period.Period, ok bool, err error) {
	first, last, ok, err := s.RecordedRange(ctx, userID)
	if err != nil || !ok {
		return "", "", false, err
	}
	if anchor.Valid() {
		if anchor < first {
			first = anchor
		}
		if anchor > last {
			last = anchor
		}
	}
	// Walk back from the end rather than forward from the start: recent months
	// are the ones a forecast should be built on.
	cur := last
	for i := 1; i < MaxPeriods && cur > first; i++ {
		cur = cur.Prev()
	}
	if cur > first {
		first = cur
	}
	return first, last, true, nil
}
