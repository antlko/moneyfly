package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/setting"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// Provisioner installs the canonical taxonomy for a newly created user.
type Provisioner struct {
	categories category.Repo
	accounts   account.Repo
	settings   setting.Repo
	clock      clock.Clock
}

// NewProvisioner builds the provisioner.
//
// settings may be nil, which installs the taxonomy without the settings block.
// Nothing depends on that outside tests written before stage 07.
func NewProvisioner(categories category.Repo, accounts account.Repo, settings setting.Repo, clk clock.Clock) *Provisioner {
	return &Provisioner{categories: categories, accounts: accounts, settings: settings, clock: clk}
}

// Provision writes the seeded categories, accounts and aliases for userID. It is
// idempotent in the sense that it is only ever called once per user, at creation.
func (p *Provisioner) Provision(ctx context.Context, userID int64) error {
	now := p.clock.Now()
	if err := p.categoriesFor(ctx, userID, now); err != nil {
		return err
	}
	if err := p.accountsFor(ctx, userID, now); err != nil {
		return err
	}
	return p.settingsFor(ctx, userID, now)
}

// EnsureCategories installs any seeded category an account is missing.
//
// Purely additive: an existing category is never renamed, re-flagged or
// archived, because the user may have changed it on purpose. It exists because a
// stage that adds a seeded category — stage 05 added the two income ones — would
// otherwise reach only accounts created afterwards, leaving the original user
// with no way to record a salary.
func (p *Provisioner) EnsureCategories(ctx context.Context, userID int64) (int, error) {
	existing, err := p.categories.List(ctx, userID, "", true)
	if err != nil {
		return 0, err
	}
	have := make(map[string]bool, len(existing))
	for _, c := range existing {
		have[c.Name] = true
	}

	now := p.clock.Now()
	installed := 0
	for i, c := range Categories() {
		if have[c.Name] {
			continue
		}
		created, err := p.categories.Create(ctx, category.Category{
			UserID: userID, Name: c.Name, Kind: c.Kind, IsEssential: c.IsEssential,
			Icon: c.Icon, Color: c.Color, SortOrder: i + 1,
		}, now)
		if err != nil {
			return installed, fmt.Errorf("seed: category %q: %w", c.Name, err)
		}
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			if _, err := p.categories.CreateAlias(ctx, category.Alias{
				UserID: userID, CategoryID: created.ID,
				Source: category.SourceMonefy, SourceName: name,
			}, now); err != nil {
				return installed, fmt.Errorf("seed: category alias %q: %w", name, err)
			}
		}
		installed++
	}
	return installed, nil
}

// EnsureSettings installs any seeded setting a user does not already have.
//
// The taxonomy is installed once, at user creation, but a stage that adds a new
// seeded setting has to reach the accounts that already exist — otherwise the
// only real user upgrades into a settings screen with nothing on it. Upsert makes
// it idempotent, and an existing row's mode and value are left alone.
func (p *Provisioner) EnsureSettings(ctx context.Context, userID int64) (int, error) {
	if p.settings == nil {
		return 0, nil
	}
	existing, err := p.settings.List(ctx, userID)
	if err != nil {
		return 0, err
	}
	have := make(map[string]bool, len(existing))
	for _, s := range existing {
		have[s.Key] = true
	}

	now := p.clock.Now()
	installed := 0
	for _, s := range Settings() {
		if have[s.Key] {
			continue
		}
		row := setting.Setting{UserID: userID, Key: s.Key, Mode: s.Mode, RefreshInterval: s.Refresh}
		if s.ManualValue != "" {
			value := s.ManualValue
			row.ManualValue = &value
		}
		if s.ProviderKey != "" {
			key := s.ProviderKey
			row.ProviderKey = &key
		}
		if _, err := p.settings.Upsert(ctx, row, now); err != nil {
			return installed, fmt.Errorf("seed: setting %q: %w", s.Key, err)
		}
		installed++
	}
	return installed, nil
}

