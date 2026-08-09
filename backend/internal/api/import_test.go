package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"moneyfly/internal/db"
	"moneyfly/internal/importer"
	syncproto "moneyfly/internal/sync"
)

const importHeader = "date,account,category,amount,currency,converted amount,currency,description\n"

// seedCategory writes one category op directly, bypassing the record screen —
// these tests only need the resolver to have something to match against.
func seedCategory(t *testing.T, s *Server, userID, id, name, kind string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"name": name, "kind": kind})
	if _, err := s.conn().ApplyOps(userID, []syncproto.Op{
		{Entity: "category", ID: id, Lamport: 1, DeviceID: "dev-a", Data: data},
	}); err != nil {
		t.Fatalf("seed category %s: %v", name, err)
	}
}

func seedAccount(t *testing.T, s *Server, userID, id, name, currency string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"name": name, "currency": currency})
	if _, err := s.conn().ApplyOps(userID, []syncproto.Op{
		{Entity: "account", ID: id, Lamport: 1, DeviceID: "dev-a", Data: data},
	}); err != nil {
		t.Fatalf("seed account %s: %v", name, err)
	}
}

func userIDFor(t *testing.T, s *Server, cookie string) string {
	t.Helper()
	me := decodeBody[UserDTO](t, s.do(t, "GET", "/api/auth/me", "", cookie))
	return me.ID
}

func previewBody(t *testing.T, csv string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"csv": csv})
	if err != nil {
		t.Fatalf("marshal preview body: %v", err)
	}
	return string(b)
}

func commitBody(t *testing.T, csv string, categoryMap, accountMap map[string]string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"csv": csv, "categoryMap": categoryMap, "accountMap": accountMap})
	if err != nil {
		t.Fatalf("marshal commit body: %v", err)
	}
	return string(b)
}

func TestImportRequiresAuth(t *testing.T) {
	s := newTestServer(t, "")
	for _, path := range []string{"/api/import/monefy/preview", "/api/import/monefy/commit"} {
		if res := s.do(t, "POST", path, previewBody(t, importHeader), ""); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, res.StatusCode)
		}
	}
}

