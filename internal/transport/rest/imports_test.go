package rest

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

const fixtureDir = "../../../testdata/"

// upload posts a fixture as multipart/form-data, the way the browser and the bot
// both do.
func (ts *testServer) upload(fixture string) *http.Response {
	ts.t.Helper()
	data, err := os.ReadFile(fixtureDir + fixture)
	if err != nil {
		ts.t.Fatalf("reading %s: %v", fixture, err)
	}
	return ts.uploadBytes(fixture, data)
}

func (ts *testServer) uploadBytes(filename string, data []byte) *http.Response {
	ts.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(uploadField, filename)
	if err != nil {
		ts.t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		ts.t.Fatalf("multipart write: %v", err)
	}
	if err := writer.Close(); err != nil {
		ts.t.Fatalf("multipart close: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.http.URL+APIPrefix+"/imports", &body)
	if err != nil {
		ts.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := ts.client.Do(req)
	if err != nil {
		ts.t.Fatalf("POST /imports: %v", err)
	}
	return resp
}

func TestImportAPI_UploadPreviewCommitRevert(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.upload("reimport-same.csv")
	expectStatus(t, resp, http.StatusCreated)
	var batch BatchDTO
	decode(t, resp, &batch)
	if batch.ID == 0 || batch.Status != "previewed" {
		t.Fatalf("batch = %+v, want an id and status previewed", batch)
	}

	var preview PreviewDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/imports/"+strconv.FormatInt(batch.ID, 10), nil, nil), &preview)
	if preview.RowsTotal != 2 || preview.RowsNew != 2 || preview.RowsUnmapped != 0 {
		t.Fatalf("preview = %+v, want 2 total / 2 new / 0 unmapped", preview)
	}
	if preview.DateRange == nil || preview.DateRange.From != "2023-01-10" || preview.DateRange.To != "2023-01-11" {
		t.Fatalf("date range = %+v, want 2023-01-10..2023-01-11", preview.DateRange)
	}
	if preview.MonthsTouched != 1 {
		t.Fatalf("months_touched = %d, want 1", preview.MonthsTouched)
	}
	if len(preview.Warnings) == 0 {
		t.Fatal("a file with no income rows must say so")
	}

	var committed BatchDTO
	decode(t, ts.do(http.MethodPost,
		APIPrefix+"/imports/"+strconv.FormatInt(batch.ID, 10)+"/commit", nil, nil), &committed)
	if committed.Status != "committed" || committed.CommittedAt == nil {
		t.Fatalf("committed = %+v, want status committed with a timestamp", committed)
	}

	var page struct {
		Items []TransactionDTO `json:"items"`
	}
	decode(t, ts.do(http.MethodGet, APIPrefix+"/transactions?limit=50", nil, nil), &page)
	if len(page.Items) != 2 {
		t.Fatalf("transactions = %d, want 2", len(page.Items))
	}
	if page.Items[0].ImportBatchID == nil || *page.Items[0].ImportBatchID != batch.ID {
		t.Fatalf("import_batch_id = %v, want %d", page.Items[0].ImportBatchID, batch.ID)
	}

	var reverted BatchDTO
	decode(t, ts.do(http.MethodPost,
		APIPrefix+"/imports/"+strconv.FormatInt(batch.ID, 10)+"/revert", nil, nil), &reverted)
	if reverted.Status != "reverted" {
		t.Fatalf("status = %q, want reverted", reverted.Status)
	}
	decode(t, ts.do(http.MethodGet, APIPrefix+"/transactions?limit=50", nil, nil), &page)
	if len(page.Items) != 0 {
		t.Fatalf("transactions after revert = %d, want 0", len(page.Items))
	}
}

func TestImportAPI_UnmappedBlocksCommitWith409(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.upload("unknown-category.csv")
	expectStatus(t, resp, http.StatusCreated)
	var batch BatchDTO
	decode(t, resp, &batch)
	if batch.Status != "needs_mapping" || batch.RowsUnmapped != 2 {
		t.Fatalf("batch = %+v, want needs_mapping with 2 unmapped rows", batch)
	}

	commit := ts.do(http.MethodPost,
		APIPrefix+"/imports/"+strconv.FormatInt(batch.ID, 10)+"/commit", nil, nil)
	expectStatus(t, commit, http.StatusConflict)
	commit.Body.Close()

	var page struct {
		Items []TransactionDTO `json:"items"`
	}
	decode(t, ts.do(http.MethodGet, APIPrefix+"/transactions?limit=50", nil, nil), &page)
	if len(page.Items) != 0 {
		t.Fatalf("transactions = %d, want 0: a refused commit stores nothing", len(page.Items))
	}
}

func TestImportAPI_MappingScreenPayload(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.upload("fuzzy-category.csv")
	var batch BatchDTO
	decode(t, resp, &batch)

	var preview PreviewDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/imports/"+strconv.FormatInt(batch.ID, 10), nil, nil), &preview)
	if len(preview.UnmappedCategories) != 1 {
		t.Fatalf("unmapped categories = %+v, want one", preview.UnmappedCategories)
	}
	entry := preview.UnmappedCategories[0]
	if entry.SourceName != "Entertainmnet" || entry.RowCount != 1 {
		t.Fatalf("entry = %+v, want Entertainmnet with 1 row", entry)
	}
	if entry.Suggestion == nil || entry.Suggestion.Name != "Entertainment" {
		t.Fatalf("suggestion = %+v, want Entertainment", entry.Suggestion)
	}

	// Confirming the suggestion is what applies it.
	var mapped PreviewDTO
	decode(t, ts.do(http.MethodPost,
		APIPrefix+"/imports/"+strconv.FormatInt(batch.ID, 10)+"/mappings",
		MappingsInputDTO{Categories: []MappingDTO{{
			SourceName: entry.SourceName, TargetID: entry.Suggestion.ID,
		}}}, nil), &mapped)
	if mapped.Status != "previewed" || mapped.RowsUnmapped != 0 {
		t.Fatalf("after mapping = %+v, want previewed with 0 unmapped", mapped)
	}
}

func TestImportAPI_RowsFilteredByStatus(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	var batch BatchDTO
	decode(t, ts.upload("malformed-row.csv"), &batch)

	var rows []ImportRowDTO
	decode(t, ts.do(http.MethodGet,
		APIPrefix+"/imports/"+strconv.FormatInt(batch.ID, 10)+"/rows?status=rejected", nil, nil), &rows)
	if len(rows) != 1 {
		t.Fatalf("rejected rows = %d, want 1", len(rows))
	}
	if rows[0].Reason == "" {
		t.Fatal("a rejected row must carry the reason it was rejected")
	}
}

func TestImportAPI_TooLargeReturns413(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	header := "date,account,category,amount,currency,converted amount,currency,description\n"
	row := "01.01.2023,UAH,Food,-10,UAH,-10,UAH,padding padding padding padding padding\n"
	// Just over the configured cap.
	big := make([]byte, 0, ts.deps.Config.Imports.MaxUploadBytes+int64(len(header))+1)
	big = append(big, header...)
	for int64(len(big)) <= ts.deps.Config.Imports.MaxUploadBytes {
		big = append(big, row...)
	}

	resp := ts.uploadBytes("huge.csv", big)
	expectStatus(t, resp, http.StatusRequestEntityTooLarge)
	resp.Body.Close()
}

func TestImportAPI_NonMonefyFileIsRefused(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	resp := ts.upload("not-monefy.csv")
	expectStatus(t, resp, http.StatusUnprocessableEntity)
	if body := bodyString(t, resp); !strings.Contains(body, "Monefy") {
		t.Fatalf("body = %s, want it to name the expected format", body)
	}
}

func TestImportAPI_RequiresSession(t *testing.T) {
	ts := newTestServer(t, true)
	resp := ts.do(http.MethodGet, APIPrefix+"/imports", nil, nil)
	expectStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestImportAPI_ListsBatchHistory(t *testing.T) {
	ts := newTestServer(t, true)
	ts.login("owner@example.test")

	ts.upload("reimport-same.csv").Body.Close()
	ts.upload("identical-rows.csv").Body.Close()

	var list []BatchDTO
	decode(t, ts.do(http.MethodGet, APIPrefix+"/imports", nil, nil), &list)
	if len(list) != 2 {
		t.Fatalf("batches = %d, want 2", len(list))
	}
}
