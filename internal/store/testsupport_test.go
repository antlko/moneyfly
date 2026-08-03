package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/seed"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// fixedNow is the instant every repository test runs at, so nothing depends on the
// wall clock.
var fixedNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

// harness is a migrated database with every repository wired up, plus a helper to
// create fully provisioned users. Repository tests use real SQLite rather than a
// mock, because the DDL constraints are part of what is under test (conventions §9).
type harness struct {
	t          *testing.T
	ctx        context.Context
	clock      clock.Clock
	db         *sql.DB
	Auth       *AuthRepo
	Categories *CategoryRepo
	Accounts   *AccountRepo
	Fx         *FxRepo
	Txn        *TransactionRepo
	Budgets    *BudgetRepo
	Idem       *IdempotencyRepo
	Currencies *currency.Service
	AuthSvc    *auth.Service
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := OpenTest(t)
	clk := clock.Fixed(fixedNow)

	h := &harness{
		t: t, ctx: context.Background(), clock: clk, db: db,
		Auth:       NewAuthRepo(db),
		Categories: NewCategoryRepo(db),
		Accounts:   NewAccountRepo(db),
		Fx:         NewFxRepo(db),
		Txn:        NewTransactionRepo(db),
		Budgets:    NewBudgetRepo(db),
		Idem:       NewIdempotencyRepo(db),
	}
	h.Currencies = currency.NewService(NewCurrencyRepo(db))

	provisioner := seed.NewProvisioner(h.Categories, h.Accounts, NewSettingRepo(db), clk)
	svc, err := auth.NewService(h.Auth, clk, 10, 720*time.Hour, provisioner)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	h.AuthSvc = svc
	return h
}

// user creates a provisioned user: the 20 seeded categories, the chart of accounts
// and every alias.
func (h *harness) user(email string) auth.User {
	h.t.Helper()
	u, err := h.AuthSvc.CreateUser(h.ctx, auth.User{Email: email, Role: auth.RoleUser}, "correct-horse", false)
	if err != nil {
		h.t.Fatalf("creating user %s: %v", email, err)
	}
	return u
}

// categoryByName finds a seeded category.
func (h *harness) categoryByName(userID int64, name string) category.Category {
	h.t.Helper()
	cats, err := h.Categories.List(h.ctx, userID, "", true)
	if err != nil {
		h.t.Fatalf("listing categories: %v", err)
	}
	for _, c := range cats {
		if c.Name == name {
			return c
		}
	}
	h.t.Fatalf("no seeded category named %q", name)
	return category.Category{}
}

// accountByName finds a seeded account.
func (h *harness) accountByName(userID int64, name string) account.Account {
	h.t.Helper()
	accounts, err := h.Accounts.List(h.ctx, userID, true)
	if err != nil {
		h.t.Fatalf("listing accounts: %v", err)
	}
	for _, a := range accounts {
		if a.Name == name {
			return a
		}
	}
	h.t.Fatalf("no seeded account named %q", name)
	return account.Account{}
}

// services builds the domain services over this harness's repositories.
func (h *harness) categoryService() *category.Service {
	return category.NewService(h.Categories, h.clock)
}

func (h *harness) accountService() *account.Service {
	return account.NewService(h.Accounts, h.Currencies, h.clock)
}

func (h *harness) fxService() *fx.Service {
	return fx.NewService(h.Fx, h.Currencies)
}

func (h *harness) transactionService() *transaction.Service {
	return transaction.NewService(h.Txn, h.accountService(), h.categoryService(),
		h.Currencies, h.fxService(), h.clock)
}

func (h *harness) budgetService() *budget.Service {
	return budget.NewService(h.Budgets, h.categoryService(), h.Currencies, h.clock)
}

// newExpense builds a minimal expense input.
func newExpense(accountID, categoryID int64, on string, minor int64, currency string) transaction.Input {
	return transaction.Input{
		AccountID: accountID, CategoryID: &categoryID, OccurredOn: mustDate(on),
		Kind: transaction.KindExpense, Amount: money.New(minor, currency),
	}
}

func mustDate(s string) time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic("test: bad date " + s)
	}
	return t.UTC()
}
