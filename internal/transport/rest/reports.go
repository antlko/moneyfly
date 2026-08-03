package rest

import (
	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/setting"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func (s *Server) registerReportRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged()}

	reports := api.Group("/reports", guard...)
	reports.Get("/summary", s.handleReportSummary)
	reports.Get("/categories", s.handleReportCategories)
}

// PeriodSummaryDTO is one month of the roll-up rows: the workbook's 27, 31, 32
// and 33 for a single column.
type PeriodSummaryDTO struct {
	Period string `json:"period"`
	// Recorded is false for a month with no data. Every money field below is then
	// null — never 0, and never the workbook's -1.
	Recorded        bool      `json:"recorded"`
	SpendTotal      *MoneyDTO `json:"spend_total"`
	PlannedTotal    MoneyDTO  `json:"planned_total"`
	PossibleMinimum MoneyDTO  `json:"possible_minimum"`
	Income          *MoneyDTO `json:"income"`
	Diff            *MoneyDTO `json:"diff"`
	SavedPercent    *float64  `json:"saved_percent"`
	Unconverted     int       `json:"unconverted"`
}

// ReportSummaryDTO is the multi-period roll-up.
type ReportSummaryDTO struct {
	From         string             `json:"from"`
	To           string             `json:"to"`
	BaseCurrency string             `json:"base_currency"`
	FiscalYear   string             `json:"fiscal_year"`
	Periods      []PeriodSummaryDTO `json:"periods"`
}

// CategoryCellDTO is one cell of the year grid.
type CategoryCellDTO struct {
	Period string `json:"period"`
	// Actual is null for an unrecorded month. The grid renders that blank; it
	// must never render 0 (docs/08-ux.md §8.1).
	Actual  *MoneyDTO `json:"actual"`
	Planned *MoneyDTO `json:"planned"`
	Ratio   *float64  `json:"ratio"`
	State   string    `json:"state"`
	Label   string    `json:"state_label"`
}

// CategorySeriesDTO is one row of the year grid: the workbook's rows 9-26.
type CategorySeriesDTO struct {
	CategoryID  int64   `json:"category_id"`
	Name        string  `json:"name"`
	Icon        *string `json:"icon"`
	Color       *string `json:"color"`
	IsEssential bool    `json:"is_essential"`
	Archived    bool    `json:"archived"`
	// Average is column C and Total is column T, both over recorded months only.
	Average *MoneyDTO         `json:"average"`
	Total   *MoneyDTO         `json:"total"`
	Cells   []CategoryCellDTO `json:"cells"`
}

// ReportCategoriesDTO is the whole grid.
type ReportCategoriesDTO struct {
	From         string              `json:"from"`
	To           string              `json:"to"`
	BaseCurrency string              `json:"base_currency"`
	Periods      []string            `json:"periods"`
	Recorded     map[string]bool     `json:"recorded"`
	Categories   []CategorySeriesDTO `json:"categories"`
	Summary      []PeriodSummaryDTO  `json:"summary"`
}

// reportRange reads from/to, defaulting to the fiscal year containing the
// user's most recent data — which is the grid anyone opening the screen wants.
func (s *Server) reportRange(c *fiber.Ctx) (from, to period.Period, err error) {
	u := currentUser(c)
	rawFrom, rawTo := c.Query("from"), c.Query("to")

	if rawFrom != "" {
		if from, err = period.ParsePeriod(rawFrom); err != nil {
			return "", "", err
		}
	}
	if rawTo != "" {
		if to, err = period.ParsePeriod(rawTo); err != nil {
			return "", "", err
		}
	}
	if rawFrom != "" && rawTo != "" {
		if to < from {
			return "", "", apperr.Validation("to", "must not be before from (%s < %s)", to, from)
		}
		return from, to, nil
	}

	// Anchor on the most recent data, falling back to today for an empty
	// account: a first-run grid shows this fiscal year, blank.
	anchor := period.FromTime(s.deps.Clock.Now())
	if _, last, ok, err := s.deps.Metrics.RecordedRange(c.UserContext(), u.ID); err != nil {
		return "", "", err
	} else if ok {
		anchor = last
	}
	defaultFrom, defaultTo := fiscalYear(anchor, u.FiscalYearStartMonth)
	if rawFrom == "" {
		from = defaultFrom
	}
	if rawTo == "" {
		to = defaultTo
	}
	if to < from {
		return "", "", apperr.Validation("to", "must not be before from (%s < %s)", to, from)
	}
	return from, to, nil
}

// fiscalYearStart returns the first month of the fiscal year containing p.
func fiscalYearStart(p period.Period, startMonth int) period.Period {
	if startMonth < 1 || startMonth > 12 {
		startMonth = 1
	}
	t := p.Time()
	year := t.Year()
	if int(t.Month()) < startMonth {
		year--
	}
	return period.FromTime(t.AddDate(year-t.Year(), startMonth-int(t.Month()), 0))
}

// fiscalYear returns the twelve months of the fiscal year containing p — the
// workbook's August-to-July layout when the start month is 8.
func fiscalYear(p period.Period, startMonth int) (from, to period.Period) {
	from = fiscalYearStart(p, startMonth)
	to = from
	for i := 0; i < 11; i++ {
		to = to.Next()
	}
	return from, to
}

