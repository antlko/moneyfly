package rest

import (
	"net/http"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

func (s *Server) registerBudgetRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged(), s.requireWritable()}

	g := api.Group("/budgets", guard...)
	g.Get("/", s.handleListBudgets)
	g.Post("/bulk", s.handleBulkBudgets)
	g.Put("/:category_id/:period", s.handleUpsertBudget)
	g.Delete("/:category_id/:period", s.handleDeleteBudget)

	reports := api.Group("/reports", guard...)
	reports.Get("/budget", s.handleBudgetReport)
}

// BudgetDTO is a planned figure on the wire.
type BudgetDTO struct {
	CategoryID   int64    `json:"category_id"`
	CategoryName string   `json:"category_name"`
	Period       string   `json:"period"`
	Planned      MoneyDTO `json:"planned"`
}

func (s *Server) handleListBudgets(c *fiber.Ctx) error {
	p, err := period.ParsePeriod(c.Query("period"))
	if err != nil {
		return err
	}
	u := currentUser(c)
	budgets, err := s.deps.Budgets.ListByPeriod(c.UserContext(), u.ID, p)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	out := make([]BudgetDTO, 0, len(budgets))
	for _, b := range budgets {
		out = append(out, BudgetDTO{
			CategoryID: b.CategoryID, CategoryName: b.CategoryName,
			Period: b.Period.String(), Planned: toMoney(b.Planned, exponents),
		})
	}
	return c.JSON(out)
}

func (s *Server) handleUpsertBudget(c *fiber.Ctx) error {
	categoryID, err := paramInt64(c, "category_id")
	if err != nil {
		return err
	}
	p, err := period.ParsePeriod(c.Params("period"))
	if err != nil {
		return err
	}
	var req struct {
		Planned MoneyDTO `json:"planned"`
	}
	if err := bind(c, &req); err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	planned, err := req.Planned.Money("planned", exponents)
	if err != nil {
		return err
	}
	u := currentUser(c)
	saved, err := s.deps.Budgets.Upsert(c.UserContext(), u.ID, categoryID, p, planned)
	if err != nil {
		return err
	}
	return c.JSON(BudgetDTO{
		CategoryID: saved.CategoryID, Period: saved.Period.String(),
		Planned: toMoney(saved.Planned, exponents),
	})
}

