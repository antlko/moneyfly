// Package transaction records expenses, income and transfers.
//
// Identity is structural: the natural key plus an occurrence counter, enforced by
// a UNIQUE index, so two identical coffees on one day both survive while a
// re-import of the same file adds nothing (docs/adr/0008-natural-key-dedup.md).
package transaction

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// DateLayout is the storage format for occurred_on.
const DateLayout = "2006-01-02"

// Kind is the transaction type. The sign lives here, not in the amount.
type Kind string

// The four kinds.
const (
	KindExpense     Kind = "expense"
	KindIncome      Kind = "income"
	KindTransferIn  Kind = "transfer_in"
	KindTransferOut Kind = "transfer_out"
)

// Valid reports whether the kind is one of the four allowed values.
func (k Kind) Valid() bool {
	switch k {
	case KindExpense, KindIncome, KindTransferIn, KindTransferOut:
		return true
	}
	return false
}

// IsTransfer reports whether the kind is one half of a transfer.
func (k Kind) IsTransfer() bool { return k == KindTransferIn || k == KindTransferOut }

// Transaction is one row of transaction_entry.
type Transaction struct {
	ID         int64
	UserID     int64
	AccountID  int64
	CategoryID *int64 // nil iff kind is a transfer
	OccurredOn time.Time
	Kind       Kind
	Amount     money.Money  // native, always non-negative
	BaseAmount *money.Money // nil when no rate was available
	FxRateID   *int64
	// FxAsOf is the date of the rate used, so a figure can be traced to it.
	FxAsOf          *time.Time
	Description     *string
	Merchant        *string
	NaturalKey      string
	Occurrence      int
	TransferGroupID *int64
	ImportBatchID   *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// Denormalised for list rendering; not stored on the row.
	AccountName  string
	CategoryName string
}

// Input is the create/update payload.
type Input struct {
	AccountID   int64
	CategoryID  *int64
	OccurredOn  time.Time
	Kind        Kind
	Amount      money.Money
	Description *string
	Merchant    *string
}

// TransferInput creates a mirrored pair.
type TransferInput struct {
	FromAccountID int64
	ToAccountID   int64
	OccurredOn    time.Time
	Amount        money.Money
	Description   *string
}

// Filter selects transactions for the history screen.
type Filter struct {
	From       *time.Time
	To         *time.Time
	CategoryID *int64
	AccountID  *int64
	Kind       Kind
	Query      string
	Cursor     Cursor
	Limit      int
}

// Cursor paginates on (occurred_on, id) — offset pagination drifts when rows are
// inserted mid-scroll, and an import inserts thousands at once.
type Cursor struct {
	OccurredOn time.Time
	ID         int64
	Set        bool
}

// Page is one page of history.
type Page struct {
	Items      []Transaction
	NextCursor *Cursor
	HasMore    bool
}

// Repo is the storage contract.
type Repo interface {
	// Create inserts one row, assigning the occurrence inside the same
	// transaction as the insert so a concurrent writer cannot duplicate it.
	Create(ctx context.Context, t Transaction, now time.Time) (Transaction, error)
	CreateTransferPair(ctx context.Context, out, in Transaction, now time.Time) ([]Transaction, error)
	Get(ctx context.Context, userID, id int64) (Transaction, error)
	List(ctx context.Context, userID int64, f Filter) (Page, error)
	Update(ctx context.Context, t Transaction, now time.Time) (Transaction, error)
	// SoftDelete removes the row from reports while keeping it in the database.
	// Deleting one half of a transfer deletes both.
	SoftDelete(ctx context.Context, userID, id int64, now time.Time) error
}

// FXConverter is the slice of the fx service this package needs.
type FXConverter interface {
	Convert(ctx context.Context, m money.Money, to string, on time.Time) (money.Money, *int64, error)
}

// Service is the transaction use-case layer.
type Service struct {
	repo       Repo
	accounts   *account.Service
	categories *category.Service
	currencies *currency.Service
	fx         FXConverter
	clock      clock.Clock
}

// NewService builds the service.
func NewService(
	repo Repo,
	accounts *account.Service,
	categories *category.Service,
	currencies *currency.Service,
	fxConv FXConverter,
	clk clock.Clock,
) *Service {
	return &Service{repo: repo, accounts: accounts, categories: categories,
		currencies: currencies, fx: fxConv, clock: clk}
}

