package store

import (
	"errors"
	"os"
	"testing"

	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/money"
)

// fixtureDir holds the real export and the targeted fixtures from
// docs/04-import-monefy.md §4.14.
const fixtureDir = "../../testdata/"

// importService wires the import pipeline over this harness, retaining uploads in
// a directory that disappears with the test.
func (h *harness) importService() importer.Service {
	h.t.Helper()
	return importer.NewService(
		NewImportRepo(h.db),
		importer.NewDirFileStore(h.t.TempDir()),
		importer.MonefyFactory(h.Currencies),
		h.categoryService(),
		h.accountService(),
		h.fxService(),
		h.clock,
		"EUR",
		0,
	)
}

func (h *harness) receive(svc importer.Service, userID int64, fixture string) *importer.Batch {
	h.t.Helper()
	f, err := os.Open(fixtureDir + fixture)
	if err != nil {
		h.t.Fatalf("opening %s: %v", fixture, err)
	}
	defer func() { _ = f.Close() }()

	batch, err := svc.Receive(h.ctx, userID, importer.OriginWeb, fixture, f)
	if err != nil {
		h.t.Fatalf("Receive(%s): %v", fixture, err)
	}
	return batch
}

// liveRows counts the user's undeleted transactions.
func (h *harness) liveRows(userID int64) int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(
		`SELECT count(*) FROM transaction_entry WHERE user_id = ? AND deleted_at IS NULL`,
		userID).Scan(&n); err != nil {
		h.t.Fatalf("counting transactions: %v", err)
	}
	return n
}

// countsByCategory returns live row counts keyed by canonical category name.
func (h *harness) countsByCategory(userID int64) map[string]int {
	h.t.Helper()
	rows, err := h.db.Query(`
		SELECT c.name, count(*)
		FROM transaction_entry t
		JOIN category c ON c.id = t.category_id AND c.user_id = t.user_id
		WHERE t.user_id = ? AND t.deleted_at IS NULL
		GROUP BY c.name`, userID)
	if err != nil {
		h.t.Fatalf("counting by category: %v", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int{}
	for rows.Next() {
		var (
			name string
			n    int
		)
		if err := rows.Scan(&name, &n); err != nil {
			h.t.Fatalf("scanning category count: %v", err)
		}
		out[name] = n
	}
	if err := rows.Err(); err != nil {
		h.t.Fatalf("reading category counts: %v", err)
	}
	return out
}

// The gate. A run producing 1,446 is the 14% silent loss regressing.

func TestImport_RealExport_All1683Rows(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "monefy-real-1683.csv")
	if batch.RowsTotal != 1683 {
		t.Fatalf("rows_total = %d, want 1683", batch.RowsTotal)
	}
	if batch.RowsNew != 1683 {
		t.Fatalf("rows_new = %d, want 1683", batch.RowsNew)
	}
	if batch.RowsRejected != 0 {
		t.Fatalf("rows_rejected = %d, want 0", batch.RowsRejected)
	}
	if batch.Status != importer.StatusPreviewed {
		t.Fatalf("status = %q, want previewed", batch.Status)
	}

	preview, err := svc.Preview(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if got := preview.DateRange.From.Format(transaction.DateLayout); got != "2021-07-19" {
		t.Fatalf("date range from = %s, want 2021-07-19", got)
	}
	if got := preview.DateRange.To.Format(transaction.DateLayout); got != "2023-12-03" {
		t.Fatalf("date range to = %s, want 2023-12-03", got)
	}
	// 28, not the 29 the spec first claimed: 2022-08 and 2022-09 have no rows.
	if preview.MonthsTouched != 28 {
		t.Fatalf("months_touched = %d, want 28", preview.MonthsTouched)
	}
	if want := map[string]int{"UAH": 1681, "EUR": 1, "HUF": 1}; !sameCounts(preview.ByCurrency, want) {
		t.Fatalf("by_currency = %v, want %v", preview.ByCurrency, want)
	}

	committed, err := svc.Commit(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if committed.Status != importer.StatusCommitted {
		t.Fatalf("status = %q, want committed", committed.Status)
	}
	if got := h.liveRows(u.ID); got != 1683 {
		t.Fatalf("stored transactions = %d, want exactly 1683", got)
	}
}

func TestImport_RealExport_NoUnmapped(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "monefy-real-1683.csv")
	if batch.RowsUnmapped != 0 {
		preview, _ := svc.Preview(h.ctx, u.ID, batch.ID)
		t.Fatalf("rows_unmapped = %d, want 0 with stage 02's seeded aliases; unmapped: %+v / %+v",
			batch.RowsUnmapped, preview.UnmappedCategories, preview.UnmappedAccounts)
	}
}

func TestImport_RealExport_CategoryTotals(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "monefy-real-1683.csv")
	if _, err := svc.Commit(h.ctx, u.ID, batch.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// The source names on the right are what the file actually contains; the
	// bolded ones in the stage file are the seven the old pipeline discarded.
	want := map[string]int{
		"Food": 431, "Eating out": 363, "Entertainment": 275, "Transport": 121,
		"Utilities": 119, "Gifts": 106, "Hotel/Trip": 58, "Toiletry": 52,
		"Health": 30, "House": 28, "Communications": 25, "Hobby": 20,
		"Clothes": 19, "Studying": 12, "Appliances": 12, "Family": 7,
		"Sport": 3, "Taxi": 1, "Services": 1,
	}
	got := h.countsByCategory(u.ID)
	for name, n := range want {
		if got[name] != n {
			t.Errorf("category %q has %d rows, want %d", name, got[name], n)
		}
	}
	for name, n := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected category %q with %d rows", name, n)
		}
	}
}