// A category exact-matched, one resolved only through the alias table, and
// one genuinely unrecognised — preview must sort all three correctly and
// write nothing.
func TestImportPreviewResolvesAndNeverWrites(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedCategory(t, s, userID, "cat:hotel", "Hotel/Trip", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,exact match\n" +
		"19.07.2021,Cash,HotelTrip,-50,EUR,-50,EUR,alias match\n" +
		"19.07.2021,Cash,Utilities,-5,EUR,-5,EUR,unrecognised\n"

	res := s.do(t, "POST", "/api/import/monefy/preview", previewBody(t, csv), cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	preview := decodeBody[importPreviewResponse](t, res)

	if preview.TotalRows != 3 || len(preview.ParseErrors) != 0 {
		t.Fatalf("preview = %+v", preview)
	}
	byName := map[string]nameStatusDTO{}
	for _, c := range preview.Categories {
		byName[c.Name] = c
	}
	if !byName["Food"].Resolved || byName["Food"].ID != "cat:food" || byName["Food"].ViaAlias {
		t.Errorf("Food = %+v", byName["Food"])
	}
	if !byName["HotelTrip"].Resolved || byName["HotelTrip"].ID != "cat:hotel" || !byName["HotelTrip"].ViaAlias {
		t.Errorf("HotelTrip = %+v", byName["HotelTrip"])
	}
	if byName["Utilities"].Resolved {
		t.Errorf("Utilities resolved to %+v, want unresolved", byName["Utilities"])
	}

	// Preview must not have created any transactions.
	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	for _, row := range snap.Rows {
		if row.Entity == "txn" {
			t.Fatalf("preview wrote a transaction: %+v", row)
		}
	}
}

// The end-to-end path: commit with an explicit mapping for the one category
// preview could not resolve on its own.
func TestImportCommitWritesResolvedRows(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedCategory(t, s, userID, "cat:bills", "Bills", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,lunch\n" +
		"20.07.2021,Cash,Utilities,-5,EUR,-5,EUR,electric\n"

	res := s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, map[string]string{"Utilities": "cat:bills"}, nil), cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	commit := decodeBody[importCommitResponse](t, res)
	if commit.Imported != 2 || commit.AlreadyImported != 0 || len(commit.Unresolved) != 0 {
		t.Fatalf("commit = %+v", commit)
	}

	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	var txns int
	for _, row := range snap.Rows {
		if row.Entity == "txn" {
			txns++
			var body struct {
				CategoryID  string `json:"categoryId"`
				AmountMinor int64  `json:"amountMinor"`
			}
			if err := json.Unmarshal(row.Data, &body); err != nil {
				t.Fatalf("unmarshal txn: %v", err)
			}
			if body.AmountMinor != -1000 && body.AmountMinor != -500 {
				t.Errorf("unexpected amountMinor %d", body.AmountMinor)
			}
		}
	}
	if txns != 2 {
		t.Fatalf("got %d transactions, want 2", txns)
	}
}

// A category left unmapped must block only its own row, never the rest of
// the batch, and must never be silently dropped without a reason reported
// back — the exact failure this package exists to prevent.
func TestImportCommitReportsUnresolvedWithoutBlockingOthers(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,good\n" +
		"20.07.2021,Cash,Utilities,-5,EUR,-5,EUR,unmapped\n"

	commit := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, nil, nil), cookie))

	if commit.Imported != 1 {
		t.Errorf("imported = %d, want 1 (the mapped row)", commit.Imported)
	}
	if len(commit.Unresolved) != 1 || commit.Unresolved[0].Line != 3 {
		t.Errorf("unresolved = %+v, want exactly line 3 reported", commit.Unresolved)
	}
}

// An account name that matches an existing account, but not that account's
// currency, must never resolve to it — the incident this guards against is a
// HUF export auto-matching an existing EUR "Cash" account by name alone and
// misattributing every row to a balance in the wrong currency. Both preview
// and commit must catch it, and an explicit remap to a same-currency account
// must still succeed.
func TestImportAccountCurrencyMismatchBlocksAutoResolve(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:cash-eur", "Cash", "EUR")
	seedAccount(t, s, userID, "acc:cash-huf", "Forint Cash", "HUF")

	csv := importHeader + "19.07.2021,Cash,Food,-1000,HUF,-1000,HUF,taxi\n"

	preview := decodeBody[importPreviewResponse](t, s.do(t, "POST", "/api/import/monefy/preview",
		previewBody(t, csv), cookie))
	if len(preview.Accounts) != 1 || preview.Accounts[0].Resolved {
		t.Fatalf("accounts = %+v, want the EUR account left unresolved for a HUF row", preview.Accounts)
	}
	if !preview.Accounts[0].CurrencyMismatch {
		t.Errorf("accounts[0] = %+v, want CurrencyMismatch set", preview.Accounts[0])
	}

	// Committing without a mapping must block the row, not silently attach it
	// to the EUR account.
	blocked := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, nil, nil), cookie))
	if blocked.Imported != 0 || len(blocked.Unresolved) != 1 {
		t.Fatalf("commit without a mapping = %+v, want the row blocked", blocked)
	}

	// Mapping explicitly to the matching-currency account must succeed.
	mapped := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, nil, map[string]string{"Cash": "acc:cash-huf"}), cookie))
	if mapped.Imported != 1 {
		t.Fatalf("commit with an explicit mapping = %+v, want 1 imported", mapped)
	}
}