// Create records a transaction.
//
// A missing exchange rate does not block the write: base_amount is stored NULL
// and the response says so.
func (s *Service) Create(ctx context.Context, userID int64, baseCurrency string, in Input) (Transaction, error) {
	acct, cat, err := s.resolve(ctx, userID, in)
	if err != nil {
		return Transaction{}, err
	}

	t := Transaction{
		UserID:      userID,
		AccountID:   acct.ID,
		OccurredOn:  in.OccurredOn.UTC().Truncate(24 * time.Hour),
		Kind:        in.Kind,
		Amount:      in.Amount,
		Description: trimPtr(in.Description),
		Merchant:    trimPtr(in.Merchant),
	}
	categoryName := ""
	if cat != nil {
		id := cat.ID
		t.CategoryID = &id
		categoryName = cat.Name
	}
	t.NaturalKey = NaturalKey(t.OccurredOn, acct.Name, categoryName,
		t.Amount.Minor, t.Amount.Currency, derefOr(in.Description, ""))

	if err := s.applyConversion(ctx, &t, baseCurrency); err != nil {
		return Transaction{}, err
	}
	return s.repo.Create(ctx, t, s.clock.Now())
}

// CreateTransfer creates the mirrored pair in one SQL transaction, sharing a
// transfer_group_id. A transfer never carries a category and never appears in
// category totals.
func (s *Service) CreateTransfer(ctx context.Context, userID int64, baseCurrency string, in TransferInput) ([]Transaction, error) {
	v := &apperr.ValidationError{}
	if in.FromAccountID == in.ToAccountID {
		v.Add("to_account_id", "must differ from from_account_id")
	}
	if in.Amount.Minor <= 0 {
		v.Add("amount.amount_minor", "must be greater than zero")
	}
	if in.OccurredOn.IsZero() {
		v.Add("occurred_on", "must be a date in YYYY-MM-DD form")
	}
	if err := v.OrNil(); err != nil {
		return nil, err
	}

	from, err := s.accounts.AssertPostable(ctx, userID, in.FromAccountID)
	if err != nil {
		return nil, err
	}
	to, err := s.accounts.AssertPostable(ctx, userID, in.ToAccountID)
	if err != nil {
		return nil, err
	}
	if from.Currency != to.Currency {
		// A cross-currency transfer needs a rate decision (which side is
		// authoritative, at what rate) that the specification does not make.
		// Refusing is better than inventing one.
		return nil, apperr.Validation("to_account_id",
			"a transfer must be between accounts of the same currency (%s vs %s); record two transactions instead",
			from.Currency, to.Currency)
	}
	if !strings.EqualFold(in.Amount.Currency, from.Currency) {
		return nil, apperr.Validation("amount.currency",
			"must be the accounts' currency %s, got %s", from.Currency, in.Amount.Currency)
	}

	day := in.OccurredOn.UTC().Truncate(24 * time.Hour)
	desc := derefOr(in.Description, "")
	out := Transaction{
		UserID: userID, AccountID: from.ID, OccurredOn: day, Kind: KindTransferOut,
		Amount: in.Amount, Description: trimPtr(in.Description),
		NaturalKey: NaturalKey(day, from.Name, "", in.Amount.Minor, in.Amount.Currency, desc),
	}
	inbound := Transaction{
		UserID: userID, AccountID: to.ID, OccurredOn: day, Kind: KindTransferIn,
		Amount: in.Amount, Description: trimPtr(in.Description),
		NaturalKey: NaturalKey(day, to.Name, "", in.Amount.Minor, in.Amount.Currency, desc),
	}
	if err := s.applyConversion(ctx, &out, baseCurrency); err != nil {
		return nil, err
	}
	if err := s.applyConversion(ctx, &inbound, baseCurrency); err != nil {
		return nil, err
	}
	return s.repo.CreateTransferPair(ctx, out, inbound, s.clock.Now())
}

// Get returns one transaction.
func (s *Service) Get(ctx context.Context, userID, id int64) (Transaction, error) {
	return s.repo.Get(ctx, userID, id)
}

// List returns a page of history.
func (s *Service) List(ctx context.Context, userID int64, f Filter) (Page, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.Kind != "" && !f.Kind.Valid() {
		return Page{}, apperr.Validation("kind", "must be one of expense|income|transfer_in|transfer_out")
	}
	return s.repo.List(ctx, userID, f)
}

