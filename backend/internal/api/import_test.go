package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

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
