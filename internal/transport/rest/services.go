package rest

import (
	"context"
	"time"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/setting"
	"github.com/antlko/moneyapp/internal/domain/telegram"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// The interfaces below are what transport is allowed to call. They exist so that
// handlers depend on behaviour rather than on concrete services, and so this
// package never reaches into internal/store (conventions §1).

// AuthService is the auth surface used by handlers.
type AuthService interface {
	Login(ctx context.Context, email, password string) (string, error)
	Authenticate(ctx context.Context, token string) (*auth.User, error)
	Logout(ctx context.Context, token string) error
	ChangePassword(ctx context.Context, userID int64, old, new string) error
	ListSessions(ctx context.Context, userID int64) ([]auth.SessionInfo, error)
	RevokeSession(ctx context.Context, userID int64, id string) error
}

// CategoryService is the category surface used by handlers.
type CategoryService interface {
	List(ctx context.Context, userID int64, kind category.Kind, includeArchived bool) ([]category.Category, error)
	Get(ctx context.Context, userID, id int64) (category.Category, error)
	Create(ctx context.Context, userID int64, in category.Input) (category.Category, error)
	Update(ctx context.Context, userID, id int64, in category.Input) (category.Category, error)
	Archive(ctx context.Context, userID, id int64) error
	Merge(ctx context.Context, userID, id, intoID int64) error
	ListAliases(ctx context.Context, userID int64) ([]category.Alias, error)
	CreateAlias(ctx context.Context, userID, categoryID int64, source, sourceName string) (category.Alias, error)
	DeleteAlias(ctx context.Context, userID, id int64) error
}

// AccountService is the account surface used by handlers.
type AccountService interface {
	List(ctx context.Context, userID int64, includeArchived bool) ([]account.Account, error)
	Get(ctx context.Context, userID, id int64) (account.Account, error)
	Create(ctx context.Context, userID int64, in account.Input) (account.Account, error)
	Update(ctx context.Context, userID, id int64, in account.Input) (account.Account, error)
	Archive(ctx context.Context, userID, id int64) error
	ListAliases(ctx context.Context, userID int64) ([]account.Alias, error)
	CreateAlias(ctx context.Context, userID, accountID int64, source, sourceName string) (account.Alias, error)
	DeleteAlias(ctx context.Context, userID, id int64) error
}

// TransactionService is the transaction surface used by handlers.
type TransactionService interface {
	Create(ctx context.Context, userID int64, baseCurrency string, in transaction.Input) (transaction.Transaction, error)
	CreateTransfer(ctx context.Context, userID int64, baseCurrency string, in transaction.TransferInput) ([]transaction.Transaction, error)
	Get(ctx context.Context, userID, id int64) (transaction.Transaction, error)
	List(ctx context.Context, userID int64, f transaction.Filter) (transaction.Page, error)
	Update(ctx context.Context, userID, id int64, baseCurrency string, in transaction.Input) (transaction.Transaction, error)
	Delete(ctx context.Context, userID, id int64) error
}

// BudgetService is the budget surface used by handlers.
type BudgetService interface {
	ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]budget.Budget, error)
	Upsert(ctx context.Context, userID, categoryID int64, p period.Period, planned money.Money) (budget.Budget, error)
	Delete(ctx context.Context, userID, categoryID int64, p period.Period) error
	Bulk(ctx context.Context, userID int64, from, to period.Period, items []budget.BulkItem) (budget.BulkResult, error)
	Report(ctx context.Context, userID int64, p period.Period, baseCurrency string,
		actuals budget.ActualsSource, th budget.Thresholds) (budget.Report, error)
}

// FXService is the FX surface used by handlers.
type FXService interface {
	RateOn(ctx context.Context, base, quote string, on time.Time) (*fx.Rate, error)
	History(ctx context.Context, base, quote string, from, to time.Time) ([]fx.Rate, error)
	Latest(ctx context.Context) ([]fx.Rate, error)
}

// ImportService is the import surface used by handlers. It is importer.Service
// verbatim: the bot in stage 08 plugs into the same seam.
type ImportService = importer.Service

// MetricsService is the reporting surface used by handlers: one batched load per
// request, then pure functions over the result.
type MetricsService interface {
	Load(ctx context.Context, userID int64, from, to period.Period, baseCurrency string) (metrics.Data, metrics.Summary, error)
	RecordedRange(ctx context.Context, userID int64) (from, to period.Period, ok bool, err error)
	AverageWindow(ctx context.Context, userID int64, anchor period.Period) (from, to period.Period, ok bool, err error)
}

// CapitalService is the capital surface used by handlers.
type CapitalService interface {
	ListByPeriod(ctx context.Context, userID int64, p period.Period) ([]capital.Snapshot, error)
	Upsert(ctx context.Context, userID, accountID int64, p period.Period, baseCurrency string, in capital.Input) (capital.Snapshot, error)
	Bulk(ctx context.Context, userID int64, p period.Period, baseCurrency string, items map[int64]capital.Input) (int, error)
	Delete(ctx context.Context, userID, accountID int64, p period.Period) error
	Load(ctx context.Context, userID int64, from, to period.Period, baseCurrency string, md metrics.Data, valuation capital.Valuation) (capital.Data, error)
	Reconciliation(ctx context.Context, userID int64, p period.Period, baseCurrency string) ([]capital.Drift, error)
	GeneralInCurrency(ctx context.Context, d capital.Data, code string, p period.Period) (*money.Money, error)
}

// SettingService is the settings surface used by handlers.
type SettingService interface {
	List(ctx context.Context, userID int64) ([]setting.Setting, error)
	Get(ctx context.Context, userID int64, key string) (setting.Setting, error)
	Update(ctx context.Context, userID int64, key string, in setting.Input) (setting.Setting, error)
	Refresh(ctx context.Context, userID int64, key string) (*setting.Setting, error)
	ListProviders(ctx context.Context) ([]setting.Provider, error)
	Float(ctx context.Context, userID int64, key string, fallback float64) float64
	String(ctx context.Context, userID int64, key, fallback string) string
}

// TelegramService is the chat-link surface used by handlers. The bot itself is
// a separate worker; this is only the linking half.
type TelegramService interface {
	Mint(ctx context.Context, userID int64) (telegram.Link, error)
	List(ctx context.Context, userID int64) ([]telegram.Link, error)
	Revoke(ctx context.Context, userID, id int64) error
}

// CurrencyService supplies the exponents every money field is rendered with.
type CurrencyService interface {
	List(ctx context.Context) ([]currency.Currency, error)
	Exponents(ctx context.Context) (map[string]int, error)
}

// IdempotencyStore replays POST outcomes for a repeated Idempotency-Key. The
// signatures are primitive so this package needs no storage import.
type IdempotencyStore interface {
	Find(ctx context.Context, userID int64, key, method, path, requestHash string) (found bool, status int, body []byte, err error)
	Save(ctx context.Context, userID int64, key, method, path, requestHash string, status int, body []byte, now time.Time) error
}

// ReadinessChecker reports the database's schema version against the one this
// binary expects, for /readyz. It keeps the schema query in the store layer.
type ReadinessChecker interface {
	Ready(ctx context.Context) (current, expected int64, err error)
}
