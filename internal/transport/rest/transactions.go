package rest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// HeaderIdempotencyKey lets a client retry a create safely.
const HeaderIdempotencyKey = "Idempotency-Key"

func (s *Server) registerTransactionRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged(), s.requireWritable()}

	g := api.Group("/transactions", guard...)
	g.Get("/", s.handleListTransactions)
	g.Post("/", s.handleCreateTransaction)
	g.Get("/:id", s.handleGetTransaction)
	g.Patch("/:id", s.handleUpdateTransaction)
	g.Delete("/:id", s.handleDeleteTransaction)

	api.Post("/transfers", append(guard, s.handleCreateTransfer)...)
}

// TransactionDTO is a transaction on the wire.
type TransactionDTO struct {
	ID           int64         `json:"id"`
	AccountID    int64         `json:"account_id"`
	AccountName  string        `json:"account_name"`
	CategoryID   *int64        `json:"category_id"`
	CategoryName string        `json:"category_name,omitempty"`
	OccurredOn   string        `json:"occurred_on"`
	Kind         string        `json:"kind"`
	Amount       MoneyDTO      `json:"amount"`
	BaseAmount   *BaseMoneyDTO `json:"base_amount"`
	Description  *string       `json:"description"`
	Merchant     *string       `json:"merchant"`
	Occurrence   int           `json:"occurrence"`
	// Unconverted says the base figure is missing because no rate was available
	// for this date. The write is not blocked by that, so the client is told.
	Unconverted     bool   `json:"unconverted"`
	TransferGroupID *int64 `json:"transfer_group_id"`
	ImportBatchID   *int64 `json:"import_batch_id"`
}

func toTransactionDTO(t transaction.Transaction, exponents map[string]int) TransactionDTO {
	return TransactionDTO{
		ID: t.ID, AccountID: t.AccountID, AccountName: t.AccountName,
		CategoryID: t.CategoryID, CategoryName: t.CategoryName,
		OccurredOn: t.OccurredOn.Format(dateLayout), Kind: string(t.Kind),
		Amount:      toMoney(t.Amount, exponents),
		BaseAmount:  toBaseMoney(t.BaseAmount, t.FxRateID, t.FxAsOf, exponents),
		Description: t.Description, Merchant: t.Merchant, Occurrence: t.Occurrence,
		Unconverted:     t.BaseAmount == nil,
		TransferGroupID: t.TransferGroupID, ImportBatchID: t.ImportBatchID,
	}
}

// TransactionInputDTO is the create/update payload.
type TransactionInputDTO struct {
	AccountID   int64    `json:"account_id"`
	CategoryID  *int64   `json:"category_id"`
	OccurredOn  string   `json:"occurred_on"`
	Kind        string   `json:"kind"`
	Amount      MoneyDTO `json:"amount"`
	Description *string  `json:"description"`
	Merchant    *string  `json:"merchant"`
}

func (d TransactionInputDTO) toInput(exponents map[string]int, requireDate bool) (transaction.Input, error) {
	in := transaction.Input{
		AccountID: d.AccountID, CategoryID: d.CategoryID, Kind: transaction.Kind(d.Kind),
		Description: d.Description, Merchant: d.Merchant,
	}
	if d.OccurredOn != "" {
		on, err := parseDate("occurred_on", d.OccurredOn)
		if err != nil {
			return transaction.Input{}, err
		}
		in.OccurredOn = on
	} else if requireDate {
		return transaction.Input{}, apperr.Validation("occurred_on", "is required")
	}
	if d.Amount.Currency != "" || requireDate {
		amount, err := d.Amount.Money("amount", exponents)
		if err != nil {
			return transaction.Input{}, err
		}
		in.Amount = amount
	}
	return in, nil
}

// transactionFilter reads the history filter from the query string. The CSV
// export uses the same one, so what is exported is exactly what is on screen.
func transactionFilter(c *fiber.Ctx) (transaction.Filter, error) {
	f := transaction.Filter{
		Kind:  transaction.Kind(c.Query("kind")),
		Query: c.Query("q"),
		Limit: c.QueryInt("limit", 50),
	}
	if v := c.Query("from"); v != "" {
		from, err := parseDate("from", v)
		if err != nil {
			return transaction.Filter{}, err
		}
		f.From = &from
	}
	if v := c.Query("to"); v != "" {
		to, err := parseDate("to", v)
		if err != nil {
			return transaction.Filter{}, err
		}
		f.To = &to
	}
	if v := c.QueryInt("category_id", 0); v != 0 {
		id := int64(v)
		f.CategoryID = &id
	}
	if v := c.QueryInt("account_id", 0); v != 0 {
		id := int64(v)
		f.AccountID = &id
	}
	cursor, err := transaction.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return transaction.Filter{}, err
	}
	f.Cursor = cursor
	return f, nil
}

