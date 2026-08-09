package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	// Key identifies this entry for the mapping the client sends back, and is
	// the *composite* — "expense:Gifts", "HUF:Cash" — not the bare name.
	//
	// The name alone is not an identity in either direction. A CSV that uses
	// "Gifts" as both an expense and an income category produces two entries
	// here (they resolve against different existing rows), and keying the map
	// on the name meant mapping one silently mapped the other onto a category
	// of the wrong kind. The same for an account name that appears in two
	// currencies. The client never builds this — it echoes it back.
	Key      string `json:"key"`
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
			Key:  importer.CategoryKey(k.name, k.kind),
			Name: k.name, Kind: k.kind, Resolved: res.Resolved(), ID: res.ID,
			ViaAlias: res.ViaAlias, Count: counts[k],
		})
	}
	return out
}

// summariseAccounts returns each distinct (name, currency) pair, in order of
// first appearance.
//
// Grouped by the pair and not by the name, because that is what
// ResolveAccount actually matches on — an account is scoped to its currency, so
// "Cash" in EUR and "Cash" in HUF are two different wallets. Grouping by name
// and taking the first row's currency as representative made the preview claim
// one resolved entry where the commit would find two, and the rows behind the
// second currency were then dropped as unresolved after the operator had been
// told the file was clean. Preview and commit have to agree about what a thing
// *is* before they can agree about anything else.
func summariseAccounts(rows []importer.Row, existing []importer.NamedRow) []nameStatusDTO {
	type key struct{ name, currency string }
	var order []key
	counts := map[key]int{}
	for _, r := range rows {
		k := key{r.Account, r.Currency}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}

	out := make([]nameStatusDTO, 0, len(order))
	for _, k := range order {
		res := importer.ResolveAccount(k.name, k.currency, existing, nil)
		out = append(out, nameStatusDTO{
			Key:  importer.AccountKey(k.name, k.currency),
			Name: k.name, Currency: k.currency, Resolved: res.Resolved(), ID: res.ID,
			CurrencyMismatch: res.CurrencyMismatch, Count: counts[k],
		})
	}
	return out
}

// currencyCountDTO is one distinct currency in the CSV and how many rows use it.
//
// A plain fact about the file — the server does not decide which of these are
// "missing", because whether a currency is declared is a synced per-user
// setting and a rate is a client-side cache. The import screen cross-references
// both from its own replica, with no extra request.
type currencyCountDTO struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// summariseCurrencies is what makes an import's currencies visible at all.
// Rows in a currency the instance has no rate for import perfectly well and
// then sit outside every total, captioned "no exchange rate yet", with nothing
// to connect that to the file that was just imported.
func summariseCurrencies(rows []importer.Row) []currencyCountDTO {
	var order []string
	counts := map[string]int{}
	for _, r := range rows {
		if counts[r.Currency] == 0 {
			order = append(order, r.Currency)
		}
		counts[r.Currency]++
	}
	out := make([]currencyCountDTO, 0, len(order))
	for _, code := range order {
		out = append(out, currencyCountDTO{Code: code, Count: counts[code]})
	}
	return out
}

// --- preview ---------------------------------------------------------------------

type importPreviewRequest struct {
	CSV string `json:"csv"`
}

type importPreviewResponse struct {
	TotalRows   int                `json:"totalRows"`
	ParseErrors []parseErrorDTO    `json:"parseErrors"`
	Categories  []nameStatusDTO    `json:"categories"`
	Accounts    []nameStatusDTO    `json:"accounts"`
	Currencies  []currencyCountDTO `json:"currencies"`
	Groups      []groupCountDTO    `json:"groups"`
}

// groupCountDTO is how many rows use one (category, account) pair, keyed the
// same way the mapping is. See summariseGroups.
type groupCountDTO struct {
	CategoryKey string `json:"categoryKey"`
	AccountKey  string `json:"accountKey"`
	Count       int    `json:"count"`
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
		Currencies:  summariseCurrencies(res.Rows),
		Groups:      summariseGroups(res.Rows),
	})
}