// Re-committing the same CSV — the normal way someone re-exports "since last
// time" and it overlaps — must not duplicate the ledger.
func TestImportCommitIsIdempotent(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,lunch\n" +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,lunch again\n" // a second, genuine row

	first := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, nil, nil), cookie))
	if first.Imported != 2 {
		t.Fatalf("first commit imported = %d, want 2", first.Imported)
	}

	second := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, nil, nil), cookie))
	if second.Imported != 0 || second.AlreadyImported != 2 {
		t.Fatalf("second commit = %+v, want 0 imported, 2 already imported", second)
	}

	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	var txns int
	for _, row := range snap.Rows {
		if row.Entity == "txn" {
			txns++
		}
	}
	if txns != 2 {
		t.Fatalf("got %d transactions stored after two commits of the same file, want 2", txns)
	}
}

// A real export that exceeds db.ApplyOps' MaxOpsPerPush in one commit (the
// exact incident: a 1491-row file 500'd with "batch of 1491 exceeds the 1000
// op limit") must still import in full — the commit handler chunks the ops
// it builds rather than handing them to ApplyOps in one call.
func TestImportCommitChunksBatchesOverOpLimit(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	const rows = int(syncproto.MaxOpsPerPush) + 491
	var b strings.Builder
	b.WriteString(importHeader)
	for i := range rows {
		fmt.Fprintf(&b, "19.07.2021,Cash,Food,-10,EUR,-10,EUR,row %d\n", i)
	}

	commit := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, b.String(), nil, nil), cookie))
	if commit.Imported != rows {
		t.Fatalf("imported = %d, want %d", commit.Imported, rows)
	}

	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	var txns int
	for _, row := range snap.Rows {
		if row.Entity == "txn" {
			txns++
		}
	}
	if txns != rows {
		t.Fatalf("got %d transactions stored, want %d", txns, rows)
	}
}

// Chunking trades one atomic transaction for N independent ones, so a failure
// part way through leaves the earlier chunks committed. What must never happen
// is reporting that as a total failure: the rows are in the database either
// way, and the operator needs to know how many before deciding what to do.
func TestApplyInChunksReportsWhatLandedBeforeFailing(t *testing.T) {
	ops := make([]syncproto.Op, syncproto.MaxOpsPerPush*2+10)

	var calls int
	accepted, lastSeq, err := applyInChunks(func(chunk []syncproto.Op) (db.ApplyResult, error) {
		calls++
		if calls == 2 {
			return db.ApplyResult{}, fmt.Errorf("disk full")
		}
		return db.ApplyResult{Accepted: len(chunk), ServerSeq: int64(calls)}, nil
	}, ops)

	if err == nil {
		t.Fatal("err = nil, want the chunk failure surfaced")
	}
	if accepted != syncproto.MaxOpsPerPush {
		t.Errorf("accepted = %d, want %d — the first chunk committed and must be reported",
			accepted, syncproto.MaxOpsPerPush)
	}
	if lastSeq != 1 {
		t.Errorf("lastSeq = %d, want 1 (the last chunk that actually applied)", lastSeq)
	}
	if calls != 2 {
		t.Errorf("apply called %d times, want 2 — it must stop at the failure", calls)
	}
}

func TestApplyInChunksSplitsOnTheOpLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ops        int
		wantChunks int
	}{
		{"empty", 0, 0},
		{"one", 1, 1},
		{"exactly the limit", syncproto.MaxOpsPerPush, 1},
		{"one over", syncproto.MaxOpsPerPush + 1, 2},
		{"the 1491-row incident", 1491, 2},
		{"several times over", syncproto.MaxOpsPerPush*3 + 7, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chunks int
			accepted, _, err := applyInChunks(func(chunk []syncproto.Op) (db.ApplyResult, error) {
				chunks++
				if len(chunk) > syncproto.MaxOpsPerPush {
					t.Errorf("chunk of %d exceeds the %d op limit", len(chunk), syncproto.MaxOpsPerPush)
				}
				return db.ApplyResult{Accepted: len(chunk)}, nil
			}, make([]syncproto.Op, tc.ops))
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if chunks != tc.wantChunks {
				t.Errorf("chunks = %d, want %d", chunks, tc.wantChunks)
			}
			if accepted != tc.ops {
				t.Errorf("accepted = %d, want %d", accepted, tc.ops)
			}
		})
	}
}

