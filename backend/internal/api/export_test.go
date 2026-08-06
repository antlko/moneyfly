package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
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
	if !strings.HasPrefix(body, "date,account,category,amount,currency,converted amount,currency,description\n") {
		t.Fatalf("body = %q", body)
	}
	if !strings.Contains(body, "03.08.2026,") {
		t.Errorf("body = %q, want the DD.MM.YYYY date", body)
	}
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
