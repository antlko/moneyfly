package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/db"
	"moneyfly/internal/importer"
	syncproto "moneyfly/internal/sync"
)

// --- shared --------------------------------------------------------------------

type parseErrorDTO struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

func toParseErrorDTOs(errs []importer.RowError) []parseErrorDTO {
	out := make([]parseErrorDTO, 0, len(errs))
	for _, e := range errs {
		out = append(out, parseErrorDTO{Line: e.Line, Reason: e.Reason})
	}
	return out
}

func toNamedRows(names []db.NameKind) []importer.NamedRow {
	out := make([]importer.NamedRow, 0, len(names))
	for _, n := range names {
		out = append(out, importer.NamedRow{ID: n.ID, Name: n.Name, Kind: n.Kind, Currency: n.Currency})
	}
	return out
}

// nameStatusDTO is one distinct category or account name found in the CSV,
// with how it resolves against what the account already has.
type nameStatusDTO struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`     // category only
	Currency string `json:"currency,omitempty"` // account only
	Resolved bool   `json:"resolved"`
	ID       string `json:"id,omitempty"`
	ViaAlias bool   `json:"viaAlias,omitempty"`
	// CurrencyMismatch means the name matched an existing account, but none of
	// that name has this currency — surfaced separately from a plain "no such
	// account" so the operator knows the account exists, just not for this
	// currency, per importer.ResolveAccount.
	CurrencyMismatch bool `json:"currencyMismatch,omitempty"`
	Count            int  `json:"count"`
}

// summariseCategories returns each distinct (name, kind) pair in the CSV, in
// order of first appearance, resolved against what already exists, with how
// many rows each one covers — the count is what tells the operator whether an
// unresolved name is worth pausing for or is one stray row.
func summariseCategories(rows []importer.Row, existing []importer.NamedRow) []nameStatusDTO {
	type key struct{ name, kind string }
	var order []key
	counts := map[key]int{}
	for _, r := range rows {
		k := key{r.Category, r.Kind}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}

	out := make([]nameStatusDTO, 0, len(order))
	for _, k := range order {
		res := importer.ResolveCategory(k.name, k.kind, existing, nil)
		out = append(out, nameStatusDTO{
			Name: k.name, Kind: k.kind, Resolved: res.Resolved(), ID: res.ID,
			ViaAlias: res.ViaAlias, Count: counts[k],
		})
	}
	return out
}

// summariseAccounts groups by name alone, taking the currency of the first
// row seen for it as representative — true for every real Monefy export,
// where an account name is a permanent wallet and so a permanent currency.
// A row whose own currency later disagrees with that is still checked and
// reported individually by buildImportOps at commit time, which is why this
// summary is a preview, not the authority.
func summariseAccounts(rows []importer.Row, existing []importer.NamedRow) []nameStatusDTO {
	var order []string
	counts := map[string]int{}
	currency := map[string]string{}
	for _, r := range rows {
		if counts[r.Account] == 0 {
			order = append(order, r.Account)
			currency[r.Account] = r.Currency
		}
		counts[r.Account]++
	}

	out := make([]nameStatusDTO, 0, len(order))
	for _, name := range order {
		cur := currency[name]
		res := importer.ResolveAccount(name, cur, existing, nil)
		out = append(out, nameStatusDTO{
			Name: name, Currency: cur, Resolved: res.Resolved(), ID: res.ID,
			CurrencyMismatch: res.CurrencyMismatch, Count: counts[name],
		})
	}
	return out
}

// --- preview ---------------------------------------------------------------------

type importPreviewRequest struct {
	CSV string `json:"csv"`
}

type importPreviewResponse struct {
	TotalRows   int             `json:"totalRows"`
	ParseErrors []parseErrorDTO `json:"parseErrors"`
	Categories  []nameStatusDTO `json:"categories"`
	Accounts    []nameStatusDTO `json:"accounts"`
}

// handleImportPreview parses a Monefy CSV export and reports what it will
// take to import cleanly, without writing anything. See
// docs/MONEFY-PARITY.md §5 for the format this reads and internal/importer
// for why an unrecognised category or account never resolves itself.
func (s *Server) handleImportPreview(c fiber.Ctx) error {
	var in importPreviewRequest
	if err := decode(c, &in); err != nil {
		return err
	}

	res, err := importer.Parse(strings.NewReader(in.CSV))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read the file as CSV: "+err.Error())
	}

	user := userLocal(c)
	existingCategories, existingAccounts, err := s.importNames(user.ID)
	if err != nil {
		return err
	}

	return c.JSON(importPreviewResponse{
		TotalRows:   len(res.Rows),
		ParseErrors: toParseErrorDTOs(res.Errors),
		Categories:  summariseCategories(res.Rows, existingCategories),
		Accounts:    summariseAccounts(res.Rows, existingAccounts),
	})
}

func (s *Server) importNames(userID string) (categories, accounts []importer.NamedRow, err error) {
	cats, err := s.conn().CategoryNames(userID)
	if err != nil {
		return nil, nil, err
	}
	accs, err := s.conn().AccountNames(userID)
	if err != nil {
		return nil, nil, err
	}
	return toNamedRows(cats), toNamedRows(accs), nil
}

// --- commit ------------------------------------------------------------------------

type importCommitRequest struct {
	CSV string `json:"csv"`
	// Keyed on the CSV's own name for that category or account — not
	// normalised — because that is exactly what the preview step reported as
	// unresolved and what the operator picked a target for.
	CategoryMap map[string]string `json:"categoryMap"`
	AccountMap  map[string]string `json:"accountMap"`
}

type importCommitResponse struct {
	Imported        int             `json:"imported"`
	AlreadyImported int             `json:"alreadyImported"`
	ParseErrors     []parseErrorDTO `json:"parseErrors"`
	Unresolved      []parseErrorDTO `json:"unresolved"`
}

// handleImportCommit re-parses the same CSV text the preview step saw and
// writes every row it can resolve.
//
// It re-parses rather than trusting anything the client remembers from the
// preview response, for the same reason the sync protocol never trusts a
// client's idea of state: the categories and accounts a row resolves against
// may have changed in the seconds between the two requests, on this device
// or another, and re-reading them here is what keeps that honest.
func (s *Server) handleImportCommit(c fiber.Ctx) error {
	var in importCommitRequest
	if err := decode(c, &in); err != nil {
		return err
	}

	res, err := importer.Parse(strings.NewReader(in.CSV))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read the file as CSV: "+err.Error())
	}

	user := userLocal(c)
	existingCategories, existingAccounts, err := s.importNames(user.ID)
	if err != nil {
		return err
	}
	existingKeys, err := s.conn().TxnNaturalKeys(user.ID)
	if err != nil {
		return err
	}

	ops, alreadyImported, unresolved, err := buildImportOps(
		res.Rows, existingCategories, existingAccounts, in.CategoryMap, in.AccountMap, existingKeys)
	if err != nil {
		return err
	}

	// ApplyOps rejects a batch over syncproto.MaxOpsPerPush outright, and an
	// import is routinely thousands of rows in one request — nothing else
	// that builds ops (a push, the recurring worker) hands over more than a
	// device's own queue or a single rule's tick, so chunking is only ever
	// needed here.
	var accepted int
	var lastSeq int64
	for chunk := range slices.Chunk(ops, syncproto.MaxOpsPerPush) {
		result, err := s.conn().ApplyOps(user.ID, chunk)
		if err != nil {
			return err
		}
		accepted += result.Accepted
		lastSeq = result.ServerSeq
	}
	// Every one of these transactions is new to every device, including
	// whichever one is running the import — unlike an ordinary push, there
	// is no originating device already holding the rows locally to skip.
	if accepted > 0 {
		s.events.publish(user.ID, syncEvent{Seq: lastSeq, DeviceID: serverDeviceID})
	}

	return c.JSON(importCommitResponse{
		Imported:        accepted,
		AlreadyImported: alreadyImported,
		ParseErrors:     toParseErrorDTOs(res.Errors),
		Unresolved:      unresolved,
	})
}

// buildImportOps resolves each row and turns the ones it can into sync ops,
// de-duplicating against natural keys the account already has.
//
// A row whose category or account does not resolve is reported in
// `unresolved` and otherwise skipped — never coerced onto some other
// category, never left to fail a constraint, per internal/importer's package
// doc. This mirrors the recurring worker's own check-before-insert
// (internal/api/recurring.go) using the same natural-key mechanism, just
// checked against a set fetched once instead of one query per row: an import
// is thousands of rows in a single request, not a handful per hourly tick.
func buildImportOps(
	rows []importer.Row,
	existingCategories, existingAccounts []importer.NamedRow,
	categoryMap, accountMap map[string]string,
	existingKeys map[string]bool,
) (ops []syncproto.Op, alreadyImported int, unresolved []parseErrorDTO, err error) {
	// Initialised, never nil: a Go nil slice marshals to JSON `null`, and the
	// client's type says this is always an array — an import with nothing
	// unresolved would otherwise hand the frontend a `null` where it calls
	// `.length`, the exact incident `api.rejections` exists to avoid.
	unresolved = []parseErrorDTO{}
	occurrence := map[string]int{}

	for _, row := range rows {
		cat := importer.ResolveCategory(row.Category, row.Kind, existingCategories, categoryMap)
		if !cat.Resolved() {
			unresolved = append(unresolved, parseErrorDTO{
				Line: row.Line, Reason: fmt.Sprintf("category %q is not mapped", row.Category)})
			continue
		}
		acc := importer.ResolveAccount(row.Account, row.Currency, existingAccounts, accountMap)
		if !acc.Resolved() {
			reason := fmt.Sprintf("account %q is not mapped", row.Account)
			if acc.CurrencyMismatch {
				reason = fmt.Sprintf("account %q exists but not in %s", row.Account, row.Currency)
			}
			unresolved = append(unresolved, parseErrorDTO{Line: row.Line, Reason: reason})
			continue
		}

		// The occurrence group deliberately excludes the note: two rows that
		// differ only in description are already distinguishable without it,
		// and folding it in would make NaturalKey (which does hash the note)
		// the only thing standing between "same purchase, re-exported" and "a
		// coincidentally identical one" — this way the group is coarser than
		// the key, which is the safer direction to be wrong in.
		group := row.OccurredOn + "|" + row.Account + "|" + row.Category + "|" + row.Kind
		n := occurrence[group]
		occurrence[group] = n + 1
		naturalKey := importer.NaturalKey(row, n)

		if existingKeys[naturalKey] {
			alreadyImported++
			continue
		}

		amountMinor := row.AmountMinor
		if row.Kind == "expense" {
			amountMinor = -amountMinor
		}
		body, marshalErr := json.Marshal(map[string]any{
			"kind":        row.Kind,
			"occurredOn":  row.OccurredOn,
			"amountMinor": amountMinor,
			"currency":    row.Currency,
			"categoryId":  cat.ID,
			"accountId":   acc.ID,
			"note":        row.Note,
			"naturalKey":  naturalKey,
		})
		if marshalErr != nil {
			return nil, 0, nil, marshalErr
		}
		ops = append(ops, syncproto.Op{
			Entity: "txn", ID: db.NewID(), Lamport: 1, DeviceID: serverDeviceID, Data: body,
		})
	}
	return ops, alreadyImported, unresolved, nil
}