// Update changes a transaction. The natural key is recomputed, because identity
// follows content — an edited row is a different row for dedup purposes, and the
// importer must not treat the original as vanished.
func (s *Service) Update(ctx context.Context, userID, id int64, baseCurrency string, in Input) (Transaction, error) {
	existing, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Transaction{}, err
	}
	if existing.Kind.IsTransfer() {
		return Transaction{}, apperr.Validation("kind",
			"a transfer is edited by deleting and re-creating the pair")
	}
	if in.Kind == "" {
		in.Kind = existing.Kind
	}
	if in.AccountID == 0 {
		in.AccountID = existing.AccountID
	}
	if in.OccurredOn.IsZero() {
		in.OccurredOn = existing.OccurredOn
	}
	if in.Amount.Currency == "" {
		in.Amount = existing.Amount
	}
	if in.CategoryID == nil && !in.Kind.IsTransfer() {
		in.CategoryID = existing.CategoryID
	}

	acct, cat, err := s.resolve(ctx, userID, in)
	if err != nil {
		return Transaction{}, err
	}
	existing.AccountID = acct.ID
	existing.OccurredOn = in.OccurredOn.UTC().Truncate(24 * time.Hour)
	existing.Kind = in.Kind
	existing.Amount = in.Amount
	existing.Description = trimPtr(in.Description)
	existing.Merchant = trimPtr(in.Merchant)
	categoryName := ""
	existing.CategoryID = nil
	if cat != nil {
		cid := cat.ID
		existing.CategoryID = &cid
		categoryName = cat.Name
	}
	existing.NaturalKey = NaturalKey(existing.OccurredOn, acct.Name, categoryName,
		existing.Amount.Minor, existing.Amount.Currency, derefOr(in.Description, ""))
	if err := s.applyConversion(ctx, &existing, baseCurrency); err != nil {
		return Transaction{}, err
	}
	return s.repo.Update(ctx, existing, s.clock.Now())
}

// Delete soft-deletes a transaction, and both halves when it is a transfer.
func (s *Service) Delete(ctx context.Context, userID, id int64) error {
	if _, err := s.repo.Get(ctx, userID, id); err != nil {
		return err
	}
	return s.repo.SoftDelete(ctx, userID, id, s.clock.Now())
}

// PeriodOf returns the period a transaction falls in.
func PeriodOf(t Transaction) period.Period { return period.FromTime(t.OccurredOn) }

func (s *Service) resolve(ctx context.Context, userID int64, in Input) (account.Account, *category.Category, error) {
	v := &apperr.ValidationError{}
	if !in.Kind.Valid() {
		v.Add("kind", "must be one of expense|income|transfer_in|transfer_out, got %q", in.Kind)
	}
	if in.Kind.IsTransfer() {
		v.Add("kind", "use POST /transfers to record a transfer")
	}
	if in.OccurredOn.IsZero() {
		v.Add("occurred_on", "must be a date in YYYY-MM-DD form")
	}
	if in.Amount.Minor < 0 {
		v.Add("amount.amount_minor", "must not be negative; the sign is carried by kind")
	}
	if in.Amount.Minor == 0 {
		v.Add("amount.amount_minor", "must be greater than zero")
	}
	if in.CategoryID == nil {
		v.Add("category_id", "is required for %s", in.Kind)
	}
	if err := v.OrNil(); err != nil {
		return account.Account{}, nil, err
	}

	acct, err := s.accounts.AssertPostable(ctx, userID, in.AccountID)
	if err != nil {
		return account.Account{}, nil, err
	}
	if !strings.EqualFold(in.Amount.Currency, acct.Currency) {
		return account.Account{}, nil, apperr.Validation("amount.currency",
			"must match the account currency %s, got %s", acct.Currency, in.Amount.Currency)
	}

	cat, err := s.categories.Get(ctx, userID, *in.CategoryID)
	if err != nil {
		return account.Account{}, nil, err
	}
	if cat.ArchivedAt != nil {
		return account.Account{}, nil, apperr.Validation("category_id", "%q is archived", cat.Name)
	}
	// An expense must not be filed under an income category, and vice versa.
	switch in.Kind {
	case KindExpense:
		if cat.Kind != category.KindExpense {
			return account.Account{}, nil, apperr.Validation("category_id",
				"%q is an income category and cannot hold an expense", cat.Name)
		}
	case KindIncome:
		if cat.Kind != category.KindIncome {
			return account.Account{}, nil, apperr.Validation("category_id",
				"%q is an expense category and cannot hold income", cat.Name)
		}
	}
	return acct, &cat, nil
}

// applyConversion computes base_amount and records which rate produced it.
func (s *Service) applyConversion(ctx context.Context, t *Transaction, baseCurrency string) error {
	if baseCurrency == "" {
		return fmt.Errorf("transaction: base currency not set for user %d", t.UserID)
	}
	converted, rateID, err := s.fx.Convert(ctx, t.Amount, baseCurrency, t.OccurredOn)
	if err != nil {
		return err
	}
	if rateID == nil && !strings.EqualFold(t.Amount.Currency, baseCurrency) {
		// No rate for this date, or none at all. The write proceeds with a NULL
		// base amount; the response tells the client the figure is unconverted.
		t.BaseAmount = nil
		t.FxRateID = nil
		return nil
	}
	t.BaseAmount = &converted
	t.FxRateID = rateID
	return nil
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
