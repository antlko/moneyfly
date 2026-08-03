package rest

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func (s *Server) registerMetricRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged()}

	g := api.Group("/metrics", guard...)
	g.Get("/", s.handleMetricRegistry)
	g.Post("/validate", s.requireWritable(), s.handleValidateMetric)
	g.Post("/evaluate", s.requireWritable(), s.handleEvaluateMetric)

	api.Group("/export", guard...).Get("/transactions.csv", s.handleExportTransactions)
}

// MetricRegistryDTO lists what a user-defined metric may name.
//
// It is published so the editor can offer completions rather than letting someone
// discover the vocabulary by trial and error.
type MetricRegistryDTO struct {
	// Identifiers are the period-level quantities.
	Identifiers []string `json:"identifiers"`
	// References take a name, as in spend('Food').
	References []string `json:"references"`
	Functions  []string `json:"functions"`
	Operators  []string `json:"operators"`
	Note       string   `json:"note"`
}

// registryIdentifiers is the fixed set of names a metric may use. Anything else
// is a named error rather than a silent zero.
var registryIdentifiers = []string{
	"spend_total", "planned_total", "possible_minimum", "income", "diff", "saved_percent",
	"ready_for_usage", "general", "runway_months", "burn_rate",
}

func knownIdentifier(name string) bool {
	if metrics.References[name] {
		return true
	}
	for _, known := range registryIdentifiers {
		if known == name {
			return true
		}
	}
	return false
}

func (s *Server) handleMetricRegistry(c *fiber.Ctx) error {
	references := make([]string, 0, len(metrics.References))
	for name := range metrics.References {
		references = append(references, name)
	}
	sort.Strings(references)

	functions := make([]string, 0, len(metrics.Functions))
	for name := range metrics.Functions {
		functions = append(functions, name)
	}
	sort.Strings(functions)

	return c.JSON(MetricRegistryDTO{
		Identifiers: registryIdentifiers,
		References:  references,
		Functions:   functions,
		Operators:   []string{"+", "-", "*", "/", ">", "<", ">=", "<=", "==", "!="},
		Note: "A metric is parsed to a syntax tree and evaluated against loaded values. " +
			"There are no cell references, no loops and no I/O, and nothing is executed as code. " +
			"Division by zero is null, never infinity.",
	})
}

// MetricExpressionDTO is an expression to check or run.
type MetricExpressionDTO struct {
	Expression string `json:"expression"`
	// Values supplies the identifiers for an evaluation. A null value means the
	// quantity was not recorded, which propagates to a null result.
	Values map[string]*float64 `json:"values"`
}

func (s *Server) handleValidateMetric(c *fiber.Ctx) error {
	var body MetricExpressionDTO
	if err := bind(c, &body); err != nil {
		return err
	}
	if err := metrics.Validate(body.Expression, knownIdentifier); err != nil {
		// A syntax error is a fact about the input, not a server fault: it comes
		// back as a field error the editor can put under the box.
		return apperr.Validation("expression", "%v", err)
	}
	return c.JSON(fiber.Map{"expression": body.Expression, "valid": true})
}

func (s *Server) handleEvaluateMetric(c *fiber.Ctx) error {
	var body MetricExpressionDTO
	if err := bind(c, &body); err != nil {
		return err
	}
	node, err := metrics.Parse(body.Expression)
	if err != nil {
		return apperr.Validation("expression", "%v", err)
	}
	if err := metrics.Validate(body.Expression, knownIdentifier); err != nil {
		return apperr.Validation("expression", "%v", err)
	}

	result, err := metrics.Eval(node, func(name, arg string) (*float64, error) {
		key := name
		if arg != "" {
			key = fmt.Sprintf("%s('%s')", name, arg)
		}
		value, ok := body.Values[key]
		if !ok {
			return nil, apperr.Validation("values", "no value supplied for %s", key)
		}
		return value, nil
	})
	if err != nil {
		return err
	}
	// null rather than 0 when anything was unrecorded or divided by zero.
	return c.JSON(fiber.Map{"expression": body.Expression, "value": result})
}

// handleExportTransactions streams the history as CSV.
//
// It is deliberately the raw rows rather than a report: an export exists so the
// data can leave, and a summary is not the data (docs/08-ux.md §8.7).
func (s *Server) handleExportTransactions(c *fiber.Ctx) error {
	u := currentUser(c)
	f, err := transactionFilter(c)
	if err != nil {
		return err
	}
	f.Limit = 5000

	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}

	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="moneyapp-transactions.csv"`)

	var buf csvBuffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{
		"date", "account", "category", "kind", "amount", "currency",
		"base_amount", "base_currency", "description", "import_batch_id",
	})

	// Paged rather than one query: the cursor is what keeps a long history from
	// being one enormous result set.
	for {
		page, err := s.deps.Transactions.List(c.UserContext(), u.ID, f)
		if err != nil {
			return err
		}
		for _, t := range page.Items {
			row := []string{
				t.OccurredOn.Format(dateLayout), t.AccountName, t.CategoryName, string(t.Kind),
				t.Amount.String(exponents[t.Amount.Currency]), t.Amount.Currency,
				"", "", "", "",
			}
			if t.BaseAmount != nil {
				row[6] = t.BaseAmount.String(exponents[t.BaseAmount.Currency])
				row[7] = t.BaseAmount.Currency
			}
			if t.Description != nil {
				row[8] = *t.Description
			}
			if t.ImportBatchID != nil {
				row[9] = strconv.FormatInt(*t.ImportBatchID, 10)
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
		if !page.HasMore || page.NextCursor == nil {
			break
		}
		f.Cursor = *page.NextCursor
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return c.Send(buf.Bytes())
}

// csvBuffer is a minimal io.Writer so the CSV writer can build the body without
// a bytes.Buffer import shadowing the one dto.go already uses.
type csvBuffer struct{ data []byte }

func (b *csvBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

// Bytes returns the accumulated CSV.
func (b *csvBuffer) Bytes() []byte { return b.data }