func (s *Server) handleReportSummary(c *fiber.Ctx) error {
	u := currentUser(c)
	from, to, err := s.reportRange(c)
	if err != nil {
		return err
	}
	data, summary, err := s.deps.Metrics.Load(c.UserContext(), u.ID, from, to, u.BaseCurrency)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	return c.JSON(ReportSummaryDTO{
		From: from.String(), To: to.String(), BaseCurrency: u.BaseCurrency,
		FiscalYear: from.FiscalYearLabel(u.FiscalYearStartMonth),
		Periods:    periodSummaries(data, summary, exponents),
	})
}

func (s *Server) handleReportCategories(c *fiber.Ctx) error {
	u := currentUser(c)
	from, to, err := s.reportRange(c)
	if err != nil {
		return err
	}
	data, summary, err := s.deps.Metrics.Load(c.UserContext(), u.ID, from, to, u.BaseCurrency)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	th := s.thresholds(c)

	out := ReportCategoriesDTO{
		From: from.String(), To: to.String(), BaseCurrency: u.BaseCurrency,
		Periods:    make([]string, 0, len(data.Periods)),
		Recorded:   make(map[string]bool, len(data.Periods)),
		Categories: make([]CategorySeriesDTO, 0, len(data.Categories)),
		Summary:    periodSummaries(data, summary, exponents),
	}
	for _, p := range data.Periods {
		out.Periods = append(out.Periods, p.String())
		out.Recorded[p.String()] = data.Recorded[p]
	}

	for _, cat := range data.Categories {
		if cat.Kind != category.KindExpense {
			continue
		}
		row := CategorySeriesDTO{
			CategoryID: cat.ID, Name: cat.Name, IsEssential: cat.IsEssential,
			Archived: cat.ArchivedAt != nil,
			Average:  toMoneyPtr(metrics.Average(data, cat.ID), exponents),
			Total:    toMoneyPtr(metrics.Total(data, cat.ID), exponents),
			Cells:    make([]CategoryCellDTO, 0, len(data.Periods)),
		}
		if cat.Icon != "" {
			icon := cat.Icon
			row.Icon = &icon
		}
		if cat.Color != "" {
			color := cat.Color
			row.Color = &color
		}
		for _, p := range data.Periods {
			actual := metrics.Spend(data, cat.ID, p)
			plannedValue, hasPlan := data.Planned[cat.ID][p]
			cell := CategoryCellDTO{Period: p.String(), Actual: toMoneyPtr(actual, exponents)}
			if hasPlan {
				value := plannedValue
				cell.Planned = toMoneyPtr(&value, exponents)
				cell.Ratio = metrics.Ratio(actual, &value)
				cell.State = string(metrics.State(actual, &value, th))
			} else {
				cell.State = string(metrics.State(actual, nil, th))
			}
			cell.Label = budget.Label(budget.State(cell.State))
			row.Cells = append(row.Cells, cell)
		}
		out.Categories = append(out.Categories, row)
	}
	return c.JSON(out)
}

// periodSummaries builds the roll-up rows shared by both report endpoints.
func periodSummaries(data metrics.Data, summary metrics.Summary, exponents map[string]int) []PeriodSummaryDTO {
	out := make([]PeriodSummaryDTO, 0, len(data.Periods))
	for _, p := range data.Periods {
		out = append(out, PeriodSummaryDTO{
			Period:          p.String(),
			Recorded:        data.Recorded[p],
			SpendTotal:      toMoneyPtr(metrics.SpendTotal(data, p), exponents),
			PlannedTotal:    toMoney(metrics.PlannedTotal(data, p), exponents),
			PossibleMinimum: toMoney(metrics.PossibleMinimum(data, p), exponents),
			Income:          toMoneyPtr(metrics.Income(data, p), exponents),
			Diff:            toMoneyPtr(metrics.Diff(data, p), exponents),
			SavedPercent:    metrics.SavedPercent(data, p),
			Unconverted:     summary.Unconverted[p],
		})
	}
	return out
}

// thresholds reads the amber band and the over multiplier from the user's own
// settings, falling back to the config defaults.
//
// Reading them per request is what makes a change apply without a restart: they
// were config-only until stage 07 (docs/implementation-plan/07-fx-automation.md §9).
func (s *Server) thresholds(c *fiber.Ctx) budget.Thresholds {
	out := budget.Thresholds{
		WarnPercent:    s.deps.Config.Reports.WarnPercent,
		OverMultiplier: s.deps.Config.Reports.OverMultiplier,
	}
	u := currentUser(c)
	if s.deps.Settings == nil || u == nil {
		return out
	}
	ctx := c.UserContext()
	out.WarnPercent = s.deps.Settings.Float(ctx, u.ID, setting.KeyWarnPercent, out.WarnPercent)
	out.OverMultiplier = s.deps.Settings.Float(ctx, u.ID, setting.KeyOverMultiplier, out.OverMultiplier)
	return out
}