// An ordinary import must not start advertising a failure field.
func TestImportCommitReportsNoFailureOnASuccessfulImport(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	csv := importHeader + "19.07.2021,Cash,Food,-10,EUR,-10,EUR,\n"
	commit := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, nil, nil), cookie))

	if commit.Failed != 0 || commit.FailureReason != "" {
		t.Errorf("failed = %d, reason = %q on a clean import, want 0 and empty",
			commit.Failed, commit.FailureReason)
	}
}

// One account name in two currencies is two wallets, and the preview has to
// say so — grouping by name alone reported one resolved entry where the commit
// found two, and the second currency's rows were dropped as unresolved after
// the operator had been told the file was clean.
func TestPreviewSplitsOneAccountNameAcrossCurrencies(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,\n" +
		"20.07.2021,Cash,Food,-1000,HUF,-3,EUR,\n"

	preview := decodeBody[importPreviewResponse](t, s.do(t, "POST",
		"/api/import/monefy/preview", previewBody(t, csv), cookie))

	if len(preview.Accounts) != 2 {
		t.Fatalf("got %d account entries, want 2 (Cash/EUR and Cash/HUF): %+v",
			len(preview.Accounts), preview.Accounts)
	}
	byKey := map[string]nameStatusDTO{}
	for _, a := range preview.Accounts {
		byKey[a.Key] = a
	}
	eur, ok := byKey[importer.AccountKey("Cash", "EUR")]
	if !ok || !eur.Resolved {
		t.Errorf("EUR Cash should resolve to the seeded account: %+v", eur)
	}
	huf, ok := byKey[importer.AccountKey("Cash", "HUF")]
	if !ok {
		t.Fatalf("no entry for Cash in HUF: %+v", preview.Accounts)
	}
	if huf.Resolved {
		t.Error("HUF Cash resolved against a EUR account")
	}
	if !huf.CurrencyMismatch {
		t.Error("HUF Cash should report currencyMismatch — the name exists, the currency does not")
	}
}

// Mapping each currency separately must send each currency's rows to its own
// account.
func TestCommitMapsEachCurrencyIndependently(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:eur", "Cash", "EUR")
	seedAccount(t, s, userID, "acc:huf", "Cash HUF", "HUF")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,\n" +
		"20.07.2021,Cash,Food,-1000,HUF,-3,EUR,\n"
	accountMap := map[string]string{importer.AccountKey("Cash", "HUF"): "acc:huf"}

	commit := decodeBody[importCommitResponse](t, s.do(t, "POST", "/api/import/monefy/commit",
		commitBody(t, csv, nil, accountMap), cookie))
	if commit.Imported != 2 {
		t.Fatalf("imported = %d, want 2: %+v", commit.Imported, commit)
	}

	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	got := map[string]string{} // currency -> accountId
	for _, row := range snap.Rows {
		if row.Entity != "txn" {
			continue
		}
		var body struct {
			Currency  string `json:"currency"`
			AccountID string `json:"accountId"`
		}
		if err := json.Unmarshal(row.Data, &body); err != nil {
			t.Fatalf("unmarshal txn: %v", err)
		}
		got[body.Currency] = body.AccountID
	}
	if got["EUR"] != "acc:eur" {
		t.Errorf("EUR row landed in %q, want acc:eur", got["EUR"])
	}
	if got["HUF"] != "acc:huf" {
		t.Errorf("HUF row landed in %q, want acc:huf", got["HUF"])
	}
}

