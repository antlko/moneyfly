// Package category owns the chart of expense and income categories and the alias
// table that maps source names onto them.
//
// The alias table is the fix for the 14% silent loss described in
// docs/04-import-monefy.md §4.2: an unrecognised source name resolves to nothing
// and blocks the batch, rather than being auto-created or coerced to zero.
package category

import (
	"context"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

// Kind separates expense from income categories.
type Kind string

// The two kinds.
const (
	KindExpense Kind = "expense"
	KindIncome  Kind = "income"
)

// Valid reports whether the kind is one of the two allowed values.
func (k Kind) Valid() bool { return k == KindExpense || k == KindIncome }

// SourceMonefy is the default alias source.
const SourceMonefy = "monefy"

// Category is one row of the category table.
type Category struct {
	ID          int64
	UserID      int64
	Name        string
	Kind        Kind
	IsEssential bool // drives Possible Minimum, stage 05
	Icon        string
	Color       string
	SortOrder   int
	ParentID    *int64
	ArchivedAt  *time.Time
}

// Alias maps a source name (as it appears in an export) onto a category.
type Alias struct {
	ID         int64
	UserID     int64
	CategoryID int64
	Source     string
	SourceName string
	TargetName string
}

// Input is the create/update payload.
type Input struct {
	Name        string
	Kind        Kind
	IsEssential bool
	Icon        string
	Color       string
	SortOrder   *int
	ParentID    *int64
}

// Resolver is the contract stage 04 depends on.
type Resolver interface {
	// ResolveAlias returns (nil, nil) when the name is unknown. The caller must
	// block the import and ask; it must never auto-create.
	ResolveAlias(ctx context.Context, userID int64, source, sourceName string) (*Category, error)
}

// Repo is the storage contract.
type Repo interface {
	List(ctx context.Context, userID int64, kind Kind, includeArchived bool) ([]Category, error)
	Get(ctx context.Context, userID, id int64) (Category, error)
	Create(ctx context.Context, c Category, now time.Time) (Category, error)
	Update(ctx context.Context, c Category, now time.Time) (Category, error)
	Archive(ctx context.Context, userID, id int64, now time.Time) error
	// Merge rewrites every reference from id to intoID, archives id and leaves an
	// alias behind, in one transaction.
	Merge(ctx context.Context, userID, id, intoID int64, now time.Time) error
	NextSortOrder(ctx context.Context, userID int64) (int, error)

	ListAliases(ctx context.Context, userID int64) ([]Alias, error)
	CreateAlias(ctx context.Context, a Alias, now time.Time) (Alias, error)
	DeleteAlias(ctx context.Context, userID, id int64) error
	FindAlias(ctx context.Context, userID int64, source, sourceName string) (*Category, error)
}

// Service is the category use-case layer.
type Service struct {
	repo  Repo
	clock clock.Clock
}

// NewService builds the service.
func NewService(repo Repo, clk clock.Clock) *Service { return &Service{repo: repo, clock: clk} }

// List returns the user's categories. kind may be empty for all kinds.
func (s *Service) List(ctx context.Context, userID int64, kind Kind, includeArchived bool) ([]Category, error) {
	if kind != "" && !kind.Valid() {
		return nil, apperr.Validation("kind", "must be expense or income, got %q", kind)
	}
	return s.repo.List(ctx, userID, kind, includeArchived)
}

// Get returns one category, archived or not.
func (s *Service) Get(ctx context.Context, userID, id int64) (Category, error) {
	return s.repo.Get(ctx, userID, id)
}

// Create adds a category.
func (s *Service) Create(ctx context.Context, userID int64, in Input) (Category, error) {
	v := &apperr.ValidationError{}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		v.Add("name", "must not be empty")
	}
	if !in.Kind.Valid() {
		v.Add("kind", "must be expense or income, got %q", in.Kind)
	}
	if err := v.OrNil(); err != nil {
		return Category{}, err
	}
	if err := s.checkParent(ctx, userID, 0, in.ParentID); err != nil {
		return Category{}, err
	}

	sortOrder := 0
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	} else {
		next, err := s.repo.NextSortOrder(ctx, userID)
		if err != nil {
			return Category{}, err
		}
		sortOrder = next
	}
	return s.repo.Create(ctx, Category{
		UserID:      userID,
		Name:        name,
		Kind:        in.Kind,
		IsEssential: in.IsEssential,
		Icon:        in.Icon,
		Color:       in.Color,
		SortOrder:   sortOrder,
		ParentID:    in.ParentID,
	}, s.clock.Now())
}

