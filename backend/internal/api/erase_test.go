package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"moneyfly/internal/db"
	syncproto "moneyfly/internal/sync"
)

func commitNamed(t *testing.T, s *Server, cookie, csv, fileName string) importCommitResponse {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"csv": csv, "fileName": fileName})
	res := s.do(t, "POST", "/api/import/monefy/commit", string(b), cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("commit status = %d", res.StatusCode)
	}
	return decodeBody[importCommitResponse](t, res)
}

// liveCounts is what a freshly bootstrapped device would hold, per entity.
func liveCounts(t *testing.T, s *Server, cookie string) map[string]int {
	t.Helper()
	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	out := map[string]int{}
	for _, row := range snap.Rows {
		out[row.Entity]++
	}
	return out
}

func listImports(t *testing.T, s *Server, cookie string) []importBatchDTO {
	t.Helper()
	res := s.do(t, "GET", "/api/imports", "", cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list imports status = %d", res.StatusCode)
	}
	return decodeBody[[]importBatchDTO](t, res)
}

func seedImportable(t *testing.T, s *Server, cookie string) {
	t.Helper()
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:cash", "Cash", "EUR")
}

const twoRows = importHeader +
	"19.07.2021,Cash,Food,-10,EUR,-10,EUR,lunch\n" +
	"20.07.2021,Cash,Food,-5,EUR,-5,EUR,coffee\n"

// The reason the feature exists: undo an import, then run the same file again
// and get every row back. A tombstone that kept its natural key would make the
// second run skip all of them as "already imported".
func TestUndoImportThenReimportWritesEveryRowAgain(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	seedImportable(t, s, cookie)

	if c := commitNamed(t, s, cookie, twoRows, "monefy.csv"); c.Imported != 2 {
		t.Fatalf("first import = %+v", c)
	}
	imports := listImports(t, s, cookie)
	if len(imports) != 1 || imports[0].FileName != "monefy.csv" || imports[0].Rows != 2 {
		t.Fatalf("imports = %+v", imports)
	}

	res := s.do(t, "DELETE", "/api/imports/"+imports[0].ID, "", cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("undo status = %d", res.StatusCode)
	}
	if out := decodeBody[eraseResponse](t, res); out.Deleted != 2 || out.Failed != 0 {
		t.Fatalf("undo = %+v", out)
	}
	if n := liveCounts(t, s, cookie)["txn"]; n != 0 {
		t.Fatalf("%d transactions left after undo, want 0", n)
	}
	if got := listImports(t, s, cookie); len(got) != 0 {
		t.Fatalf("an undone import is still listed: %+v", got)
	}

	again := commitNamed(t, s, cookie, twoRows, "monefy.csv")
	if again.Imported != 2 || again.AlreadyImported != 0 {
		t.Fatalf("re-import after undo = %+v, want 2 imported", again)
	}
}

// Undo takes back exactly one import and leaves an earlier one alone, and the
// list is newest first so "the last one" is the top row.
func TestUndoImportRemovesOnlyThatImport(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	seedImportable(t, s, cookie)

	commitNamed(t, s, cookie, importHeader+"19.07.2021,Cash,Food,-10,EUR,-10,EUR,first\n", "first.csv")
	commitNamed(t, s, cookie, twoRows, "second.csv")

	imports := listImports(t, s, cookie)
	if len(imports) != 2 {
		t.Fatalf("imports = %+v", imports)
	}
	// Same-second timestamps fall back to id order, and ids are time-ordered.
	if imports[0].FileName != "second.csv" {
		t.Fatalf("newest import listed first = %q, want second.csv", imports[0].FileName)
	}

	s.do(t, "DELETE", "/api/imports/"+imports[0].ID, "", cookie)
	if n := liveCounts(t, s, cookie)["txn"]; n != 1 {
		t.Fatalf("%d transactions left, want the first import's 1", n)
	}
}