// summariseGroups counts rows per (category, account) pair, so the screen can
// say exactly how many rows a given mapping decision covers.
//
// Needed because the two counts cannot simply be added: a row blocked by both
// an unmapped category *and* an unmapped account would be counted twice, and
// the screen would claim to skip more rows than the file contains. With the
// pairs, the client takes a union. Bounded by distinct pairs, not rows — a
// 1683-row export with 20 categories over 3 accounts is at most 60 entries.
func summariseGroups(rows []importer.Row) []groupCountDTO {
	type key struct{ category, account string }
	var order []key
	counts := map[key]int{}
	for _, r := range rows {
		k := key{importer.CategoryKey(r.Category, r.Kind), importer.AccountKey(r.Account, r.Currency)}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	out := make([]groupCountDTO, 0, len(order))
	for _, k := range order {
		out = append(out, groupCountDTO{
			CategoryKey: k.category, AccountKey: k.account, Count: counts[k],
		})
	}
	return out
}

// ownedOnly drops mapping entries that name a row this account does not have.
// See the call site for why an unknown id must not be trusted.
func ownedOnly(mapping map[string]string, existing []importer.NamedRow) map[string]string {
	if len(mapping) == 0 {
		return mapping
	}
	owned := make(map[string]bool, len(existing))
	for _, row := range existing {
		owned[row.ID] = true
	}
	out := make(map[string]string, len(mapping))
	for key, id := range mapping {
		if owned[id] {
			out[key] = id
		}
	}
	return out
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
	// Rows that were resolved and then could not be written, because a chunk
	// after the first failed. Zero on every ordinary import.
	Failed        int    `json:"failed"`
	FailureReason string `json:"failureReason,omitempty"`
}

// applyInChunks writes ops in batches ApplyOps will accept, and reports what
// landed even when a later batch fails.
//
// ApplyOps rejects anything over syncproto.MaxOpsPerPush outright, and an
// import is routinely thousands of rows in one request. Chunking is the answer,
// but it trades one atomic transaction for N independent ones — so a failure
// half way through leaves the earlier chunks committed. Returning only the
// error would throw away the count of what was already written, and the caller
// would have to report a total failure over a partial success.
//
// A free function rather than a method so it can be tested against a stub
// without standing up a database.
func applyInChunks(
	apply func([]syncproto.Op) (db.ApplyResult, error),
	ops []syncproto.Op,
) (accepted int, lastSeq int64, err error) {
	for chunk := range slices.Chunk(ops, syncproto.MaxOpsPerPush) {
		result, chunkErr := apply(chunk)
		if chunkErr != nil {
			return accepted, lastSeq, chunkErr
		}
		accepted += result.Accepted
		lastSeq = result.ServerSeq
	}
	return accepted, lastSeq, nil
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

	// A mapping may only point at a row this account actually owns.
	//
	// Nothing else checks it: the resolvers take an override id on trust, so a
	// client that mapped to an id the server has never seen — one still sitting
	// in its own outbox, or a stale id from a previous replica — had every one
	// of those rows written pointing at nothing. Thousands of transactions
	// belonging to no category, which is worse than the rows being skipped and
	// invisible until someone opens the dashboard and finds a blank slice.
	//
	// Dropping the bad entries rather than failing the request keeps the rest of
	// the import working, and buildImportOps then reports the affected rows as
	// unresolved with a reason, which is the contract every other unresolved row
	// already follows.
	categoryMap := ownedOnly(in.CategoryMap, existingCategories)
	accountMap := ownedOnly(in.AccountMap, existingAccounts)

	ops, alreadyImported, unresolved, err := buildImportOps(
		res.Rows, existingCategories, existingAccounts, categoryMap, accountMap, existingKeys)
	if err != nil {
		return err
	}

	accepted, lastSeq, applyErr := applyInChunks(
		func(chunk []syncproto.Op) (db.ApplyResult, error) {
			return s.conn().ApplyOps(user.ID, chunk)
		}, ops)

	// Every one of these transactions is new to every device, including
	// whichever one is running the import — unlike an ordinary push, there
	// is no originating device already holding the rows locally to skip.
	//
	// Published before the error is considered, because rows that were written
	// are written whether or not a later chunk failed, and the other devices
	// should not have to wait for their next poll to find out.
	if accepted > 0 {
		s.events.publish(user.ID, syncEvent{Seq: lastSeq, DeviceID: serverDeviceID})
	}

	out := importCommitResponse{
		Imported:        accepted,
		AlreadyImported: alreadyImported,
		ParseErrors:     toParseErrorDTOs(res.Errors),
		Unresolved:      unresolved,
	}
	if applyErr != nil {
		// Deliberately a 200 carrying the damage, not a 500. The central
		// errorHandler renders only {"error": …}, which would discard the
		// count of what already landed — and "the import failed" is a lie
		// about a database that now holds several thousand new rows. Re-running
		// the same file is safe: buildImportOps de-duplicates on natural keys,
		// so the rows that made it are skipped the second time.
		out.Failed = len(ops) - accepted
		out.FailureReason = applyErr.Error()
		slog.ErrorContext(c.Context(), "import: applying ops",
			"user", user.ID, "accepted", accepted, "failed", out.Failed, "error", applyErr)
	}
	return c.JSON(out)
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
