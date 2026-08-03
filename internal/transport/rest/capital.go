package rest

import (
	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func (s *Server) registerCapitalRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged(), s.requireWritable()}

	g := api.Group("/snapshots", guard...)
	g.Get("/", s.handleListSnapshots)
	g.Post("/bulk", s.handleBulkSnapshots)
	g.Get("/:period/reconciliation", s.handleReconciliation)
	g.Put("/:account_id/:period", s.handleUpsertSnapshot)
	g.Delete("/:account_id/:period", s.handleDeleteSnapshot)

	reports := api.Group("/reports", guard...)
	reports.Get("/capital", s.handleCapitalReport)
	reports.Get("/capital/series", s.handleCapitalSeries)
	reports.Get("/net-worth-in/:currency", s.handleNetWorthIn)
}

// SnapshotDTO is one account's recorded balance for one month.
type SnapshotDTO struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	AssetClass  string `json:"asset_class"`
	Currency    string `json:"currency"`
	Period      string `json:"period"`
	// Amount is null when the row records a quantity instead, and when no figure
	// has been entered for this month at all.
	Amount       *MoneyDTO `json:"amount"`
	QuantityNano *int64    `json:"quantity_nano"`
	BaseAmount   *MoneyDTO `json:"base_amount"`
	// PreviousAmount is last month's figure, so the entry screen is
	// confirm-or-adjust rather than retype (docs/08-ux.md §8.5).
	PreviousAmount *MoneyDTO `json:"previous_amount"`
	IsLiquid       bool      `json:"is_liquid"`
	CountsTowardNW bool      `json:"counts_toward_net_worth"`
	// Computed marks a parent account: its value is the sum of its children and
	// cannot be written directly.
	Computed bool      `json:"computed"`
	Note     string    `json:"note,omitempty"`
	Value    *MoneyDTO `json:"value"`
}

// ShareDTO is one slice of the allocation.
type ShareDTO struct {
	AccountID  int64    `json:"account_id"`
	Name       string   `json:"name"`
	AssetClass string   `json:"asset_class"`
	Value      MoneyDTO `json:"value"`
	Share      float64  `json:"share"`
}

// CapitalChangeDTO splits the month's movement into what was saved and what the
// currencies did.
type CapitalChangeDTO struct {
	Recorded bool      `json:"recorded"`
	Total    *MoneyDTO `json:"total"`
	Real     *MoneyDTO `json:"real"`
	FX       *MoneyDTO `json:"fx"`
}

// CapitalReportDTO is the capital screen's payload.
type CapitalReportDTO struct {
	Period       string `json:"period"`
	BaseCurrency string `json:"base_currency"`
	// Valuation says which rate the historical balances were converted at.
	Valuation string `json:"valuation"`
	// General and ReadyForUsage are null for a month with no snapshots at all.
	General       *MoneyDTO        `json:"general"`
	ReadyForUsage *MoneyDTO        `json:"ready_for_usage"`
	RunwayMonths  *float64         `json:"runway_months"`
	BurnMode      string           `json:"burn_mode"`
	BurnRate      *MoneyDTO        `json:"burn_rate"`
	Change        CapitalChangeDTO `json:"change"`
	Allocation    []ShareDTO       `json:"allocation"`
	Accounts      []SnapshotDTO    `json:"accounts"`
}

// CapitalSeriesPointDTO is one month of the capital series.
type CapitalSeriesPointDTO struct {
	Period        string           `json:"period"`
	Recorded      bool             `json:"recorded"`
	General       *MoneyDTO        `json:"general"`
	ReadyForUsage *MoneyDTO        `json:"ready_for_usage"`
	RunwayMonths  *float64         `json:"runway_months"`
	Change        CapitalChangeDTO `json:"change"`
}

// CapitalSeriesDTO is the capital half over a range.
type CapitalSeriesDTO struct {
	From         string                  `json:"from"`
	To           string                  `json:"to"`
	BaseCurrency string                  `json:"base_currency"`
	BurnMode     string                  `json:"burn_mode"`
	Points       []CapitalSeriesPointDTO `json:"points"`
}

// SnapshotInputDTO is the create/update payload.
type SnapshotInputDTO struct {
	Amount       *MoneyDTO `json:"amount"`
	QuantityNano *int64    `json:"quantity_nano"`
	Note         string    `json:"note"`
}