// Behaviour.

func TestImport_Reimport_ZeroNew(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	first := h.receive(svc, u.ID, "monefy-real-1683.csv")
	if _, err := svc.Commit(h.ctx, u.ID, first.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// A fresh export of the same history: identical rows, different bytes (one
	// extra blank line), so the sha256 short-circuit does not stand in for the
	// dedup this test is about. This is the realistic monthly case.
	data, err := os.ReadFile(fixtureDir + "monefy-real-1683.csv")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	copyPath := t.TempDir() + "/again.csv"
	if err := os.WriteFile(copyPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("writing copy: %v", err)
	}
	f, err := os.Open(copyPath)
	if err != nil {
		t.Fatalf("opening copy: %v", err)
	}
	defer func() { _ = f.Close() }()

	second, err := svc.Receive(h.ctx, u.ID, importer.OriginWeb, "again.csv", f)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if second.AlreadyImported {
		t.Fatal("a different file must be analysed, not short-circuited on its checksum")
	}
	if second.RowsNew != 0 || second.RowsDuplicate != 1683 {
		t.Fatalf("second run = %d new / %d duplicate, want 0 / 1683",
			second.RowsNew, second.RowsDuplicate)
	}
	if got := h.liveRows(u.ID); got != 1683 {
		t.Fatalf("stored transactions = %d, want 1683 after a re-import", got)
	}
}

func TestImport_ThreeIdenticalRows(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "identical-rows.csv")
	if batch.RowsNew != 3 {
		t.Fatalf("rows_new = %d, want 3: three coffees are three transactions", batch.RowsNew)
	}
	if _, err := svc.Commit(h.ctx, u.ID, batch.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	rows, err := h.db.Query(`
		SELECT occurrence FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL ORDER BY occurrence`, u.ID)
	if err != nil {
		t.Fatalf("reading occurrences: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scanning occurrence: %v", err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading occurrences: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("occurrences = %v, want [1 2 3]", got)
	}
}

func TestImport_ThirdCoffeeAdded(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	// Two identical rows, committed.
	first := h.receive(svc, u.ID, "identical-rows-2.csv")
	if _, err := svc.Commit(h.ctx, u.ID, first.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// The next export has three: one genuinely new, two already recorded.
	second := h.receive(svc, u.ID, "identical-rows.csv")
	if second.RowsNew != 1 || second.RowsDuplicate != 2 {
		t.Fatalf("second run = %d new / %d duplicate, want 1 / 2",
			second.RowsNew, second.RowsDuplicate)
	}
	if _, err := svc.Commit(h.ctx, u.ID, second.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := h.liveRows(u.ID); got != 3 {
		t.Fatalf("stored = %d, want 3", got)
	}
}

func TestImport_UnknownCategory_BlocksBatch(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "unknown-category.csv")
	if batch.Status != importer.StatusNeedsMapping {
		t.Fatalf("status = %q, want needs_mapping", batch.Status)
	}
	if batch.RowsUnmapped != 2 {
		t.Fatalf("rows_unmapped = %d, want 2", batch.RowsUnmapped)
	}
	if got := h.liveRows(u.ID); got != 0 {
		t.Fatalf("stored = %d, want 0: an unmapped name stores nothing", got)
	}

	preview, err := svc.Preview(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(preview.UnmappedCategories) != 1 || preview.UnmappedCategories[0].SourceName != "Croissants" {
		t.Fatalf("unmapped categories = %+v, want one entry for Croissants", preview.UnmappedCategories)
	}
	if preview.UnmappedCategories[0].RowCount != 2 {
		t.Fatalf("row count = %d, want 2: the cost of the decision must be visible",
			preview.UnmappedCategories[0].RowCount)
	}
}

func TestImport_CommitWithUnmapped_409(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "unknown-category.csv")
	_, err := svc.Commit(h.ctx, u.ID, batch.ID)
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Commit error = %v, want apperr.ErrConflict", err)
	}
	if got := h.liveRows(u.ID); got != 0 {
		t.Fatalf("stored = %d, want 0: a refused commit writes nothing", got)
	}
}

func TestImport_FuzzySuggestsNeverApplies(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "fuzzy-category.csv")
	preview, err := svc.Preview(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(preview.UnmappedCategories) != 1 {
		t.Fatalf("unmapped categories = %+v, want one", preview.UnmappedCategories)
	}
	suggestion := preview.UnmappedCategories[0].Suggestion
	if suggestion == nil || suggestion.Name != "Entertainment" {
		t.Fatalf("suggestion = %+v, want Entertainment", suggestion)
	}
	// A proposal is not an application: the batch is still blocked and no alias
	// was written.
	if preview.Batch.Status != importer.StatusNeedsMapping {
		t.Fatalf("status = %q, want needs_mapping", preview.Batch.Status)
	}
	aliases, err := h.categoryService().ListAliases(h.ctx, u.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	for _, a := range aliases {
		if a.SourceName == "Entertainmnet" {
			t.Fatal("a fuzzy suggestion created an alias; it must only ever propose")
		}
	}
}

func TestImport_ApplyMappings_ThenCommits(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "fuzzy-category.csv")
	entertainment := h.categoryByName(u.ID, "Entertainment")

	preview, err := svc.ApplyMappings(h.ctx, u.ID, batch.ID, importer.Mappings{
		Categories: []importer.Mapping{{SourceName: "Entertainmnet", TargetID: entertainment.ID}},
	})
	if err != nil {
		t.Fatalf("ApplyMappings: %v", err)
	}
	if preview.Batch.Status != importer.StatusPreviewed {
		t.Fatalf("status = %q, want previewed after mapping", preview.Batch.Status)
	}
	if preview.Batch.RowsUnmapped != 0 {
		t.Fatalf("rows_unmapped = %d, want 0", preview.Batch.RowsUnmapped)
	}

	committed, err := svc.Commit(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if committed.Status != importer.StatusCommitted || h.liveRows(u.ID) != 2 {
		t.Fatalf("status = %q with %d rows, want committed with 2", committed.Status, h.liveRows(u.ID))
	}

	// The decision is permanent: that name never asks again.
	counts := h.countsByCategory(u.ID)
	if counts["Entertainment"] != 1 {
		t.Fatalf("Entertainment rows = %d, want 1", counts["Entertainment"])
	}
}

func TestImport_Revert_RemovesExactlyBatch(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	keep := h.receive(svc, u.ID, "reimport-same.csv")
	if _, err := svc.Commit(h.ctx, u.ID, keep.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	drop := h.receive(svc, u.ID, "identical-rows.csv")
	if _, err := svc.Commit(h.ctx, u.ID, drop.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := h.liveRows(u.ID); got != 5 {
		t.Fatalf("stored = %d, want 5 before the revert", got)
	}

	reverted, err := svc.Revert(h.ctx, u.ID, drop.ID)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if reverted.Status != importer.StatusReverted || reverted.RevertedAt == nil {
		t.Fatalf("batch = %+v, want reverted with a timestamp", reverted)
	}
	if got := h.liveRows(u.ID); got != 2 {
		t.Fatalf("stored = %d, want 2: the other batch must be untouched", got)
	}

	// Un-revert by re-importing: the rows were soft-deleted, so the occurrence
	// numbers are free again.
	again := h.receive(svc, u.ID, "identical-rows.csv")
	if again.RowsNew != 3 {
		t.Fatalf("rows_new after revert = %d, want 3", again.RowsNew)
	}
	if _, err := svc.Commit(h.ctx, u.ID, again.ID); err != nil {
		t.Fatalf("Commit after revert: %v", err)
	}
	if got := h.liveRows(u.ID); got != 5 {
		t.Fatalf("stored = %d, want 5 after re-importing", got)
	}
}

func TestImport_VanishedFlaggedNotDeleted(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	first := h.receive(svc, u.ID, "reimport-same.csv")
	if _, err := svc.Commit(h.ctx, u.ID, first.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// The next export covers the same dates but no longer contains the transport
	// row: someone deleted it in Monefy.
	second := h.receive(svc, u.ID, "vanished-row.csv")
	preview, err := svc.Preview(h.ctx, u.ID, second.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(preview.Vanished) != 1 {
		t.Fatalf("vanished = %+v, want exactly one row", preview.Vanished)
	}
	if preview.Vanished[0].CategoryName != "Transport" {
		t.Fatalf("vanished category = %q, want Transport", preview.Vanished[0].CategoryName)
	}
	// Flagged, never deleted: auto-deleting would make a parsing bug destructive.
	if got := h.liveRows(u.ID); got != 2 {
		t.Fatalf("stored = %d, want 2 — nothing is removed by a reconcile", got)
	}
}

func TestImport_VanishedScopedToFileRange(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	january := h.receive(svc, u.ID, "reimport-same.csv")
	if _, err := svc.Commit(h.ctx, u.ID, january.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	december := h.receive(svc, u.ID, "huf-zero-decimal.csv")
	if _, err := svc.Commit(h.ctx, u.ID, december.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// A partial export covering only January must not imply that December's row
	// was deleted.
	partial := h.receive(svc, u.ID, "vanished-row.csv")
	preview, err := svc.Preview(h.ctx, u.ID, partial.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	for _, v := range preview.Vanished {
		if v.OccurredOn.Format(transaction.DateLayout) == "2023-12-03" {
			t.Fatalf("a row outside the file's range was flagged: %+v", v)
		}
	}
	if len(preview.Vanished) != 1 {
		t.Fatalf("vanished = %d rows, want exactly the one inside the range", len(preview.Vanished))
	}
}

func TestImport_RussianLocale(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "russian-locale.csv")
	if batch.RowsUnmapped != 0 {
		t.Fatalf("rows_unmapped = %d, want 0: Наличные and Счета resolve via aliases", batch.RowsUnmapped)
	}
	if _, err := svc.Commit(h.ctx, u.ID, batch.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	counts := h.countsByCategory(u.ID)
	if counts["Bills"] != 1 {
		t.Fatalf("Bills rows = %d, want 1 (Счета)", counts["Bills"])
	}

	var accountName string
	if err := h.db.QueryRow(`
		SELECT a.name FROM transaction_entry t
		JOIN account a ON a.id = t.account_id AND a.user_id = t.user_id
		WHERE t.user_id = ? AND t.deleted_at IS NULL LIMIT 1`, u.ID).Scan(&accountName); err != nil {
		t.Fatalf("reading account: %v", err)
	}
	if accountName != "Cash UAH" {
		t.Fatalf("account = %q, want Cash UAH", accountName)
	}
}

func TestImport_SameSha256_ReportsAlreadyImported(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	first := h.receive(svc, u.ID, "reimport-same.csv")
	if _, err := svc.Commit(h.ctx, u.ID, first.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	again := h.receive(svc, u.ID, "reimport-same.csv")
	if !again.AlreadyImported {
		t.Fatal("re-uploading an identical committed file must be reported, not reprocessed")
	}
	if again.ID != first.ID {
		t.Fatalf("batch id = %d, want the original %d", again.ID, first.ID)
	}
}

// TestImport_PreviewAfterCommit_KeepsTheRecord guards a bug the browser found:
// previewing re-runs the analysis, and on a committed batch that re-derivation
// sees the batch's own rows in the database — so it reported 0 new / 1,683
// duplicate and reset the status to previewed, losing committed_at and offering
// the commit button again.
func TestImport_PreviewAfterCommit_KeepsTheRecord(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "reimport-same.csv")
	if _, err := svc.Commit(h.ctx, u.ID, batch.ID); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	preview, err := svc.Preview(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.Batch.Status != importer.StatusCommitted {
		t.Fatalf("status = %q, want it to stay committed", preview.Batch.Status)
	}
	if preview.Batch.CommittedAt == nil {
		t.Fatal("committed_at must survive a preview")
	}
	if preview.Batch.RowsNew != 2 || preview.Batch.RowsDuplicate != 0 {
		t.Fatalf("counts = %d new / %d duplicate, want the 2 / 0 the batch actually did",
			preview.Batch.RowsNew, preview.Batch.RowsDuplicate)
	}
	// The file's own properties are still derived, because those describe the
	// file rather than the ledger.
	if preview.MonthsTouched != 1 || preview.DateRange == nil {
		t.Fatalf("preview lost the file's date range: %+v", preview.DateRange)
	}
	if len(preview.Vanished) != 0 {
		t.Fatalf("vanished = %+v, want none for a committed batch", preview.Vanished)
	}

	stored, err := svc.Get(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != importer.StatusCommitted {
		t.Fatalf("the stored batch is now %q; the preview must not write to it", stored.Status)
	}
}

func TestImport_MalformedRow_RejectedNotFatal(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	svc := h.importService()

	batch := h.receive(svc, u.ID, "malformed-row.csv")
	if batch.RowsRejected != 1 || batch.RowsNew != 2 {
		t.Fatalf("batch = %d rejected / %d new, want 1 / 2", batch.RowsRejected, batch.RowsNew)
	}
	if batch.Status != importer.StatusPreviewed {
		t.Fatalf("status = %q, want previewed: a rejected line does not block", batch.Status)
	}

	rows, err := svc.Rows(h.ctx, u.ID, batch.ID, importer.RowRejected, 0)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Reason == "" {
		t.Fatalf("rejected rows = %+v, want one with a reason", rows)
	}
}

// TestImport_AtomicCommit drives the repository directly: the second entry
// references an account that does not exist, so the statement fails part-way and
// the whole transaction must roll back.
func TestImport_AtomicCommit(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	repo := NewImportRepo(h.db)

	batch, err := repo.CreateBatch(h.ctx, importer.Batch{
		UserID: u.ID, Source: importer.SourceMonefy, Origin: importer.OriginWeb,
		Filename: "atomic.csv", Status: importer.StatusPreviewed,
	}, fixedNow)
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	good := h.accountByName(u.ID, "Cash UAH")
	food := h.categoryByName(u.ID, "Food")
	entry := func(accountID int64, key string) importer.CommitEntry {
		return importer.CommitEntry{LineNo: 2, Transaction: transaction.Transaction{
			UserID: u.ID, AccountID: accountID, CategoryID: &food.ID,
			OccurredOn: mustDate("2023-01-10"), Kind: transaction.KindExpense,
			Amount: money.New(1000, "UAH"), NaturalKey: key, Occurrence: 1,
		}}
	}
	entries := []importer.CommitEntry{
		entry(good.ID, "key-one"),
		entry(999999, "key-two"), // no such account
	}

	if _, err := repo.Commit(h.ctx, u.ID, batch.ID, entries, importer.Counts{Total: 2, New: 2}, fixedNow); err == nil {
		t.Fatal("Commit must fail when an entry violates a constraint")
	}
	if got := h.liveRows(u.ID); got != 0 {
		t.Fatalf("stored = %d, want 0: a failed commit stores nothing", got)
	}
	after, err := repo.GetBatch(h.ctx, u.ID, batch.ID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if after.Status == importer.StatusCommitted {
		t.Fatal("the batch must not be marked committed after a failure")
	}
}

func TestImportUserIsolation(t *testing.T) {
	h := newHarness(t)
	owner := h.user("owner@example.test")
	other := h.user("other@example.test")
	svc := h.importService()

	batch := h.receive(svc, owner.ID, "reimport-same.csv")

	if _, err := svc.Get(h.ctx, other.ID, batch.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Get as another user = %v, want ErrNotFound", err)
	}
	if _, err := svc.Preview(h.ctx, other.ID, batch.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Preview as another user = %v, want ErrNotFound", err)
	}
	if _, err := svc.Commit(h.ctx, other.ID, batch.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Commit as another user = %v, want ErrNotFound", err)
	}
	if _, err := svc.Rows(h.ctx, other.ID, batch.ID, "", 0); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Rows as another user = %v, want ErrNotFound", err)
	}

	batches, err := svc.List(h.ctx, other.ID, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(batches) != 0 {
		t.Fatalf("other user sees %d batches, want 0", len(batches))
	}
}

func sameCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
