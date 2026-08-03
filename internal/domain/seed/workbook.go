package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// Workbook is the spreadsheet's history, extracted.
//
// It is the same JSON the parity suite asserts against — `testdata/parity/`,
// produced from the .xlsx by `generate.py`. Reading the extract rather than the
// workbook keeps an XLSX parser out of a binary that would use it exactly once,
// and means the file the migration loads is the file the tests check.
//
// The workbook itself is deliberately not committed: it holds real finances.
type Workbook struct {
	Source     string                 `json:"source"`
	Base       string                 `json:"base_currency"`
	Periods    []period.Period        `json:"periods"`
	Recorded   map[period.Period]bool `json:"recorded"`
	Categories []WorkbookCategory     `json:"categories"`
	Capital    WorkbookCapital        `json:"capital"`
	Rollups    struct {
		Income map[period.Period]struct {
			ExpectedMinor int64 `json:"expected_minor"`
		} `json:"income"`
	} `json:"rollups"`
}

// WorkbookCategory is one expense row.
type WorkbookCategory struct {
	Row          int                     `json:"row"`
	SheetName    string                  `json:"sheet_name"`
	Name         string                  `json:"name"`
	PlannedMinor int64                   `json:"planned_minor"`
	Spend        map[period.Period]int64 `json:"spend"`
	// SpendSheet is the original float, kept so a rounded figure stays traceable
	// to what the cell actually said.
	SpendSheet map[period.Period]float64 `json:"spend_sheet"`
}

// WorkbookCapital is the capital block.
type WorkbookCapital struct {
	Rates    map[string]float64 `json:"rates"`
	Accounts []struct {
		Row      int                     `json:"row"`
		Name     string                  `json:"name"`
		Currency string                  `json:"currency"`
		Exponent int                     `json:"exponent"`
		Native   map[period.Period]int64 `json:"native"`
	} `json:"accounts"`
}

// LoadWorkbook reads the extract.
func LoadWorkbook(path string) (Workbook, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path
	if err != nil {
		return Workbook{}, fmt.Errorf("seed: reading %s: %w", path, err)
	}
	var wb Workbook
	if err := json.Unmarshal(raw, &wb); err != nil {
		return Workbook{}, fmt.Errorf("seed: decoding %s: %w", path, err)
	}
	if len(wb.Periods) == 0 {
		return Workbook{}, fmt.Errorf("seed: %s has no periods", path)
	}
	return wb, nil
}

// MigrationPlan is what a migration would do, or did.
type MigrationPlan struct {
	Budgets    int
	Snapshots  int
	Aggregates int
	Rates      int
	Income     int
	// SkippedPeriods are the months the Monefy import already covers. The CSV is
	// authoritative for transaction detail, so writing the workbook's monthly
	// aggregate on top would double every one of them.
	SkippedPeriods []period.Period
	// Notes carries anything the operator should read before committing.
	Notes []string
}

// WorkbookTarget is everything the migration writes through. Every write goes via
// a domain service, so the migration cannot bypass a validation the app enforces.
type WorkbookTarget struct {
	Categories   *category.Service
	Accounts     *account.Service
	Budgets      *budget.Service
	Capital      *capital.Service
	Transactions *transaction.Service
	// RecordedPeriods returns the months that already hold transactions, which is
	// how the overlap with the Monefy import is detected.
	RecordedPeriods func(ctx context.Context, userID int64) ([]period.Period, error)
	// StoreRate writes one historical rate.
	StoreRate func(ctx context.Context, asOf time.Time, quote, rate string) error
}

// AggregateNote marks a transaction the workbook supplied as a monthly total
// rather than a real line item. It is deliberately visible in the description.
const AggregateNote = "workbook monthly aggregate"