func (s *Server) handleDeleteBudget(c *fiber.Ctx) error {
	categoryID, err := paramInt64(c, "category_id")
	if err != nil {
		return err
	}
	p, err := period.ParsePeriod(c.Params("period"))
	if err != nil {
		return err
	}
	u := currentUser(c)
	if err := s.deps.Budgets.Delete(c.UserContext(), u.ID, categoryID, p); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) handleBulkBudgets(c *fiber.Ctx) error {
	var req struct {
		FromPeriod string `json:"from_period"`
		ToPeriod   string `json:"to_period"`
		Items      []struct {
			CategoryID int64    `json:"category_id"`
			Planned    MoneyDTO `json:"planned"`
		} `json:"items"`
	}
	if err := bind(c, &req); err != nil {
		return err
	}
	from, err := period.ParsePeriod(req.FromPeriod)
	if err != nil {
		return apperr.Validation("from_period", "must be a month in YYYY-MM form")
	}
	to, err := period.ParsePeriod(req.ToPeriod)
	if err != nil {
		return apperr.Validation("to_period", "must be a month in YYYY-MM form")
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	items := make([]budget.BulkItem, 0, len(req.Items))
	for _, item := range req.Items {
		planned, err := item.Planned.Money("planned", exponents)
		if err != nil {
			return err
		}
		items = append(items, budget.BulkItem{CategoryID: item.CategoryID, Planned: planned})
	}
	u := currentUser(c)
	result, err := s.deps.Budgets.Bulk(c.UserContext(), u.ID, from, to, items)
	if err != nil {
		return err
	}
	return c.JSON(result)
}

// BudgetReportDTO is the plan-versus-actual payload.
type BudgetReportDTO struct {
	Period       string `json:"period"`
	Valuation    string `json:"valuation"`
	BaseCurrency string `json:"base_currency"`
	// SpendTotal, Income, Diff and SavedPercent are null for a month that
	// recorded nothing. Absent is not zero (docs/09-api.md §9.1).
	SpendTotal      *MoneyDTO            `json:"spend_total"`
	PlannedTotal    MoneyDTO             `json:"planned_total"`
	Income          *MoneyDTO            `json:"income"`
	Diff            *MoneyDTO            `json:"diff"`
	SavedPercent    *float64             `json:"saved_percent"`
	PossibleMinimum MoneyDTO             `json:"possible_minimum"`
	Unconverted     int                  `json:"unconverted"`
	Categories      []BudgetReportRowDTO `json:"categories"`
}

// BudgetReportRowDTO is one category line of the report.
type BudgetReportRowDTO struct {
	CategoryID  int64     `json:"category_id"`
	Name        string    `json:"name"`
	Icon        *string   `json:"icon"`
	Color       *string   `json:"color"`
	IsEssential bool      `json:"is_essential"`
	Actual      *MoneyDTO `json:"actual"`
	Planned     *MoneyDTO `json:"planned"`
	// Average is the mean over every recorded month, the workbook's column C. It
	// is null until there is data to average, never 0.
	Average *MoneyDTO `json:"average"`
	Ratio   *float64  `json:"ratio"`
	State   string    `json:"state"`
	// Label is the accessible wording for the state, so colour is never the only
	// signal (docs/08-ux.md §8.1).
	Label string `json:"state_label"`
}

func (s *Server) handleBudgetReport(c *fiber.Ctx) error {
	p, err := period.ParsePeriod(c.Query("period"))
	if err != nil {
		return err
	}
	if v := c.Query("valuation"); v != "" && v != "contemporaneous" && v != "constant" {
		return apperr.Validation("valuation", "must be contemporaneous or constant")
	}
	u := currentUser(c)
	th := s.thresholds(c)
	report, err := s.deps.Budgets.Report(c.UserContext(), u.ID, p, u.BaseCurrency, s.deps.Actuals, th)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}

	// Averages span the whole recorded history, so they hold steady as the month
	// on screen changes. A separate load keeps the single-period report the one
	// query it has always been for an account with no history yet.
	averages := map[int64]*money.Money{}
	if s.deps.Metrics != nil {
		from, to, ok, err := s.deps.Metrics.AverageWindow(c.UserContext(), u.ID, p)
		if err != nil {
			return err
		}
		if ok {
			data, _, err := s.deps.Metrics.Load(c.UserContext(), u.ID, from, to, u.BaseCurrency)
			if err != nil {
				return err
			}
			for _, cat := range data.Categories {
				averages[cat.ID] = metrics.Average(data, cat.ID)
			}
		}
	}

	out := BudgetReportDTO{
		Period: report.Period.String(), Valuation: report.Valuation, BaseCurrency: report.BaseCurrency,
		SpendTotal:      toMoneyPtr(report.SpendTotal, exponents),
		PlannedTotal:    toMoney(report.PlannedTotal, exponents),
		Income:          toMoneyPtr(report.Income, exponents),
		Diff:            toMoneyPtr(report.Diff, exponents),
		SavedPercent:    report.SavedPercent,
		PossibleMinimum: toMoney(report.PossibleMinimum, exponents),
		Unconverted:     report.Unconverted,
		Categories:      make([]BudgetReportRowDTO, 0, len(report.Categories)),
	}
	for _, row := range report.Categories {
		dto := BudgetReportRowDTO{
			CategoryID: row.CategoryID, Name: row.Name, IsEssential: row.IsEssential,
			Actual: toMoneyPtr(row.Actual, exponents), Planned: toMoneyPtr(row.Planned, exponents),
			Average: toMoneyPtr(averages[row.CategoryID], exponents),
			Ratio:   row.Ratio, State: string(row.State), Label: budget.Label(row.State),
		}
		if row.Icon != "" {
			icon := row.Icon
			dto.Icon = &icon
		}
		if row.Color != "" {
			color := row.Color
			dto.Color = &color
		}
		out.Categories = append(out.Categories, dto)
	}
	return c.JSON(out)
}