func (d SnapshotInputDTO) toInput(exponents map[string]int) (capital.Input, error) {
	in := capital.Input{QuantityNano: d.QuantityNano, Note: d.Note}
	if d.Amount != nil {
		amount, err := d.Amount.Money("amount", exponents)
		if err != nil {
			return capital.Input{}, err
		}
		in.Amount = &amount
	}
	return in, nil
}

// burnMode reads the requested burn mode, defaulting to the trailing three
// months. legacy_blend is accepted so the sheet's own figure can be reproduced,
// and is labelled as legacy wherever it is shown.
func burnMode(c *fiber.Ctx) (capital.BurnMode, error) {
	raw := c.Query("burn_mode")
	if raw == "" {
		return capital.DefaultBurnMode, nil
	}
	mode := capital.BurnMode(raw)
	if !mode.Valid() {
		return "", apperr.Validation("burn_mode",
			"must be one of essential_planned|actual_trailing_3|actual_trailing_12|legacy_blend, got %q", raw)
	}
	return mode, nil
}

// valuationOf reads the valuation mode, defaulting to the user's own setting and
// then to contemporaneous.
//
// `constant` reproduces the workbook, which revalued every historical balance
// whenever a rate cell changed. Both derive from the same rows, so this is a
// read-time choice and never stored data.
func valuationOf(c *fiber.Ctx) (capital.Valuation, error) {
	raw := c.Query("valuation")
	if raw == "" {
		return capital.Contemporaneous, nil
	}
	v := capital.Valuation(raw)
	if !v.Valid() {
		return "", apperr.Validation("valuation",
			"must be contemporaneous or constant, got %q", raw)
	}
	return v, nil
}

// capitalData loads the metrics snapshot the burn rates need, then the capital
// one. Both are one batched load.
func (s *Server) capitalData(c *fiber.Ctx, from, to period.Period) (capital.Data, error) {
	u := currentUser(c)
	valuation, err := valuationOf(c)
	if err != nil {
		return capital.Data{}, err
	}
	// Burn rates are means over history, so the metrics window is the recorded
	// range rather than the requested one.
	md := metrics.Data{}
	mFrom, mTo, ok, err := s.deps.Metrics.AverageWindow(c.UserContext(), u.ID, to)
	if err != nil {
		return capital.Data{}, err
	}
	if ok {
		if mFrom > from {
			mFrom = from
		}
		md, _, err = s.deps.Metrics.Load(c.UserContext(), u.ID, mFrom, mTo, u.BaseCurrency)
		if err != nil {
			return capital.Data{}, err
		}
	}
	return s.deps.Capital.Load(c.UserContext(), u.ID, from, to, u.BaseCurrency, md, valuation)
}

func (s *Server) handleListSnapshots(c *fiber.Ctx) error {
	u := currentUser(c)
	p, err := period.ParsePeriod(c.Query("period"))
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}

	accounts, err := s.deps.Accounts.List(c.UserContext(), u.ID, false)
	if err != nil {
		return err
	}
	current, err := s.deps.Capital.ListByPeriod(c.UserContext(), u.ID, p)
	if err != nil {
		return err
	}
	previous, err := s.deps.Capital.ListByPeriod(c.UserContext(), u.ID, p.Prev())
	if err != nil {
		return err
	}
	byAccount := map[int64]capital.Snapshot{}
	for _, snap := range current {
		byAccount[snap.AccountID] = snap
	}
	priorByAccount := map[int64]capital.Snapshot{}
	for _, snap := range previous {
		priorByAccount[snap.AccountID] = snap
	}

	data, err := s.capitalData(c, p, p)
	if err != nil {
		return err
	}

	out := make([]SnapshotDTO, 0, len(accounts))
	for _, a := range accounts {
		dto := SnapshotDTO{
			AccountID: a.ID, AccountName: a.Name, AssetClass: string(a.AssetClass),
			Currency: a.Currency, Period: p.String(),
			IsLiquid: a.IsLiquid, CountsTowardNW: a.CountsTowardNetWorth,
			Computed: a.HasChildren,
			Value:    toMoneyPtr(capital.Value(data, a.ID, p), exponents),
		}
		if snap, ok := byAccount[a.ID]; ok {
			dto.Amount = toMoneyPtr(snap.Amount, exponents)
			dto.QuantityNano = snap.QuantityNano
			dto.BaseAmount = toMoneyPtr(snap.BaseAmount, exponents)
			dto.Note = snap.Note
		}
		if prior, ok := priorByAccount[a.ID]; ok {
			dto.PreviousAmount = toMoneyPtr(prior.Amount, exponents)
		}
		out = append(out, dto)
	}
	return c.JSON(out)
}