// MigrateWorkbook loads the workbook's history.
//
// With dryRun the database is untouched and the plan describes what would
// happen. Re-running with commit is idempotent: budgets and snapshots upsert, and
// the aggregate transactions carry a natural key derived from the same inputs, so
// the unique index refuses a second copy.
func MigrateWorkbook(
	ctx context.Context, userID int64, wb Workbook, target WorkbookTarget, dryRun bool,
) (MigrationPlan, error) {
	plan := MigrationPlan{}

	covered := map[period.Period]bool{}
	if target.RecordedPeriods != nil {
		recorded, err := target.RecordedPeriods(ctx, userID)
		if err != nil {
			return plan, err
		}
		for _, p := range recorded {
			covered[p] = true
		}
	}

	cats, err := target.Categories.List(ctx, userID, "", true)
	if err != nil {
		return plan, err
	}
	categoryByName := map[string]category.Category{}
	for _, c := range cats {
		categoryByName[c.Name] = c
	}

	accounts, err := target.Accounts.List(ctx, userID, true)
	if err != nil {
		return plan, err
	}
	accountByName := map[string]account.Account{}
	for _, a := range accounts {
		accountByName[a.Name] = a
	}

	first, last := wb.Periods[0], wb.Periods[len(wb.Periods)-1]

	// The four rates the sheet hand-typed, dated from the first period so the
	// nearest-earlier lookup covers the whole history. Only EUR->X is stored: the
	// sheet's own USD/EUR contradicts its EUR/USD, and one direction removes the
	// contradiction by construction.
	for quote, perEUR := range wb.Capital.Rates {
		if perEUR == 0 {
			continue
		}
		plan.Rates++
		if dryRun || target.StoreRate == nil {
			continue
		}
		from, _ := first.Range()
		if err := target.StoreRate(ctx, from, quote, formatRate(1/perEUR)); err != nil {
			return plan, err
		}
	}

	for _, wc := range wb.Categories {
		c, ok := categoryByName[wc.Name]
		if !ok {
			plan.Notes = append(plan.Notes,
				fmt.Sprintf("no category named %q (sheet row %d); its rows are skipped", wc.Name, wc.Row))
			continue
		}

		// Column B is one figure applied to every month, which is how the sheet
		// used it.
		if wc.PlannedMinor > 0 {
			plan.Budgets += len(wb.Periods)
			if !dryRun {
				if _, err := target.Budgets.Bulk(ctx, userID, first, last, []budget.BulkItem{
					{CategoryID: c.ID, Planned: money.New(wc.PlannedMinor, wb.Base)},
				}); err != nil {
					return plan, err
				}
			}
		}

		for _, p := range wb.Periods {
			minor, ok := wc.Spend[p]
			// A -1 cell never reaches the extract, and an absent key is an absent
			// month. Neither becomes a row: this is the sentinel that corrupted
			// the workbook's own totals.
			if !ok || minor == 0 {
				continue
			}
			if covered[p] {
				continue
			}
			plan.Aggregates++
			if dryRun {
				continue
			}
			day, _ := p.Range()
			note := fmt.Sprintf("%s (%s cell %v)", AggregateNote, wc.SheetName, wc.SpendSheet[p])
			id := c.ID
			if _, err := target.Transactions.Create(ctx, userID, wb.Base, transaction.Input{
				AccountID:   accountByName["Cash EUR"].ID,
				CategoryID:  &id,
				OccurredOn:  day,
				Kind:        transaction.KindExpense,
				Amount:      money.New(minor, wb.Base),
				Description: &note,
			}); err != nil {
				return plan, fmt.Errorf("seed: %s %s: %w", wc.Name, p, err)
			}
		}
	}

	for p := range covered {
		if wb.Recorded[p] {
			plan.SkippedPeriods = append(plan.SkippedPeriods, p)
		}
	}
	sort.Slice(plan.SkippedPeriods, func(i, j int) bool {
		return plan.SkippedPeriods[i] < plan.SkippedPeriods[j]
	})
	if len(plan.SkippedPeriods) > 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf(
			"%d month(s) already hold transactions and were skipped: the Monefy export is "+
				"authoritative for detail, and writing the workbook's monthly aggregate on top "+
				"would double every one of them (%s)",
			len(plan.SkippedPeriods), joinPeriods(plan.SkippedPeriods)))
	}

	// Income, which the sheet typed into the formula bar rather than recording.
	salary := categoryByName["Salary"]
	for p, entry := range wb.Rollups.Income {
		if entry.ExpectedMinor == 0 || covered[p] || salary.ID == 0 {
			continue
		}
		plan.Income++
		if dryRun {
			continue
		}
		day, _ := p.Range()
		note := AggregateNote + " (row 31 Salary)"
		id := salary.ID
		if _, err := target.Transactions.Create(ctx, userID, wb.Base, transaction.Input{
			AccountID: accountByName["Cash EUR"].ID, CategoryID: &id, OccurredOn: day,
			Kind: transaction.KindIncome, Amount: money.New(entry.ExpectedMinor, wb.Base),
			Description: &note,
		}); err != nil {
			return plan, fmt.Errorf("seed: income %s: %w", p, err)
		}
	}

	// The capital block, one snapshot per account per month.
	for _, wa := range wb.Capital.Accounts {
		a, ok := accountByName[wa.Name]
		if !ok {
			plan.Notes = append(plan.Notes,
				fmt.Sprintf("no account named %q (sheet row %d); its balances are skipped", wa.Name, wa.Row))
			continue
		}
		for p, minor := range wa.Native {
			plan.Snapshots++
			if dryRun {
				continue
			}
			amount := money.New(minor, wa.Currency)
			if _, err := target.Capital.Upsert(ctx, userID, a.ID, p, wb.Base, capital.Input{
				Amount: &amount,
				Note:   fmt.Sprintf("workbook row %d", wa.Row),
			}); err != nil {
				return plan, fmt.Errorf("seed: snapshot %s %s: %w", wa.Name, p, err)
			}
		}
	}

	return plan, nil
}

// formatRate renders a rate with enough places to round-trip the sheet's four
// decimal inputs through their reciprocal.
func formatRate(v float64) string {
	out := fmt.Sprintf("%.12f", v)
	out = strings.TrimRight(out, "0")
	return strings.TrimRight(out, ".")
}

func joinPeriods(periods []period.Period) string {
	parts := make([]string, 0, len(periods))
	for _, p := range periods {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, ", ")
}
