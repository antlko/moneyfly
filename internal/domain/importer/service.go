package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// MaxUploadBytes is the default cap on an upload. The real export is 75 KB, so
// this is three orders of magnitude of headroom and still refuses a file that
// would exhaust memory.
const MaxUploadBytes int64 = 8 << 20

// CategoryPort is the slice of the category service the importer needs. It
// resolves and creates aliases; it never creates categories.
type CategoryPort interface {
	List(ctx context.Context, userID int64, kind category.Kind, includeArchived bool) ([]category.Category, error)
	Get(ctx context.Context, userID, id int64) (category.Category, error)
	ResolveAlias(ctx context.Context, userID int64, source, sourceName string) (*category.Category, error)
	CreateAlias(ctx context.Context, userID, categoryID int64, source, sourceName string) (category.Alias, error)
}

// AccountPort is the slice of the account service the importer needs.
type AccountPort interface {
	List(ctx context.Context, userID int64, includeArchived bool) ([]account.Account, error)
	ResolveAlias(ctx context.Context, userID int64, source, sourceName string) (*account.Account, error)
	CreateAlias(ctx context.Context, userID, accountID int64, source, sourceName string) (account.Alias, error)
}

// ExponentSource supplies the currency exponents the parser scales amounts with.
type ExponentSource interface {
	Exponents(ctx context.Context) (map[string]int, error)
}

// SourceFactory builds the parser for one request.
//
// It is a factory rather than a value because the exponents come from the
// currency table: at process start the database may not even be migrated yet,
// and HUF being 0-decimal is not something to guess (conventions §2).
type SourceFactory func(ctx context.Context) (Source, error)

// MonefyFactory builds the Monefy parser from the currency table.
func MonefyFactory(currencies ExponentSource) SourceFactory {
	return func(ctx context.Context) (Source, error) {
		exponents, err := currencies.Exponents(ctx)
		if err != nil {
			return nil, err
		}
		return NewMonefySource(exponents), nil
	}
}

// service implements Service.
type service struct {
	repo       Repo
	files      FileStore
	newSource  SourceFactory
	categories CategoryPort
	accounts   AccountPort
	fx         transaction.FXConverter
	clock      clock.Clock

	baseCurrency string
	maxBytes     int64
}

// NewService builds the import service.
//
// The source is injected rather than selected here so stage 08's bot and a future
// non-Monefy format reuse the identical pipeline.
func NewService(
	repo Repo,
	files FileStore,
	src SourceFactory,
	categories CategoryPort,
	accounts AccountPort,
	fxConv transaction.FXConverter,
	clk clock.Clock,
	baseCurrency string,
	maxBytes int64,
) Service {
	if maxBytes <= 0 {
		maxBytes = MaxUploadBytes
	}
	return &service{
		repo: repo, files: files, newSource: src, categories: categories, accounts: accounts,
		fx: fxConv, clock: clk,
		baseCurrency: strings.ToUpper(baseCurrency), maxBytes: maxBytes,
	}
}

// Receive stores the file, analyses it and records the outcome. It never writes
// a transaction.
func (s *service) Receive(ctx context.Context, userID int64, origin Origin, filename string, r io.Reader) (*Batch, error) {
	if !origin.Valid() {
		return nil, apperr.Validation("origin", "must be one of web|telegram|cli, got %q", origin)
	}
	// One byte past the cap is read so an oversized file is refused rather than
	// silently truncated.
	data, err := io.ReadAll(io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("importer: reading upload: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return nil, apperr.TooLargef("the file exceeds the %d byte upload limit", s.maxBytes)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, apperr.Validation("file", "the file is empty")
	}

	src, err := s.newSource(ctx)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])

	// An identical file that was already committed is reported, not reprocessed.
	if existing, err := s.repo.FindCommittedByChecksum(ctx, userID, src.Key(), checksum); err != nil {
		return nil, err
	} else if existing != nil {
		existing.AlreadyImported = true
		return existing, nil
	}

	path, err := s.files.Put(userID, checksum, data)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	batch, err := s.repo.CreateBatch(ctx, Batch{
		UserID:     userID,
		Source:     src.Key(),
		Origin:     origin,
		Filename:   filename,
		FileSHA256: checksum,
		StoredPath: path,
		Status:     StatusReceived,
	}, now)
	if err != nil {
		return nil, err
	}

	if err := s.analyseAndStore(ctx, userID, &batch); err != nil {
		batch.Status = StatusFailed
		batch.Error = err.Error()
		if uerr := s.repo.UpdateBatch(ctx, batch); uerr != nil {
			return nil, uerr
		}
		return nil, err
	}
	return &batch, nil
}