func (s *Server) handleUpsertSnapshot(c *fiber.Ctx) error {
	u := currentUser(c)
	accountID, err := paramInt64(c, "account_id")
	if err != nil {
		return err
	}
	p, err := period.ParsePeriod(c.Params("period"))
	if err != nil {
		return err
	}
	var body SnapshotInputDTO
	if err := bind(c, &body); err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	in, err := body.toInput(exponents)
	if err != nil {
		return err
	}
	snap, err := s.deps.Capital.Upsert(c.UserContext(), u.ID, accountID, p, u.BaseCurrency, in)
	if err != nil {
		return err
	}
	return c.JSON(SnapshotDTO{
		AccountID: snap.AccountID, Period: snap.Period.String(),
		Amount: toMoneyPtr(snap.Amount, exponents), QuantityNano: snap.QuantityNano,
		BaseAmount: toMoneyPtr(snap.BaseAmount, exponents), Note: snap.Note,
	})
}

func (s *Server) handleBulkSnapshots(c *fiber.Ctx) error {
	u := currentUser(c)
	var body struct {
		Period string `json:"period"`
		Items  []struct {
			AccountID int64 `json:"account_id"`
			SnapshotInputDTO
		} `json:"items"`
	}
	if err := bind(c, &body); err != nil {
		return err
	}
	p, err := period.ParsePeriod(body.Period)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}

	items := make(map[int64]capital.Input, len(body.Items))
	for _, item := range body.Items {
		in, err := item.toInput(exponents)
		if err != nil {
			return err
		}
		items[item.AccountID] = in
	}
	written, err := s.deps.Capital.Bulk(c.UserContext(), u.ID, p, u.BaseCurrency, items)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"period": p.String(), "rows_written": written})
}

func (s *Server) handleDeleteSnapshot(c *fiber.Ctx) error {
	u := currentUser(c)
	accountID, err := paramInt64(c, "account_id")
	if err != nil {
		return err
	}
	p, err := period.ParsePeriod(c.Params("period"))
	if err != nil {
		return err
	}
	if err := s.deps.Capital.Delete(c.UserContext(), u.ID, accountID, p); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// DriftDTO is one account's reconciliation row.
type DriftDTO struct {
	AccountID int64     `json:"account_id"`
	Name      string    `json:"name"`
	Snapshot  *MoneyDTO `json:"snapshot"`
	Implied   *MoneyDTO `json:"implied"`
	// Difference is snapshot minus implied. The snapshot always wins; this is
	// informational and nothing is auto-corrected.
	Difference *MoneyDTO `json:"difference"`
}

func (s *Server) handleReconciliation(c *fiber.Ctx) error {
	u := currentUser(c)
	p, err := period.ParsePeriod(c.Params("period"))
	if err != nil {
		return err
	}
	drift, err := s.deps.Capital.Reconciliation(c.UserContext(), u.ID, p, u.BaseCurrency)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	out := make([]DriftDTO, 0, len(drift))
	for _, d := range drift {
		out = append(out, DriftDTO{
			AccountID: d.AccountID, Name: d.Name,
			Snapshot:   toMoneyPtr(d.Snapshot, exponents),
			Implied:    toMoneyPtr(d.Implied, exponents),
			Difference: toMoneyPtr(d.Difference, exponents),
		})
	}
	return c.JSON(fiber.Map{
		"period": p.String(),
		"note":   "The snapshot is authoritative. Drift is reported, never corrected.",
		"items":  out,
	})
}

func (s *Server) handleCapitalReport(c *fiber.Ctx) error {
	u := currentUser(c)
	p, err := period.ParsePeriod(c.Query("period"))
	if err != nil {
		return err
	}
	mode, err := burnMode(c)
	if err != nil {
		return err
	}
	data, err := s.capitalData(c, p, p)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}

	valuation, err := valuationOf(c)
	if err != nil {
		return err
	}
	out := CapitalReportDTO{
		Period: p.String(), BaseCurrency: u.BaseCurrency, BurnMode: string(mode),
		Valuation:     string(valuation),
		General:       toMoneyPtr(capital.General(data, p), exponents),
		ReadyForUsage: toMoneyPtr(capital.ReadyForUsage(data, p), exponents),
		RunwayMonths:  capital.Runway(data, p, mode),
		Change:        toChangeDTO(capital.Change(data, p), exponents),
		Allocation:    toShareDTOs(capital.Allocation(data, p), exponents),
		Accounts:      accountRows(data, p, exponents),
	}
	if rates, ok := data.Burn[p]; ok {
		if rate, ok := rates[mode]; ok {
			out.BurnRate = toMoneyPtr(&rate, exponents)
		}
	}
	return c.JSON(out)
}