// Deleting one imported record by hand is still intent: a re-import of an
// overlapping export must not bring it back. Only an explicit undo forgets.
func TestHandDeletedImportedRowStaysDeletedOnReimport(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	seedImportable(t, s, cookie)
	commitNamed(t, s, cookie, twoRows, "monefy.csv")

	// What a device's remove() sends: the row's body, natural key included.
	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	var victim syncproto.Change
	for _, row := range snap.Rows {
		if row.Entity == "txn" {
			victim = row
			break
		}
	}
	victim.Lamport++
	victim.DeviceID = "dev-a"
	victim.Deleted = true
	if _, err := s.conn().ApplyOps(userIDFor(t, s, cookie), []syncproto.Op{victim.Op}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	again := commitNamed(t, s, cookie, twoRows, "monefy.csv")
	if again.Imported != 0 || again.AlreadyImported != 2 {
		t.Fatalf("re-import = %+v, want both rows recognised", again)
	}
}

func TestEraseRecordsKeepsAccountsAndCategories(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	seedImportable(t, s, cookie)
	commitNamed(t, s, cookie, twoRows, "monefy.csv")

	res := s.do(t, "POST", "/api/data/erase", `{"scope":"records"}`, cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if out := decodeBody[eraseResponse](t, res); out.Deleted != 2 {
		t.Fatalf("erase = %+v", out)
	}
	counts := liveCounts(t, s, cookie)
	if counts["txn"] != 0 || counts["category"] != 1 || counts["account"] != 1 {
		t.Fatalf("live rows after erasing records = %v", counts)
	}
	if again := commitNamed(t, s, cookie, twoRows, "monefy.csv"); again.Imported != 2 {
		t.Fatalf("re-import after erase = %+v", again)
	}
}

func TestEraseEverythingKeepsSettings(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	seedImportable(t, s, cookie)
	commitNamed(t, s, cookie, twoRows, "monefy.csv")
	s.do(t, "POST", "/api/sync/push", pushBody("dev-a",
		`{"entity":"user_setting","id":"baseCurrency","lamport":3,"deviceId":"dev-a","deleted":false,"data":{"value":"EUR"}}`,
	), cookie)

	out := decodeBody[eraseResponse](t, s.do(t, "POST", "/api/data/erase", `{"scope":"everything"}`, cookie))
	if out.Deleted != 4 {
		t.Fatalf("erase = %+v, want 2 txns + 1 category + 1 account", out)
	}
	counts := liveCounts(t, s, cookie)
	if len(counts) != 1 || counts["user_setting"] != 1 {
		t.Fatalf("live rows after erasing everything = %v, want only the setting", counts)
	}
}

// The tombstone must beat a row whatever lamport a device last wrote it at —
// erase is the person's own latest action, not a background write that loses.
func TestEraseBeatsARowWithAHighLamport(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	s.do(t, "POST", "/api/sync/push", pushBody("zz-dev", txnOpJSON("t1", 50, "zz-dev")), cookie)

	out := decodeBody[eraseResponse](t, s.do(t, "POST", "/api/data/erase", `{"scope":"records"}`, cookie))
	if out.Deleted != 1 {
		t.Fatalf("erase = %+v", out)
	}
	if n := liveCounts(t, s, cookie)["txn"]; n != 0 {
		t.Fatalf("%d transactions survived", n)
	}
}

func TestEraseRejectsUnknownScope(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	if res := s.do(t, "POST", "/api/data/erase", `{"scope":"all"}`, cookie); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	if res := s.do(t, "POST", "/api/data/erase", `{"scope":"records"}`, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", res.StatusCode)
	}
}

// Rows imported before batches were tracked have a csv natural key and no
// importId. They are listed together and can be undone together.
func TestUntrackedImportsAreListedAndUndoable(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	old, _ := json.Marshal(map[string]any{
		"kind": "expense", "occurredOn": "2021-07-19", "amountMinor": -1000, "currency": "EUR",
		"naturalKey": "csv:abcdef:0",
	})
	manual, _ := json.Marshal(map[string]any{
		"kind": "expense", "occurredOn": "2021-07-19", "amountMinor": -500, "currency": "EUR",
	})
	if _, err := s.conn().ApplyOps(userID, []syncproto.Op{
		{Entity: "txn", ID: "old", Lamport: 1, DeviceID: serverDeviceID, Data: old},
		{Entity: "txn", ID: "manual", Lamport: 1, DeviceID: "dev-a", Data: manual},
	}); err != nil {
		t.Fatal(err)
	}

	imports := listImports(t, s, cookie)
	if len(imports) != 1 || imports[0].ID != db.LegacyImportID || !imports[0].Legacy || imports[0].Rows != 1 {
		t.Fatalf("imports = %+v", imports)
	}
	s.do(t, "DELETE", "/api/imports/"+db.LegacyImportID, "", cookie)
	if n := liveCounts(t, s, cookie)["txn"]; n != 1 {
		t.Fatalf("%d transactions left, want only the hand-entered one", n)
	}
}

func TestUndoImportIsScopedPerUser(t *testing.T) {
	s := newTestServer(t, "")
	alice := signUp(t, s, "a@example.com", "dev-a")
	seedImportable(t, s, alice)
	commitNamed(t, s, alice, twoRows, "monefy.csv")
	id := listImports(t, s, alice)[0].ID

	bob := signUp(t, s, "b@example.com", "dev-b")
	if res := s.do(t, "DELETE", "/api/imports/"+id, "", bob); res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if got := listImports(t, s, bob); len(got) != 0 {
		t.Fatalf("bob sees alice's imports: %+v", got)
	}
	if n := liveCounts(t, s, alice)["txn"]; n != 2 {
		t.Fatalf("alice has %d transactions, want 2", n)
	}
}
