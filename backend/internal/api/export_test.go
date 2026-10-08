package api

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"moneyfly/internal/fx"
)

func TestExportRequiresAuth(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "GET", "/api/export/transactions.csv", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}

func TestExportRejectsUnknownProfile(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	res := s.do(t, "GET", "/api/export/transactions.csv?profile=nope", "", cookie)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestExportNativeProfile(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	seedCategory(t, s, userIDFor(t, s, cookie), "cat:food", "Food", "expense")
	seedAccount(t, s, userIDFor(t, s, cookie), "acc:cash", "Cash", "EUR")
	s.do(t, "POST", "/api/sync/push", pushBody("dev-a", txnOpJSON("t1", 1, "dev-a")), cookie)

	res := s.do(t, "GET", "/api/export/transactions.csv", "", cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}

	body := readBody(t, res)
	if !strings.HasPrefix(body, "date,kind,account,category,amount,currency,note\n") {
		t.Fatalf("body = %q", body)
	}
	// txnOpJSON writes expenseData: kind=expense, occurredOn=2026-08-03,
	// amountMinor=-1440, currency=EUR, no category/account id — so names
	// resolve to empty, which must not crash the writer.
	if !strings.Contains(body, "2026-08-03,expense,,,-14.40,EUR,") {
		t.Errorf("body = %q, want the seeded transaction rendered", body)
	}
}

func TestExportMonefyProfile(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	s.do(t, "POST", "/api/sync/push", pushBody("dev-a", txnOpJSON("t1", 1, "dev-a")), cookie)

	res := s.do(t, "GET", "/api/export/transactions.csv?profile=monefy", "", cookie)
	body := readBody(t, res)
	const header = "\xEF\xBB\xBFdate,account,category,amount,currency,converted amount,currency,description\r\n"
	if body != header+"8/3/2026,,,-14.4,EUR,-14.4,EUR,\r\n" {
		t.Fatalf("body = %q", body)
	}
}

// The converted columns are in the base currency for every row, priced at the
// rate on the record's own day; a transfer is left out; and a row with no rate
// yet keeps its own currency there and is counted in a header.
func TestExportMonefyConvertsToTheBaseCurrency(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	userID := userIDFor(t, s, cookie)
	seedCategory(t, s, userID, "cat:food", "Food", "expense")
	seedAccount(t, s, userID, "acc:huf", "HUF", "HUF")
	seedAccount(t, s, userID, "acc:eur", "EUR", "EUR")

	for _, r := range []struct {
		day  string
		rate int64
	}{{"2026-08-01", 400}, {"2026-09-01", 380}} {
		if _, err := s.conn().Rates().Upsert(context.Background(), fx.Rate{
			AsOf: mustDay(t, r.day), Base: "EUR", Quote: "HUF", Rate: big.NewRat(r.rate, 1), Source: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}

	txn := func(id, day string, minor int64, currency, account, kind string) string {
		return fmt.Sprintf(`{"entity":"txn","id":%q,"lamport":1,"deviceId":"dev-a","data":`+
			`{"kind":%q,"occurredOn":%q,"amountMinor":%d,"currency":%q,"categoryId":"cat:food","accountId":%q}}`,
			id, kind, day, minor, currency, account)
	}
	s.do(t, "POST", "/api/sync/push", pushBody("dev-a",
		txn("t1", "2026-07-31", -1000, "HUF", "acc:huf", "expense"), // before any rate
		txn("t2", "2026-08-15", -1000, "HUF", "acc:huf", "expense"), // 400 HUF/EUR
		txn("t3", "2026-09-02", -3800, "HUF", "acc:huf", "expense"), // 380 HUF/EUR
		txn("t4", "2026-09-02", -1250, "EUR", "acc:eur", "expense"),
		txn("t5", "2026-09-03", -5000, "EUR", "acc:eur", "transfer"),
	), cookie)

	res := s.do(t, "GET", "/api/export/transactions.csv?profile=monefy", "", cookie)
	if got := res.Header.Get("X-Moneyfly-Unconverted"); got != "1" {
		t.Errorf("X-Moneyfly-Unconverted = %q, want 1", got)
	}
	body := readBody(t, res)
	want := "\xEF\xBB\xBFdate,account,category,amount,currency,converted amount,currency,description\r\n" +
		"9/2/2026,EUR,Food,-12.5,EUR,-12.5,EUR,\r\n" +
		"7/31/2026,HUF,Food,-1000,HUF,-1000,HUF,\r\n" +
		"8/15/2026,HUF,Food,-1000,HUF,-2.5,EUR,\r\n" +
		"9/2/2026,HUF,Food,-3800,HUF,-10,EUR,\r\n"
	if body != want {
		t.Errorf("body =\n%q\nwant\n%q", body, want)
	}

	dmy := readBody(t, s.do(t, "GET", "/api/export/transactions.csv?profile=monefy-dmy", "", cookie))
	if !strings.Contains(dmy, "\r\n02.09.2026,EUR,Food,-12.5,EUR,-12.5,EUR,\r\n") {
		t.Errorf("monefy-dmy body = %q", dmy)
	}
}

func mustDay(t *testing.T, day string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestExportIsScopedPerUser(t *testing.T) {
	s := newTestServer(t, "")
	a := signUp(t, s, "a@example.com", "dev-a")
	b := signUp(t, s, "b@example.com", "dev-b")
	s.do(t, "POST", "/api/sync/push", pushBody("dev-a", txnOpJSON("t1", 1, "dev-a")), a)

	res := s.do(t, "GET", "/api/export/transactions.csv", "", b)
	body := readBody(t, res)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("b's export has %d lines, want 1 (header only): %q", len(lines), body)
	}
}