func (s *Server) handleCapitalSeries(c *fiber.Ctx) error {
	u := currentUser(c)
	from, to, err := s.reportRange(c)
	if err != nil {
		return err
	}
	mode, err := burnMode(c)
	if err != nil {
		return err
	}
	data, err := s.capitalData(c, from, to)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}

	out := CapitalSeriesDTO{
		From: from.String(), To: to.String(), BaseCurrency: u.BaseCurrency,
		BurnMode: string(mode),
		Points:   make([]CapitalSeriesPointDTO, 0, len(data.Periods)),
	}
	for _, p := range data.Periods {
		out.Points = append(out.Points, CapitalSeriesPointDTO{
			Period: p.String(), Recorded: data.Recorded[p],
			General:       toMoneyPtr(capital.General(data, p), exponents),
			ReadyForUsage: toMoneyPtr(capital.ReadyForUsage(data, p), exponents),
			RunwayMonths:  capital.Runway(data, p, mode),
			Change:        toChangeDTO(capital.Change(data, p), exponents),
		})
	}
	return c.JSON(out)
}

func (s *Server) handleNetWorthIn(c *fiber.Ctx) error {
	u := currentUser(c)
	code := c.Params("currency")
	p, err := period.ParsePeriod(c.Query("period"))
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	if _, ok := exponents[normaliseCurrency(code)]; !ok {
		return apperr.Validation("currency", "unknown currency %q", code)
	}
	data, err := s.capitalData(c, p, p)
	if err != nil {
		return err
	}
	converted, err := s.deps.Capital.GeneralInCurrency(c.UserContext(), data, normaliseCurrency(code), p)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"period":        p.String(),
		"currency":      normaliseCurrency(code),
		"base_currency": u.BaseCurrency,
		"general":       toMoneyPtr(capital.General(data, p), exponents),
		"value":         toMoneyPtr(converted, exponents),
	})
}

func toChangeDTO(ch capital.CapitalChange, exponents map[string]int) CapitalChangeDTO {
	out := CapitalChangeDTO{Recorded: ch.Recorded}
	if !ch.Recorded {
		return out
	}
	out.Total = toMoneyPtr(&ch.Total, exponents)
	out.Real = toMoneyPtr(&ch.Real, exponents)
	out.FX = toMoneyPtr(&ch.FX, exponents)
	return out
}

func toShareDTOs(shares []capital.Share, exponents map[string]int) []ShareDTO {
	out := make([]ShareDTO, 0, len(shares))
	for _, sh := range shares {
		out = append(out, ShareDTO{
			AccountID: sh.AccountID, Name: sh.Name, AssetClass: string(sh.AssetClass),
			Value: toMoney(sh.Value, exponents), Share: sh.Share,
		})
	}
	return out
}

// accountRows lists every account with its value, including the computed
// parents — which is where the rollup becomes visible.
func accountRows(data capital.Data, p period.Period, exponents map[string]int) []SnapshotDTO {
	out := make([]SnapshotDTO, 0, len(data.Accounts))
	for _, a := range data.Accounts {
		if a.ArchivedAt != nil {
			continue
		}
		row := SnapshotDTO{
			AccountID: a.ID, AccountName: a.Name, AssetClass: string(a.AssetClass),
			Currency: a.Currency, Period: p.String(),
			IsLiquid: a.IsLiquid, CountsTowardNW: a.CountsTowardNetWorth,
			Computed: len(data.Children[a.ID]) > 0,
			Value:    toMoneyPtr(capital.Value(data, a.ID, p), exponents),
		}
		if native, ok := data.Native[a.ID][p]; ok {
			amount := native
			row.Amount = toMoneyPtr(&amount, exponents)
		}
		out = append(out, row)
	}
	return out
}

// normaliseCurrency upper-cases a path parameter without pulling in the domain
// currency service for one string.
func normaliseCurrency(code string) string {
	out := []byte(code)
	for i := range out {
		if out[i] >= 'a' && out[i] <= 'z' {
			out[i] -= 32
		}
	}
	return string(out)
}