// settingsFor installs the thresholds, the report defaults and the three rate
// settings. Without them a new account has no amber band and no rates to fetch.
func (p *Provisioner) settingsFor(ctx context.Context, userID int64, now time.Time) error {
	if p.settings == nil {
		return nil
	}
	for _, s := range Settings() {
		row := setting.Setting{
			UserID: userID, Key: s.Key, Mode: s.Mode, RefreshInterval: s.Refresh,
		}
		if s.ManualValue != "" {
			value := s.ManualValue
			row.ManualValue = &value
		}
		if s.ProviderKey != "" {
			key := s.ProviderKey
			row.ProviderKey = &key
		}
		if _, err := p.settings.Upsert(ctx, row, now); err != nil {
			return fmt.Errorf("seed: setting %q: %w", s.Key, err)
		}
	}
	return nil
}

func (p *Provisioner) categoriesFor(ctx context.Context, userID int64, now time.Time) error {
	for i, c := range Categories() {
		created, err := p.categories.Create(ctx, category.Category{
			UserID:      userID,
			Name:        c.Name,
			Kind:        c.Kind,
			IsEssential: c.IsEssential,
			Icon:        c.Icon,
			Color:       c.Color,
			SortOrder:   i + 1,
		}, now)
		if err != nil {
			return fmt.Errorf("seed: category %q: %w", c.Name, err)
		}
		// The canonical name is itself an alias, so an exact match in an export
		// resolves immediately instead of being offered as a suggestion.
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			if _, err := p.categories.CreateAlias(ctx, category.Alias{
				UserID:     userID,
				CategoryID: created.ID,
				Source:     category.SourceMonefy,
				SourceName: name,
			}, now); err != nil {
				return fmt.Errorf("seed: category alias %q: %w", name, err)
			}
		}
	}
	return nil
}

func (p *Provisioner) accountsFor(ctx context.Context, userID int64, now time.Time) error {
	byName := map[string]int64{}
	seeds := Accounts()
	// Parents first, so a child's parent_id always resolves.
	for pass := 0; pass < 2; pass++ {
		for i, a := range seeds {
			isChild := a.Parent != ""
			if (pass == 0) == isChild {
				continue
			}
			var parentID *int64
			if isChild {
				id, ok := byName[a.Parent]
				if !ok {
					return fmt.Errorf("seed: account %q names unknown parent %q", a.Name, a.Parent)
				}
				parentID = &id
			}
			var ticker *string
			if a.Ticker != "" {
				t := a.Ticker
				ticker = &t
			}
			created, err := p.accounts.Create(ctx, account.Account{
				UserID:               userID,
				Name:                 a.Name,
				AssetClass:           a.Class,
				Currency:             a.Currency,
				IsLiquid:             a.IsLiquid,
				CountsTowardNetWorth: a.Counts,
				PriceTicker:          ticker,
				ParentID:             parentID,
				SortOrder:            i + 1,
			}, now)
			if err != nil {
				return fmt.Errorf("seed: account %q: %w", a.Name, err)
			}
			byName[a.Name] = created.ID

			for _, name := range append([]string{a.Name}, a.Aliases...) {
				if _, err := p.accounts.CreateAlias(ctx, account.Alias{
					UserID:     userID,
					AccountID:  created.ID,
					Source:     account.SourceMonefy,
					SourceName: name,
				}, now); err != nil {
					return fmt.Errorf("seed: account alias %q: %w", name, err)
				}
			}
		}
	}
	return nil
}

// WorkbookPlan returns the workbook's annual plan per category name, in EUR minor
// units, for the budget bulk-seed screen. Categories with no workbook row are
// absent rather than zero — they were never planned, which is not the same as a
// plan of nothing.
func WorkbookPlan() map[string]money.Money {
	out := map[string]money.Money{}
	for _, c := range Categories() {
		if c.PlannedMinor == 0 {
			continue
		}
		out[c.Name] = money.New(c.PlannedMinor, "EUR")
	}
	return out
}