// Preview re-reads the stored analysis and adds the derived figures.
func (s *service) Preview(ctx context.Context, userID, batchID int64) (*Preview, error) {
	batch, err := s.repo.GetBatch(ctx, userID, batchID)
	if err != nil {
		return nil, err
	}
	return s.previewFrom(ctx, userID, batch)
}

// ApplyMappings records the decisions as aliases and re-runs the analysis from
// the retained file, which is what turns needs_mapping back into previewed.
func (s *service) ApplyMappings(ctx context.Context, userID, batchID int64, m Mappings) (*Preview, error) {
	batch, err := s.repo.GetBatch(ctx, userID, batchID)
	if err != nil {
		return nil, err
	}
	switch batch.Status {
	case StatusParsed, StatusNeedsMapping, StatusPreviewed:
	default:
		return nil, apperr.Conflictf("a %s batch cannot be remapped", batch.Status)
	}
	if len(m.Categories) == 0 && len(m.Accounts) == 0 {
		return nil, apperr.Validation("mappings", "supply at least one category or account mapping")
	}

	for _, mapping := range m.Categories {
		if strings.TrimSpace(mapping.SourceName) == "" {
			return nil, apperr.Validation("categories.source_name", "must not be empty")
		}
		if _, err := s.categories.CreateAlias(ctx, userID, mapping.TargetID, batch.Source, mapping.SourceName); err != nil {
			return nil, err
		}
	}
	for _, mapping := range m.Accounts {
		if strings.TrimSpace(mapping.SourceName) == "" {
			return nil, apperr.Validation("accounts.source_name", "must not be empty")
		}
		if _, err := s.accounts.CreateAlias(ctx, userID, mapping.TargetID, batch.Source, mapping.SourceName); err != nil {
			return nil, err
		}
	}

	return s.previewFrom(ctx, userID, batch)
}

// Commit writes the batch. The analysis is re-derived from the retained file so
// dedup reflects the database as it is now, not as it was at upload.
func (s *service) Commit(ctx context.Context, userID, batchID int64) (*Batch, error) {
	batch, err := s.repo.GetBatch(ctx, userID, batchID)
	if err != nil {
		return nil, err
	}
	switch batch.Status {
	case StatusCommitted:
		return nil, apperr.Conflictf("batch %d is already committed", batchID)
	case StatusPreviewed, StatusParsed:
	default:
		return nil, apperr.Conflictf("a %s batch cannot be committed", batch.Status)
	}

	result, err := s.analyse(ctx, userID, batch)
	if err != nil {
		return nil, err
	}
	if err := s.storeAnalysis(ctx, userID, &batch, result); err != nil {
		return nil, err
	}
	// The guarantee, enforced here rather than in the UI: nothing is stored
	// while a single source name is unresolved.
	if result.counts.Unmapped > 0 {
		return nil, apperr.Conflictf(
			"%d row(s) reference %d unmapped name(s); map them before committing",
			result.counts.Unmapped, len(result.unmappedCategories)+len(result.unmappedAccounts))
	}

	entries, err := s.buildEntries(ctx, userID, result)
	if err != nil {
		return nil, err
	}

	committed, err := s.repo.Commit(ctx, userID, batchID, entries, result.counts, s.clock.Now())
	if err != nil {
		return nil, err
	}
	return &committed, nil
}