// Update changes a category. Renaming keeps identity, so history follows the new
// name (docs/08-ux.md §8.8).
func (s *Service) Update(ctx context.Context, userID, id int64, in Input) (Category, error) {
	existing, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Category{}, err
	}
	v := &apperr.ValidationError{}
	if in.Name != "" {
		existing.Name = strings.TrimSpace(in.Name)
		if existing.Name == "" {
			v.Add("name", "must not be empty")
		}
	}
	if in.Kind != "" {
		if !in.Kind.Valid() {
			v.Add("kind", "must be expense or income, got %q", in.Kind)
		} else {
			existing.Kind = in.Kind
		}
	}
	if err := v.OrNil(); err != nil {
		return Category{}, err
	}
	if err := s.checkParent(ctx, userID, id, in.ParentID); err != nil {
		return Category{}, err
	}
	existing.IsEssential = in.IsEssential
	existing.Icon = in.Icon
	existing.Color = in.Color
	if in.SortOrder != nil {
		existing.SortOrder = *in.SortOrder
	}
	existing.ParentID = in.ParentID
	return s.repo.Update(ctx, existing, s.clock.Now())
}

// Archive hides a category from entry while keeping every historical row
// readable. Nothing is ever hard-deleted.
func (s *Service) Archive(ctx context.Context, userID, id int64) error {
	if _, err := s.repo.Get(ctx, userID, id); err != nil {
		return err
	}
	return s.repo.Archive(ctx, userID, id, s.clock.Now())
}

// Merge folds one category into another, rewriting references and leaving an
// alias behind so a later import of the old name still resolves.
func (s *Service) Merge(ctx context.Context, userID, id, intoID int64) error {
	if id == intoID {
		return apperr.Validation("into_id", "a category cannot be merged into itself")
	}
	from, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	into, err := s.repo.Get(ctx, userID, intoID)
	if err != nil {
		return err
	}
	if from.Kind != into.Kind {
		return apperr.Validation("into_id", "cannot merge a %s category into an %s one", from.Kind, into.Kind)
	}
	return s.repo.Merge(ctx, userID, id, intoID, s.clock.Now())
}

// ListAliases returns the mapping table, which the UI exposes so a decision made
// during import stays visible and editable afterwards.
func (s *Service) ListAliases(ctx context.Context, userID int64) ([]Alias, error) {
	return s.repo.ListAliases(ctx, userID)
}

// CreateAlias records a mapping. Confirming a fuzzy suggestion during an import
// lands here, after which that source name never asks again.
func (s *Service) CreateAlias(ctx context.Context, userID, categoryID int64, source, sourceName string) (Alias, error) {
	v := &apperr.ValidationError{}
	if sourceName == "" {
		v.Add("source_name", "must not be empty")
	}
	if categoryID <= 0 {
		v.Add("category_id", "must reference a category")
	}
	if err := v.OrNil(); err != nil {
		return Alias{}, err
	}
	if _, err := s.repo.Get(ctx, userID, categoryID); err != nil {
		return Alias{}, err
	}
	if source == "" {
		source = SourceMonefy
	}
	return s.repo.CreateAlias(ctx, Alias{
		UserID:     userID,
		CategoryID: categoryID,
		Source:     source,
		// Deliberately not trimmed: "Family " and "Family" are different source
		// names in the real export and must map independently.
		SourceName: sourceName,
	}, s.clock.Now())
}

// DeleteAlias removes a mapping.
func (s *Service) DeleteAlias(ctx context.Context, userID, id int64) error {
	return s.repo.DeleteAlias(ctx, userID, id)
}

// ResolveAlias implements Resolver.
//
// "Unknown" is an expected, actionable outcome — the import stops and asks — so
// it is (nil, nil) rather than an error.
func (s *Service) ResolveAlias(ctx context.Context, userID int64, source, sourceName string) (*Category, error) {
	if source == "" {
		source = SourceMonefy
	}
	if sourceName == "" {
		return nil, nil
	}
	return s.repo.FindAlias(ctx, userID, source, sourceName)
}

// checkParent rejects a missing parent and a parent cycle.
func (s *Service) checkParent(ctx context.Context, userID, selfID int64, parentID *int64) error {
	if parentID == nil {
		return nil
	}
	if *parentID == selfID {
		return apperr.Validation("parent_id", "a category cannot be its own parent")
	}
	seen := map[int64]bool{selfID: true}
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
		if parent.ParentID == nil {
			return nil
		}
		cur = *parent.ParentID
	}
	return apperr.Validation("parent_id", "parent chain is too deep")
}
