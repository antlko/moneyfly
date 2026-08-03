// Package account owns the chart of accounts: what money is held in, its asset
// class, whether it is liquid, and whether it counts toward net worth.
//
// Parent accounts are computed, never stored: the workbook enumerated children by
// hand in each parent's formula, which is how `Banks FOP` ended up excluded from
// `Banks` yet included in `General` (docs/adr/0003-snapshot-reconcile-balances.md).
package account

import (
	"context"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// AssetClass groups accounts for allocation reporting.
type AssetClass string

// The asset classes, matching the CHECK constraint in docs/03-data-model.md §3.5.
const (
	ClassCash       AssetClass = "cash"
	ClassBank       AssetClass = "bank"
	ClassDeposit    AssetClass = "deposit"
	ClassInvestment AssetClass = "investment"
	ClassMetal      AssetClass = "metal"
	ClassCrypto     AssetClass = "crypto"
	ClassOther      AssetClass = "other"
)

// Valid reports whether the class is one of the seven allowed values.
func (a AssetClass) Valid() bool {
	switch a {
	case ClassCash, ClassBank, ClassDeposit, ClassInvestment, ClassMetal, ClassCrypto, ClassOther:
		return true
	}
	return false
}

// SourceMonefy is the default alias source.
const SourceMonefy = "monefy"

// Account is one row of the account table.
type Account struct {
	ID                   int64
	UserID               int64
	Name                 string
	AssetClass           AssetClass
	Currency             string
	IsLiquid             bool         // Ready for usage, stage 06
	CountsTowardNetWorth bool         // General, stage 06
	PriceTicker          *string      // XAU, USDT — stage 07
	CostBasis            *money.Money // the workbook's "(Invested)" rows
	ParentID             *int64
	SortOrder            int
	ArchivedAt           *time.Time
	// HasChildren marks a computed parent. It is derived, not stored.
	HasChildren bool
}

// Alias maps a source account name onto an account.
type Alias struct {
	ID         int64
	UserID     int64
	AccountID  int64
	Source     string
	SourceName string
	TargetName string
}

// Input is the create/update payload.
type Input struct {
	Name                 string
	AssetClass           AssetClass
	Currency             string
	IsLiquid             bool
	CountsTowardNetWorth bool
	PriceTicker          *string
	CostBasisMinor       *int64
	ParentID             *int64
	SortOrder            *int
}

// Resolver resolves a source account name, for the importer.
type Resolver interface {
	// ResolveAlias returns (nil, nil) when unknown, exactly like the category
	// resolver: the import must block and ask.
	ResolveAlias(ctx context.Context, userID int64, source, sourceName string) (*Account, error)
}

// Repo is the storage contract.
type Repo interface {
	List(ctx context.Context, userID int64, includeArchived bool) ([]Account, error)
	Get(ctx context.Context, userID, id int64) (Account, error)
	Create(ctx context.Context, a Account, now time.Time) (Account, error)
	Update(ctx context.Context, a Account, now time.Time) (Account, error)
	Archive(ctx context.Context, userID, id int64, now time.Time) error
	NextSortOrder(ctx context.Context, userID int64) (int, error)
	// HasValues reports whether anything is posted to or recorded against the
	// account, which is what makes a parent assignment illegal.
	HasValues(ctx context.Context, userID, id int64) (bool, error)

	ListAliases(ctx context.Context, userID int64) ([]Alias, error)
	CreateAlias(ctx context.Context, a Alias, now time.Time) (Alias, error)
	DeleteAlias(ctx context.Context, userID, id int64) error
	FindAlias(ctx context.Context, userID int64, source, sourceName string) (*Account, error)
}

// Service is the account use-case layer.
type Service struct {
	repo       Repo
	currencies *currency.Service
	clock      clock.Clock
}

// NewService builds the service.
func NewService(repo Repo, currencies *currency.Service, clk clock.Clock) *Service {
	return &Service{repo: repo, currencies: currencies, clock: clk}
}

// List returns the user's accounts.
func (s *Service) List(ctx context.Context, userID int64, includeArchived bool) ([]Account, error) {
	return s.repo.List(ctx, userID, includeArchived)
}

// Get returns one account.
func (s *Service) Get(ctx context.Context, userID, id int64) (Account, error) {
	return s.repo.Get(ctx, userID, id)
}

// Create adds an account.
func (s *Service) Create(ctx context.Context, userID int64, in Input) (Account, error) {
	if err := s.validate(ctx, in, true); err != nil {
		return Account{}, err
	}
	if err := s.checkParent(ctx, userID, 0, in.ParentID); err != nil {
		return Account{}, err
	}
	sortOrder := 0
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	} else {
		next, err := s.repo.NextSortOrder(ctx, userID)
		if err != nil {
			return Account{}, err
		}
		sortOrder = next
	}
	a := Account{
		UserID:               userID,
		Name:                 strings.TrimSpace(in.Name),
		AssetClass:           in.AssetClass,
		Currency:             strings.ToUpper(strings.TrimSpace(in.Currency)),
		IsLiquid:             in.IsLiquid,
		CountsTowardNetWorth: in.CountsTowardNetWorth,
		PriceTicker:          in.PriceTicker,
		ParentID:             in.ParentID,
		SortOrder:            sortOrder,
	}
	if in.CostBasisMinor != nil {
		cb := money.New(*in.CostBasisMinor, a.Currency)
		a.CostBasis = &cb
	}
	return s.repo.Create(ctx, a, s.clock.Now())
}