// Revert soft-deletes exactly the rows this batch produced.
func (s *service) Revert(ctx context.Context, userID, batchID int64) (*Batch, error) {
	batch, err := s.repo.GetBatch(ctx, userID, batchID)
	if err != nil {
		return nil, err
	}
	if batch.Status != StatusCommitted {
		return nil, apperr.Conflictf("only a committed batch can be reverted; batch %d is %s", batchID, batch.Status)
	}
	reverted, err := s.repo.Revert(ctx, userID, batchID, s.clock.Now())
	if err != nil {
		return nil, err
	}
	return &reverted, nil
}

// List returns the batch history.
func (s *service) List(ctx context.Context, userID int64, limit int) ([]Batch, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.ListBatches(ctx, userID, limit)
}

// Get returns one batch.
func (s *service) Get(ctx context.Context, userID, batchID int64) (*Batch, error) {
	b, err := s.repo.GetBatch(ctx, userID, batchID)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// Rows returns the batch's rows, optionally filtered by status.
func (s *service) Rows(ctx context.Context, userID, batchID int64, status RowStatus, limit int) ([]Row, error) {
	if status != "" && !status.Valid() {
		return nil, apperr.Validation("status", "must be one of new|duplicate|unmapped|rejected|committed, got %q", status)
	}
	if _, err := s.repo.GetBatch(ctx, userID, batchID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	return s.repo.ListRows(ctx, userID, batchID, status, limit)
}

// analysis is one pass over the file: parse, resolve, dedup, reconcile.
type analysis struct {
	rows      []Row
	resolved  []resolved
	counts    Counts
	dateRange *DateRange
	months    int

	byCurrency         map[string]int
	unmappedCategories []UnmappedName
	unmappedAccounts   []UnmappedName
	vanished           []VanishedRow
	warnings           []string
}

func (s *service) analyseAndStore(ctx context.Context, userID int64, batch *Batch) error {
	result, err := s.analyse(ctx, userID, *batch)
	if err != nil {
		return err
	}
	return s.storeAnalysis(ctx, userID, batch, result)
}

func (s *service) storeAnalysis(ctx context.Context, userID int64, batch *Batch, result *analysis) error {
	if err := s.repo.ReplaceRows(ctx, userID, batch.ID, result.rows); err != nil {
		return err
	}
	batch.RowsTotal = result.counts.Total
	batch.RowsNew = result.counts.New
	batch.RowsDuplicate = result.counts.Duplicate
	batch.RowsUnmapped = result.counts.Unmapped
	batch.RowsRejected = result.counts.Rejected
	batch.Error = ""
	if result.counts.Unmapped > 0 {
		batch.Status = StatusNeedsMapping
	} else {
		batch.Status = StatusPreviewed
	}
	return s.repo.UpdateBatch(ctx, *batch)
}

// Splitting it would hide the order these steps depend on.
//
//nolint:gocyclo // One pass over the file: parse, resolve, dedup, reconcile.
func (s *service) analyse(ctx context.Context, userID int64, batch Batch) (*analysis, error) {
	file, err := s.files.Open(batch.StoredPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	src, err := s.newSource(ctx)
	if err != nil {
		return nil, err
	}
	raw, parseErrs, err := src.Parse(file)
	if err != nil {
		return nil, apperr.Validation("file", "%v", err)
	}

	out := &analysis{byCurrency: map[string]int{}}

	for _, pe := range parseErrs {
		out.rows = append(out.rows, Row{
			BatchID: batch.ID, LineNo: pe.LineNo, Raw: pe.Raw,
			Status: RowRejected, Reason: pe.Reason,
		})
	}

	// Resolution is memoised: the real export has 1,683 rows across 19 category
	// names, so this is 19 lookups rather than 1,683.
	categoryCache := map[string]*category.Category{}
	accountCache := map[string]*account.Account{}
	unmappedCat := map[string]*UnmappedName{}
	unmappedAcct := map[string]*UnmappedName{}

	// Occurrence within this file, per natural key (§4.5).
	seen := map[string]int{}
	keys := make([]string, 0, len(raw))
	type pending struct {
		row  RawRow
		key  string
		occ  int
		cat  *category.Category
		acct *account.Account
		bad  string // non-empty when the row cannot be committed as mapped
	}
	pendings := make([]pending, 0, len(raw))

	convertedCurrencies := map[string]bool{}
	incomeRows := 0

	for _, r := range raw {
		out.byCurrency[r.Currency]++
		if r.ConvertedCurrency != "" && r.ConvertedCurrency != r.Currency {
			convertedCurrencies[r.ConvertedCurrency] = true
		}
		if r.Kind == transaction.KindIncome {
			incomeRows++
		}

		key := transaction.NaturalKey(r.Date, r.AccountName, r.CategoryName,
			r.AmountMinor, r.Currency, r.Description)
		seen[key]++
		keys = append(keys, key)

		p := pending{row: r, key: key, occ: seen[key]}

		acct, ok := accountCache[r.AccountName]
		if !ok {
			acct, err = s.accounts.ResolveAlias(ctx, userID, batch.Source, r.AccountName)
			if err != nil {
				return nil, err
			}
			accountCache[r.AccountName] = acct
		}
		cat, ok := categoryCache[r.CategoryName]
		if !ok {
			cat, err = s.categories.ResolveAlias(ctx, userID, batch.Source, r.CategoryName)
			if err != nil {
				return nil, err
			}
			categoryCache[r.CategoryName] = cat
		}

		switch {
		case acct == nil:
			p.bad = "account name is not mapped"
			track(unmappedAcct, r.AccountName, "")
		case cat == nil:
			p.bad = "category name is not mapped"
			track(unmappedCat, r.CategoryName, "")
		default:
			p.acct, p.cat = acct, cat
			if reason := checkMapping(r, *acct, *cat); reason != "" {
				p.bad = reason
				track(unmappedCat, r.CategoryName, reason)
			}
		}
		pendings = append(pendings, p)
	}

	stored, err := s.repo.MaxOccurrences(ctx, userID, keys)
	if err != nil {
		return nil, err
	}

	inFile := map[occurrenceKey]bool{}
	for _, p := range pendings {
		row := Row{
			BatchID: batch.ID, LineNo: p.row.LineNo, Raw: p.row.Raw,
			AccountName: p.row.AccountName, CategoryName: p.row.CategoryName,
			Currency: p.row.Currency,
		}
		day := p.row.Date
		row.Date = &day
		amount := p.row.AmountMinor
		row.AmountMinor = &amount
		if p.row.Description != "" {
			desc := p.row.Description
			row.Description = &desc
		}
		inFile[occurrenceKey{key: p.key, occurrence: p.occ}] = true

		switch {
		case p.bad != "":
			row.Status = RowUnmapped
			row.Reason = p.bad
		case p.occ <= stored[p.key]:
			row.Status = RowDuplicate
			row.Reason = "already recorded"
		default:
			row.Status = RowNew
			out.resolved = append(out.resolved, resolved{
				LineNo: p.row.LineNo, AccountID: p.acct.ID, CategoryID: p.cat.ID,
				Row: p.row, NaturalKey: p.key, Occurrence: p.occ,
			})
		}
		out.rows = append(out.rows, row)
	}

	sort.Slice(out.rows, func(i, j int) bool { return out.rows[i].LineNo < out.rows[j].LineNo })

	for _, row := range out.rows {
		out.counts.Total++
		switch row.Status {
		case RowNew:
			out.counts.New++
		case RowDuplicate:
			out.counts.Duplicate++
		case RowUnmapped:
			out.counts.Unmapped++
		case RowRejected:
			out.counts.Rejected++
		}
	}

	out.dateRange, out.months = spanOf(raw)
	out.unmappedCategories = s.suggestCategories(ctx, userID, unmappedCat)
	out.unmappedAccounts = s.suggestAccounts(ctx, userID, unmappedAcct)

	if out.dateRange != nil {
		out.vanished, err = s.reconcile(ctx, userID, batch.Source, *out.dateRange, inFile)
		if err != nil {
			return nil, err
		}
	}

	if incomeRows == 0 && len(raw) > 0 {
		out.warnings = append(out.warnings,
			"No income rows found. Monefy does not export income; enter it manually.")
	}
	for c := range convertedCurrencies {
		out.warnings = append(out.warnings, fmt.Sprintf(
			"The converted-amount column is in %s, which is Monefy's base at export time; "+
				"native amounts are used instead.", c))
	}
	sort.Strings(out.warnings)
	if len(out.vanished) > 0 {
		out.warnings = append(out.warnings, fmt.Sprintf(
			"%d stored row(s) inside this file's date range are missing from it. "+
				"They are flagged, never deleted.", len(out.vanished)))
	}
	return out, nil
}

type occurrenceKey struct {
	key        string
	occurrence int
}

// reconcile finds stored rows inside the file's range that the file no longer
// contains. Scoping to the range is what stops a partial export implying that
// everything outside it was deleted.
func (s *service) reconcile(
	ctx context.Context, userID int64, source string, span DateRange, inFile map[occurrenceKey]bool,
) ([]VanishedRow, error) {
	stored, err := s.repo.ImportedInRange(ctx, userID, source, span.From, span.To)
	if err != nil {
		return nil, err
	}
	var out []VanishedRow
	for _, row := range stored {
		if inFile[occurrenceKey{key: row.NaturalKey, occurrence: row.Occurrence}] {
			continue
		}
		out = append(out, VanishedRow{
			TransactionID: row.TransactionID, OccurredOn: row.OccurredOn,
			AccountName: row.AccountName, CategoryName: row.CategoryName,
			AmountMinor: row.AmountMinor, Currency: row.Currency, Description: row.Description,
		})
	}
	return out, nil
}

// buildEntries converts the resolved rows into transactions, computing the base
// amount at each row's own date. A missing rate leaves base NULL rather than
// blocking ingestion (§4.4).
func (s *service) buildEntries(ctx context.Context, userID int64, result *analysis) ([]CommitEntry, error) {
	entries := make([]CommitEntry, 0, len(result.resolved))
	for _, r := range result.resolved {
		amount := money.New(r.Row.AmountMinor, r.Row.Currency)
		t := transaction.Transaction{
			UserID:     userID,
			AccountID:  r.AccountID,
			CategoryID: &r.CategoryID,
			OccurredOn: r.Row.Date,
			Kind:       r.Row.Kind,
			Amount:     amount,
			NaturalKey: r.NaturalKey,
			Occurrence: r.Occurrence,
		}
		if desc := strings.TrimSpace(r.Row.Description); desc != "" {
			t.Description = &desc
		}

		converted, rateID, err := s.fx.Convert(ctx, amount, s.baseCurrency, r.Row.Date)
		if err != nil {
			return nil, err
		}
		// No rate for this date, or none at all: the row is stored with a NULL
		// base amount. Losing the transaction would be worse than not knowing its
		// euro value yet.
		if rateID != nil || strings.EqualFold(amount.Currency, s.baseCurrency) {
			t.BaseAmount = &converted
			t.FxRateID = rateID
		}
		entries = append(entries, CommitEntry{LineNo: r.LineNo, Transaction: t})
	}
	return entries, nil
}

func (s *service) previewFrom(ctx context.Context, userID int64, batch Batch) (*Preview, error) {
	// Preview is rebuilt from the file rather than from the stored rows so the
	// suggestions, the reconcile and the counts are always consistent with each
	// other and with the database as it is now.
	result, err := s.analyse(ctx, userID, batch)
	if err != nil {
		return nil, err
	}
	if batch.Status.Terminal() {
		// A committed batch's counts are a record of what it did, not a question
		// to be re-asked: its own rows are in the database now, so re-deriving
		// would report every one of them as a duplicate of itself. The date
		// range, months and warnings still come from the file, because those are
		// properties of the file rather than of the ledger.
		result.unmappedCategories, result.unmappedAccounts, result.vanished = nil, nil, nil
	} else if err := s.storeAnalysis(ctx, userID, &batch, result); err != nil {
		return nil, err
	}

	var rejected []Row
	for _, row := range result.rows {
		if row.Status == RowRejected {
			rejected = append(rejected, row)
		}
	}

	return &Preview{
		Batch:              batch,
		DateRange:          result.dateRange,
		MonthsTouched:      result.months,
		ByCurrency:         result.byCurrency,
		UnmappedCategories: result.unmappedCategories,
		UnmappedAccounts:   result.unmappedAccounts,
		Vanished:           result.vanished,
		Rejected:           rejected,
		Warnings:           result.warnings,
	}, nil
}

// checkMapping refuses a mapping that would produce a transaction the domain
// would reject anyway. Reporting it as unmapped keeps commit blocked, which is
// the point: a mismatch must be decided, not dropped.
func checkMapping(r RawRow, acct account.Account, cat category.Category) string {
	if !strings.EqualFold(acct.Currency, r.Currency) {
		return fmt.Sprintf("account %q holds %s but the row is in %s", acct.Name, acct.Currency, r.Currency)
	}
	if acct.ArchivedAt != nil {
		return fmt.Sprintf("account %q is archived", acct.Name)
	}
	if cat.ArchivedAt != nil {
		return fmt.Sprintf("category %q is archived", cat.Name)
	}
	switch r.Kind {
	case transaction.KindExpense:
		if cat.Kind != category.KindExpense {
			return fmt.Sprintf("%q is an income category and cannot hold an expense", cat.Name)
		}
	case transaction.KindIncome:
		if cat.Kind != category.KindIncome {
			return fmt.Sprintf("%q is an expense category and cannot hold income", cat.Name)
		}
	}
	return ""
}

func track(into map[string]*UnmappedName, name, reason string) {
	entry, ok := into[name]
	if !ok {
		entry = &UnmappedName{SourceName: name, Reason: reason}
		into[name] = entry
	}
	if entry.Reason == "" {
		entry.Reason = reason
	}
	entry.RowCount++
}

func (s *service) suggestCategories(ctx context.Context, userID int64, names map[string]*UnmappedName) []UnmappedName {
	if len(names) == 0 {
		return nil
	}
	cats, err := s.categories.List(ctx, userID, "", false)
	if err != nil {
		// A failed suggestion lookup must not fail the import: the names are
		// still reported, just without a proposal.
		cats = nil
	}
	candidates := make([]candidate, 0, len(cats))
	for _, c := range cats {
		candidates = append(candidates, candidate{ID: c.ID, Name: c.Name})
	}
	return withSuggestions(names, candidates)
}

func (s *service) suggestAccounts(ctx context.Context, userID int64, names map[string]*UnmappedName) []UnmappedName {
	if len(names) == 0 {
		return nil
	}
	accts, err := s.accounts.List(ctx, userID, false)
	if err != nil {
		accts = nil
	}
	candidates := make([]candidate, 0, len(accts))
	for _, a := range accts {
		if a.HasChildren {
			// A computed parent cannot hold a transaction, so proposing it would
			// only produce a validation error later.
			continue
		}
		candidates = append(candidates, candidate{ID: a.ID, Name: a.Name})
	}
	return withSuggestions(names, candidates)
}

func withSuggestions(names map[string]*UnmappedName, candidates []candidate) []UnmappedName {
	out := make([]UnmappedName, 0, len(names))
	for _, entry := range names {
		item := *entry
		item.Suggestion = suggest(item.SourceName, candidates)
		out = append(out, item)
	}
	// Most rows first: the expensive decisions are the ones worth making first.
	sort.Slice(out, func(i, j int) bool {
		if out[i].RowCount != out[j].RowCount {
			return out[i].RowCount > out[j].RowCount
		}
		return out[i].SourceName < out[j].SourceName
	})
	return out
}

// spanOf returns the file's date range and the number of distinct months it
// touches. Both are nil/zero for a file with no parseable rows.
func spanOf(raw []RawRow) (*DateRange, int) {
	if len(raw) == 0 {
		return nil, 0
	}
	from, to := raw[0].Date, raw[0].Date
	months := map[period.Period]bool{}
	for _, r := range raw {
		if r.Date.Before(from) {
			from = r.Date
		}
		if r.Date.After(to) {
			to = r.Date
		}
		months[period.FromTime(r.Date)] = true
	}
	return &DateRange{From: from, To: to}, len(months)
}
