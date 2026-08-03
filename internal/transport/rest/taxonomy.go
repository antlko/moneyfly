package rest

import (
	"net/http"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func (s *Server) registerTaxonomyRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged(), s.requireWritable()}

	cats := api.Group("/categories", guard...)
	// The alias routes are declared before /:id so that "aliases" is never taken
	// for an id.
	cats.Get("/aliases", s.handleListCategoryAliases)
	cats.Post("/aliases", s.handleCreateCategoryAlias)
	cats.Delete("/aliases/:id", s.handleDeleteCategoryAlias)
	cats.Get("/", s.handleListCategories)
	cats.Post("/", s.handleCreateCategory)
	cats.Patch("/:id", s.handleUpdateCategory)
	cats.Delete("/:id", s.handleArchiveCategory)
	cats.Post("/:id/merge", s.handleMergeCategory)

	accs := api.Group("/accounts", guard...)
	accs.Get("/aliases", s.handleListAccountAliases)
	accs.Post("/aliases", s.handleCreateAccountAlias)
	accs.Delete("/aliases/:id", s.handleDeleteAccountAlias)
	accs.Get("/", s.handleListAccounts)
	accs.Post("/", s.handleCreateAccount)
	accs.Patch("/:id", s.handleUpdateAccount)
	accs.Delete("/:id", s.handleArchiveAccount)

	api.Get("/currencies", s.requireSession(), s.handleListCurrencies)
}

// CategoryDTO is a category on the wire.
type CategoryDTO struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	IsEssential bool    `json:"is_essential"`
	Icon        *string `json:"icon"`
	Color       *string `json:"color"`
	SortOrder   int     `json:"sort_order"`
	ParentID    *int64  `json:"parent_id"`
	ArchivedAt  *string `json:"archived_at"`
}

func toCategoryDTO(c category.Category) CategoryDTO {
	out := CategoryDTO{
		ID: c.ID, Name: c.Name, Kind: string(c.Kind), IsEssential: c.IsEssential,
		SortOrder: c.SortOrder, ParentID: c.ParentID,
	}
	if c.Icon != "" {
		icon := c.Icon
		out.Icon = &icon
	}
	if c.Color != "" {
		color := c.Color
		out.Color = &color
	}
	if c.ArchivedAt != nil {
		at := c.ArchivedAt.Format(timeLayoutWire)
		out.ArchivedAt = &at
	}
	return out
}

const timeLayoutWire = "2006-01-02T15:04:05Z07:00"

