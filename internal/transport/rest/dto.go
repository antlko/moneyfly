package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// MoneyDTO is how money crosses the wire: integer minor units plus the exponent
// the client needs to render it. No float ever crosses (docs/09-api.md §9.1).
type MoneyDTO struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Exponent    int    `json:"exponent"`
}

// BaseMoneyDTO additionally carries the rate that produced the figure, so any
// converted number can be traced to its source.
type BaseMoneyDTO struct {
	AmountMinor int64   `json:"amount_minor"`
	Currency    string  `json:"currency"`
	Exponent    int     `json:"exponent"`
	FxRateID    *int64  `json:"fx_rate_id"`
	AsOfDate    *string `json:"as_of_date"`
}

// toMoney renders a Money with the exponent from the currency table.
func toMoney(m money.Money, exponents map[string]int) MoneyDTO {
	return MoneyDTO{AmountMinor: m.Minor, Currency: m.Currency, Exponent: exponents[m.Currency]}
}

// toMoneyPtr renders an optional amount. nil stays null: not recorded is not zero.
func toMoneyPtr(m *money.Money, exponents map[string]int) *MoneyDTO {
	if m == nil {
		return nil
	}
	out := toMoney(*m, exponents)
	return &out
}

func toBaseMoney(m *money.Money, rateID *int64, asOf *time.Time, exponents map[string]int) *BaseMoneyDTO {
	if m == nil {
		return nil
	}
	out := BaseMoneyDTO{
		AmountMinor: m.Minor, Currency: m.Currency, Exponent: exponents[m.Currency], FxRateID: rateID,
	}
	if asOf != nil {
		s := asOf.UTC().Format(dateLayout)
		out.AsOfDate = &s
	}
	return &out
}

// Money converts an inbound DTO, verifying that the amount is consistent with the
// exponent the client claims — a client that thinks HUF has cents must be told.
func (d MoneyDTO) Money(field string, exponents map[string]int) (money.Money, error) {
	if d.Currency == "" {
		return money.Money{}, apperr.Validation(field+".currency", "is required")
	}
	m := money.New(d.AmountMinor, d.Currency)
	exp, ok := exponents[m.Currency]
	if !ok {
		return money.Money{}, apperr.Validation(field+".currency", "unknown currency %q", d.Currency)
	}
	if d.Exponent != 0 && d.Exponent != exp {
		return money.Money{}, apperr.Validation(field+".exponent",
			"%s has exponent %d, not %d", m.Currency, exp, d.Exponent)
	}
	return m, nil
}

const dateLayout = "2006-01-02"

// bind decodes a JSON body, rejecting unknown fields so a client typo surfaces
// immediately rather than being silently ignored (docs/09-api.md §9.4).
func bind(c *fiber.Ctx, dst any) error {
	body := c.Body()
	if len(bytes.TrimSpace(body)) == 0 {
		return apperr.Validation("body", "a JSON object is required")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return apperr.Validation("body", "%v", cleanJSONError(err))
	}
	if dec.More() {
		return apperr.Validation("body", "must contain exactly one JSON object")
	}
	return nil
}

func cleanJSONError(err error) error {
	// errors.As, not a type assertion: the decoder may wrap.
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return fmt.Errorf("%s must be a %s", field, typeErr.Type.String())
	}
	return err
}

// parseDate reads a YYYY-MM-DD path or query value.
func parseDate(field, value string) (time.Time, error) {
	t, err := time.Parse(dateLayout, value)
	if err != nil {
		return time.Time{}, apperr.Validation(field, "must be a date in YYYY-MM-DD form, got %q", value)
	}
	return t.UTC(), nil
}

// paramInt64 reads a numeric path parameter.
func paramInt64(c *fiber.Ctx, name string) (int64, error) {
	v, err := c.ParamsInt(name)
	if err != nil {
		return 0, apperr.Validation(name, "must be an integer")
	}
	return int64(v), nil
}