// The same name used as both an expense and an income category is two
// categories, and mapping one must not map the other.
func TestPreviewSplitsOneCategoryNameAcrossKinds(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")

	csv := importHeader +
		"19.07.2021,Cash,Gifts,-10,EUR,-10,EUR,\n" +
		"20.07.2021,Cash,Gifts,25,EUR,25,EUR,\n"

	preview := decodeBody[importPreviewResponse](t, s.do(t, "POST",
		"/api/import/monefy/preview", previewBody(t, csv), cookie))

	if len(preview.Categories) != 2 {
		t.Fatalf("got %d category entries, want 2 (expense and income Gifts): %+v",
			len(preview.Categories), preview.Categories)
	}
	keys := map[string]bool{}
	for _, c := range preview.Categories {
		if c.Key == "" {
			t.Errorf("category entry has no key: %+v", c)
		}
		keys[c.Key] = true
	}
	for _, want := range []string{
		importer.CategoryKey("Gifts", "expense"),
		importer.CategoryKey("Gifts", "income"),
	} {
		if !keys[want] {
			t.Errorf("missing key %q; mapping one kind would silently map the other", want)
		}
	}
}

// Every currency in the file is reported, so the screen can offer to declare
// the ones with no rate rather than letting those rows import and then sit
// outside every total with nothing to explain why.
func TestPreviewReportsEveryDistinctCurrency(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,\n" +
		"20.07.2021,Cash,Food,-1000,HUF,-3,EUR,\n" +
		"21.07.2021,Cash,Food,-65,UAH,-2,EUR,\n" +
		"22.07.2021,Cash,Food,-20,EUR,-20,EUR,\n"

	preview := decodeBody[importPreviewResponse](t, s.do(t, "POST",
		"/api/import/monefy/preview", previewBody(t, csv), cookie))

	want := []currencyCountDTO{{"EUR", 2}, {"HUF", 1}, {"UAH", 1}}
	if len(preview.Currencies) != len(want) {
		t.Fatalf("got %+v, want %+v", preview.Currencies, want)
	}
	for i, w := range want {
		if preview.Currencies[i] != w {
			t.Errorf("currencies[%d] = %+v, want %+v (order of first appearance)",
				i, preview.Currencies[i], w)
		}
	}
}

// Group counts have to cover every row exactly once, or the screen's "N rows
// will be skipped" is wrong in whichever direction the double-count falls.
func TestPreviewGroupsCountEveryRowExactlyOnce(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	csv := importHeader +
		"19.07.2021,Cash,Food,-10,EUR,-10,EUR,\n" +
		"20.07.2021,Cash,Food,-20,EUR,-20,EUR,\n" +
		"21.07.2021,Bank,Food,-30,EUR,-30,EUR,\n" +
		"22.07.2021,Cash,Bills,-40,EUR,-40,EUR,\n"

	preview := decodeBody[importPreviewResponse](t, s.do(t, "POST",
		"/api/import/monefy/preview", previewBody(t, csv), cookie))

	var total int
	for _, g := range preview.Groups {
		total += g.Count
	}
	if total != preview.TotalRows {
		t.Errorf("group counts sum to %d, want totalRows %d", total, preview.TotalRows)
	}
	if len(preview.Groups) != 3 {
		t.Errorf("got %d groups, want 3 distinct (category, account) pairs: %+v",
			len(preview.Groups), preview.Groups)
	}
}

// Two accounts on one instance must never see each other's categories or
// accounts while resolving, the same isolation the ordinary sync path has.
func TestImportStaysScopedPerUser(t *testing.T) {
	s := newTestServer(t, "")
	a := signUp(t, s, "a@example.com", "dev-a")
	aID := userIDFor(t, s, a)
	seedCategory(t, s, aID, "cat:food", "Food", "expense")
	seedAccount(t, s, aID, "acc:cash", "Cash", "EUR")

	b := signUp(t, s, "b@example.com", "dev-b")

	csv := importHeader + "19.07.2021,Cash,Food,-10,EUR,-10,EUR,\n"
	preview := decodeBody[importPreviewResponse](t, s.do(t, "POST", "/api/import/monefy/preview", previewBody(t, csv), b))
	if preview.Categories[0].Resolved {
		t.Errorf("account b's preview resolved against account a's category: %+v", preview.Categories[0])
	}
}