// Update changes an account.
func (s *Service) Update(ctx context.Context, userID, id int64, in Input) (Account, error) {
	existing, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Account{}, err
	}
	if err := s.validate(ctx, in, false); err != nil {
		return Account{}, err
	}
	if err := s.checkParent(ctx, userID, id, in.ParentID); err != nil {
		return Account{}, err
	}
	if in.Name != "" {
		existing.Name = strings.TrimSpace(in.Name)
	}
	if in.AssetClass != "" {
		existing.AssetClass = in.AssetClass
	}
	if in.Currency != "" {
		existing.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	}
	existing.IsLiquid = in.IsLiquid
	existing.CountsTowardNetWorth = in.CountsTowardNetWorth
	existing.PriceTicker = in.PriceTicker
	existing.ParentID = in.ParentID
	if in.SortOrder != nil {
		existing.SortOrder = *in.SortOrder
	}
	if in.CostBasisMinor != nil {
		cb := money.New(*in.CostBasisMinor, existing.Currency)
		existing.CostBasis = &cb
	} else {
		existing.CostBasis = nil
	}
	return s.repo.Update(ctx, existing, s.clock.Now())
}

// Archive hides an account while keeping its history.
func (s *Service) Archive(ctx context.Context, userID, id int64) error {
	if _, err := s.repo.Get(ctx, userID, id); err != nil {
		return err
	}
	return s.repo.Archive(ctx, userID, id, s.clock.Now())
}

// ListAliases returns the account mapping table.
func (s *Service) ListAliases(ctx context.Context, userID int64) ([]Alias, error) {
	return s.repo.ListAliases(ctx, userID)
}

// CreateAlias records a mapping. Monefy names accounts after currencies, so the
// same logical account appears under different names across exports.
func (s *Service) CreateAlias(ctx context.Context, userID, accountID int64, source, sourceName string) (Alias, error) {
	v := &apperr.ValidationError{}
	if sourceName == "" {
		v.Add("source_name", "must not be empty")
	}
	if accountID <= 0 {
		v.Add("account_id", "must reference an account")
	}
	if err := v.OrNil(); err != nil {
		return Alias{}, err
	}
	if _, err := s.repo.Get(ctx, userID, accountID); err != nil {
		return Alias{}, err
	}
	if source == "" {
		source = SourceMonefy
	}
	return s.repo.CreateAlias(ctx, Alias{
		UserID: userID, AccountID: accountID, Source: source, SourceName: sourceName,
	}, s.clock.Now())
}

// DeleteAlias removes a mapping.
func (s *Service) DeleteAlias(ctx context.Context, userID, id int64) error {
	return s.repo.DeleteAlias(ctx, userID, id)
}

// ResolveAlias implements Resolver: (nil, nil) when unknown.
func (s *Service) ResolveAlias(ctx context.Context, userID int64, source, sourceName string) (*Account, error) {
	if source == "" {
		source = SourceMonefy
	}
	if sourceName == "" {
		return nil, nil
	}
	return s.repo.FindAlias(ctx, userID, source, sourceName)
}

// AssertPostable rejects posting a value to a computed parent.
func (s *Service) AssertPostable(ctx context.Context, userID, id int64) (Account, error) {
	a, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Account{}, err
	}
	if a.HasChildren {
		return Account{}, apperr.Validation("account_id",
			"%q has child accounts, so its value is the sum of its children and cannot be written directly", a.Name)
	}
	if a.ArchivedAt != nil {
		return Account{}, apperr.Validation("account_id", "%q is archived", a.Name)
	}
	return a, nil
}

func (s *Service) validate(ctx context.Context, in Input, requireAll bool) error {
	v := &apperr.ValidationError{}
	if requireAll || in.Name != "" {
		if strings.TrimSpace(in.Name) == "" {
			v.Add("name", "must not be empty")
		}
	}
	if requireAll || in.AssetClass != "" {
		if !in.AssetClass.Valid() {
			v.Add("asset_class", "must be one of cash|bank|deposit|investment|metal|crypto|other, got %q", in.AssetClass)
		}
	}
	if requireAll || in.Currency != "" {
		code := strings.ToUpper(strings.TrimSpace(in.Currency))
		if !s.currencies.Exists(ctx, code) {
			v.Add("currency", "unknown currency %q", in.Currency)
		}
	}
	if in.CostBasisMinor != nil && *in.CostBasisMinor < 0 {
		v.Add("cost_basis", "must not be negative")
	}
	return v.OrNil()
}

// checkParent rejects a missing parent, a cycle, and a parent that already holds
// a value of its own.
func (s *Service) checkParent(ctx context.Context, userID, selfID int64, parentID *int64) error {
	if parentID == nil {
		return nil
	}
	if *parentID == selfID {
		return apperr.Validation("parent_id", "an account cannot be its own parent")
	}
	seen := map[int64]bool{}
	if selfID != 0 {
		seen[selfID] = true
	}
	cur := *parentID
	for i := 0; i < 32; i++ {
		if seen[cur] {
			return apperr.Validation("parent_id", "would create a parent cycle")
		}
		seen[cur] = true
		parent, err := s.repo.Get(ctx, userID, cur)
		if err != nil {
			return err
		}
		if i == 0 {
			// The account becoming a parent must not already hold a value: a
			// parent is a sum over its children.
			hasValues, err := s.repo.HasValues(ctx, userID, parent.ID)
			if err != nil {
				return err
			}
			if hasValues {
				return apperr.Validation("parent_id",
					"%q already holds transactions or snapshots, so it cannot become a computed parent", parent.Name)
			}
		}
		if parent.ParentID == nil {
			return nil
		}
		cur = *parent.ParentID
	}
	return apperr.Validation("parent_id", "parent chain is too deep")
}