// CategoryInputDTO is the create/update payload.
type CategoryInputDTO struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	IsEssential bool   `json:"is_essential"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	SortOrder   *int   `json:"sort_order"`
	ParentID    *int64 `json:"parent_id"`
}

func (d CategoryInputDTO) toInput() category.Input {
	return category.Input{
		Name: d.Name, Kind: category.Kind(d.Kind), IsEssential: d.IsEssential,
		Icon: d.Icon, Color: d.Color, SortOrder: d.SortOrder, ParentID: d.ParentID,
	}
}

func (s *Server) handleListCategories(c *fiber.Ctx) error {
	u := currentUser(c)
	kind := category.Kind(c.Query("kind"))
	includeArchived := c.QueryBool("include_archived", false)
	cats, err := s.deps.Categories.List(c.UserContext(), u.ID, kind, includeArchived)
	if err != nil {
		return err
	}
	out := make([]CategoryDTO, 0, len(cats))
	for _, cat := range cats {
		out = append(out, toCategoryDTO(cat))
	}
	return c.JSON(out)
}

func (s *Server) handleCreateCategory(c *fiber.Ctx) error {
	var req CategoryInputDTO
	if err := bind(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	created, err := s.deps.Categories.Create(c.UserContext(), u.ID, req.toInput())
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(toCategoryDTO(created))
}

func (s *Server) handleUpdateCategory(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	var req CategoryInputDTO
	if err := bind(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	updated, err := s.deps.Categories.Update(c.UserContext(), u.ID, id, req.toInput())
	if err != nil {
		return err
	}
	return c.JSON(toCategoryDTO(updated))
}

func (s *Server) handleArchiveCategory(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	u := currentUser(c)
	if err := s.deps.Categories.Archive(c.UserContext(), u.ID, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) handleMergeCategory(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	var req struct {
		IntoID int64 `json:"into_id"`
	}
	if err := bind(c, &req); err != nil {
		return err
	}
	if req.IntoID == 0 {
		return apperr.Validation("into_id", "is required")
	}
	u := currentUser(c)
	if err := s.deps.Categories.Merge(c.UserContext(), u.ID, id, req.IntoID); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

// AliasDTO is an alias on the wire, for both categories and accounts.
type AliasDTO struct {
	ID         int64  `json:"id"`
	Source     string `json:"source"`
	SourceName string `json:"source_name"`
	TargetID   int64  `json:"target_id"`
	TargetName string `json:"target_name"`
}

func (s *Server) handleListCategoryAliases(c *fiber.Ctx) error {
	u := currentUser(c)
	aliases, err := s.deps.Categories.ListAliases(c.UserContext(), u.ID)
	if err != nil {
		return err
	}
	out := make([]AliasDTO, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, AliasDTO{
			ID: a.ID, Source: a.Source, SourceName: a.SourceName,
			TargetID: a.CategoryID, TargetName: a.TargetName,
		})
	}
	return c.JSON(out)
}

func (s *Server) handleCreateCategoryAlias(c *fiber.Ctx) error {
	var req struct {
		CategoryID int64  `json:"category_id"`
		SourceName string `json:"source_name"`
		Source     string `json:"source"`
	}
	if err := bind(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	created, err := s.deps.Categories.CreateAlias(c.UserContext(), u.ID, req.CategoryID, req.Source, req.SourceName)
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(AliasDTO{
		ID: created.ID, Source: created.Source, SourceName: created.SourceName, TargetID: created.CategoryID,
	})
}

func (s *Server) handleDeleteCategoryAlias(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	u := currentUser(c)
	if err := s.deps.Categories.DeleteAlias(c.UserContext(), u.ID, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

// AccountDTO is an account on the wire.
type AccountDTO struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	AssetClass           string    `json:"asset_class"`
	Currency             string    `json:"currency"`
	IsLiquid             bool      `json:"is_liquid"`
	CountsTowardNetWorth bool      `json:"counts_toward_net_worth"`
	PriceTicker          *string   `json:"price_ticker"`
	CostBasis            *MoneyDTO `json:"cost_basis"`
	ParentID             *int64    `json:"parent_id"`
	SortOrder            int       `json:"sort_order"`
	// IsComputed marks a parent: its value is the sum of its children and cannot
	// be written directly.
	IsComputed bool    `json:"is_computed"`
	ArchivedAt *string `json:"archived_at"`
}

func toAccountDTO(a account.Account, exponents map[string]int) AccountDTO {
	out := AccountDTO{
		ID: a.ID, Name: a.Name, AssetClass: string(a.AssetClass), Currency: a.Currency,
		IsLiquid: a.IsLiquid, CountsTowardNetWorth: a.CountsTowardNetWorth,
		PriceTicker: a.PriceTicker, ParentID: a.ParentID, SortOrder: a.SortOrder,
		IsComputed: a.HasChildren,
	}
	out.CostBasis = toMoneyPtr(a.CostBasis, exponents)
	if a.ArchivedAt != nil {
		at := a.ArchivedAt.Format(timeLayoutWire)
		out.ArchivedAt = &at
	}
	return out
}

// AccountInputDTO is the create/update payload.
type AccountInputDTO struct {
	Name                 string `json:"name"`
	AssetClass           string `json:"asset_class"`
	Currency             string `json:"currency"`
	IsLiquid             *bool  `json:"is_liquid"`
	CountsTowardNetWorth *bool  `json:"counts_toward_net_worth"`
	PriceTicker          string `json:"price_ticker"`
	CostBasisMinor       *int64 `json:"cost_basis_minor"`
	ParentID             *int64 `json:"parent_id"`
	SortOrder            *int   `json:"sort_order"`
}

func (d AccountInputDTO) toInput() account.Input {
	in := account.Input{
		Name: d.Name, AssetClass: account.AssetClass(d.AssetClass), Currency: d.Currency,
		IsLiquid: true, CountsTowardNetWorth: true,
		CostBasisMinor: d.CostBasisMinor, ParentID: d.ParentID, SortOrder: d.SortOrder,
	}
	if d.IsLiquid != nil {
		in.IsLiquid = *d.IsLiquid
	}
	if d.CountsTowardNetWorth != nil {
		in.CountsTowardNetWorth = *d.CountsTowardNetWorth
	}
	if d.PriceTicker != "" {
		t := d.PriceTicker
		in.PriceTicker = &t
	}
	return in
}

func (s *Server) handleListAccounts(c *fiber.Ctx) error {
	u := currentUser(c)
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	accounts, err := s.deps.Accounts.List(c.UserContext(), u.ID, c.QueryBool("include_archived", false))
	if err != nil {
		return err
	}
	out := make([]AccountDTO, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, toAccountDTO(a, exponents))
	}
	return c.JSON(out)
}

func (s *Server) handleCreateAccount(c *fiber.Ctx) error {
	var req AccountInputDTO
	if err := bind(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	created, err := s.deps.Accounts.Create(c.UserContext(), u.ID, req.toInput())
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(toAccountDTO(created, exponents))
}

func (s *Server) handleUpdateAccount(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	var req AccountInputDTO
	if err := bind(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	updated, err := s.deps.Accounts.Update(c.UserContext(), u.ID, id, req.toInput())
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	return c.JSON(toAccountDTO(updated, exponents))
}

func (s *Server) handleArchiveAccount(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	u := currentUser(c)
	if err := s.deps.Accounts.Archive(c.UserContext(), u.ID, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) handleListAccountAliases(c *fiber.Ctx) error {
	u := currentUser(c)
	aliases, err := s.deps.Accounts.ListAliases(c.UserContext(), u.ID)
	if err != nil {
		return err
	}
	out := make([]AliasDTO, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, AliasDTO{
			ID: a.ID, Source: a.Source, SourceName: a.SourceName,
			TargetID: a.AccountID, TargetName: a.TargetName,
		})
	}
	return c.JSON(out)
}

func (s *Server) handleCreateAccountAlias(c *fiber.Ctx) error {
	var req struct {
		AccountID  int64  `json:"account_id"`
		SourceName string `json:"source_name"`
		Source     string `json:"source"`
	}
	if err := bind(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	created, err := s.deps.Accounts.CreateAlias(c.UserContext(), u.ID, req.AccountID, req.Source, req.SourceName)
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(AliasDTO{
		ID: created.ID, Source: created.Source, SourceName: created.SourceName, TargetID: created.AccountID,
	})
}

func (s *Server) handleDeleteAccountAlias(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	u := currentUser(c)
	if err := s.deps.Accounts.DeleteAlias(c.UserContext(), u.ID, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) handleListCurrencies(c *fiber.Ctx) error {
	if s.deps.Currencies == nil {
		return c.JSON([]any{})
	}
	list, err := s.deps.Currencies.List(c.UserContext())
	if err != nil {
		return err
	}
	return c.JSON(list)
}