func (s *Server) handleListTransactions(c *fiber.Ctx) error {
	u := currentUser(c)
	f, err := transactionFilter(c)
	if err != nil {
		return err
	}

	page, err := s.deps.Transactions.List(c.UserContext(), u.ID, f)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	items := make([]TransactionDTO, 0, len(page.Items))
	for _, t := range page.Items {
		items = append(items, toTransactionDTO(t, exponents))
	}
	out := fiber.Map{"items": items, "has_more": page.HasMore, "next_cursor": nil}
	if page.NextCursor != nil {
		out["next_cursor"] = transaction.EncodeCursor(*page.NextCursor)
	}
	return c.JSON(out)
}

func (s *Server) handleCreateTransaction(c *fiber.Ctx) error {
	return s.withIdempotency(c, func() (int, any, error) {
		var req TransactionInputDTO
		if err := bind(c, &req); err != nil {
			return 0, nil, err
		}
		exponents, err := s.exponents(c.UserContext())
		if err != nil {
			return 0, nil, err
		}
		in, err := req.toInput(exponents, true)
		if err != nil {
			return 0, nil, err
		}
		u := currentUser(c)
		created, err := s.deps.Transactions.Create(c.UserContext(), u.ID, u.BaseCurrency, in)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, toTransactionDTO(created, exponents), nil
	})
}

func (s *Server) handleGetTransaction(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	u := currentUser(c)
	t, err := s.deps.Transactions.Get(c.UserContext(), u.ID, id)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	return c.JSON(toTransactionDTO(t, exponents))
}

func (s *Server) handleUpdateTransaction(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	var req TransactionInputDTO
	if err := bind(c, &req); err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	in, err := req.toInput(exponents, false)
	if err != nil {
		return err
	}
	u := currentUser(c)
	updated, err := s.deps.Transactions.Update(c.UserContext(), u.ID, id, u.BaseCurrency, in)
	if err != nil {
		return err
	}
	return c.JSON(toTransactionDTO(updated, exponents))
}

func (s *Server) handleDeleteTransaction(c *fiber.Ctx) error {
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	u := currentUser(c)
	if err := s.deps.Transactions.Delete(c.UserContext(), u.ID, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) handleCreateTransfer(c *fiber.Ctx) error {
	return s.withIdempotency(c, func() (int, any, error) {
		var req struct {
			FromAccountID int64    `json:"from_account_id"`
			ToAccountID   int64    `json:"to_account_id"`
			OccurredOn    string   `json:"occurred_on"`
			Amount        MoneyDTO `json:"amount"`
			Description   *string  `json:"description"`
		}
		if err := bind(c, &req); err != nil {
			return 0, nil, err
		}
		exponents, err := s.exponents(c.UserContext())
		if err != nil {
			return 0, nil, err
		}
		on, err := parseDate("occurred_on", req.OccurredOn)
		if err != nil {
			return 0, nil, err
		}
		amount, err := req.Amount.Money("amount", exponents)
		if err != nil {
			return 0, nil, err
		}
		u := currentUser(c)
		pair, err := s.deps.Transactions.CreateTransfer(c.UserContext(), u.ID, u.BaseCurrency,
			transaction.TransferInput{
				FromAccountID: req.FromAccountID, ToAccountID: req.ToAccountID,
				OccurredOn: on, Amount: amount, Description: req.Description,
			})
		if err != nil {
			return 0, nil, err
		}
		out := make([]TransactionDTO, 0, len(pair))
		for _, t := range pair {
			out = append(out, toTransactionDTO(t, exponents))
		}
		return http.StatusCreated, out, nil
	})
}

// withIdempotency replays the original response when a client retries a POST with
// the same Idempotency-Key, so an optimistic save that timed out cannot become two
// transactions.
func (s *Server) withIdempotency(c *fiber.Ctx, run func() (int, any, error)) error {
	key := c.Get(HeaderIdempotencyKey)
	u := currentUser(c)
	if key == "" || s.deps.Idempotency == nil || u == nil {
		status, body, err := run()
		if err != nil {
			return err
		}
		return c.Status(status).JSON(body)
	}

	hash := sha256.Sum256(c.Body())
	requestHash := hex.EncodeToString(hash[:])
	found, status, stored, err := s.deps.Idempotency.Find(
		c.UserContext(), u.ID, key, c.Method(), c.Path(), requestHash)
	if err != nil {
		return err
	}
	if found {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		c.Set("Idempotent-Replay", "true")
		return c.Status(status).Send(stored)
	}

	newStatus, body, err := run()
	if err != nil {
		return err
	}
	encoded, err := c.App().Config().JSONEncoder(body)
	if err != nil {
		return err
	}
	if err := s.deps.Idempotency.Save(c.UserContext(), u.ID, key, c.Method(), c.Path(),
		requestHash, newStatus, encoded, s.deps.Clock.Now()); err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Status(newStatus).Send(encoded)
}
