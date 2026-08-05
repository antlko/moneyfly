package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// body reads a response as text, for assertions about the JSON *shape* rather
// than about decoded values — which is the only way to catch a nil slice going
// out as `null`.
func body(t *testing.T, res *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(raw)
}

// A successful push must send `"rejected": []`, never `"rejected": null`.
//
// This is not tidiness. The client's own type says the field is an array and it
// reads `.length` on the happy path, so `null` threw a TypeError on every
// successful push — which the engine classified as a transport failure and
// reported as "Offline" while every request was returning 200. Nothing was
// wrong with the network, so nothing about the symptom pointed at this.
func TestPushSendsAnEmptyRejectionListNotNull(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	for _, tc := range []struct{ name, ops string }{
		{"nothing to send", pushBody("dev-a")},
		{"one good op", pushBody("dev-a", txnOpJSON("t1", 1, "dev-a"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := body(t, s.do(t, "POST", "/api/sync/push", tc.ops, cookie))
			if strings.Contains(got, `"rejected":null`) {
				t.Errorf("push responded %s; want \"rejected\":[]", got)
			}
			if !strings.Contains(got, `"rejected":[]`) {
				t.Errorf("push responded %s; want an empty array", got)
			}
		})
	}
}

func TestSetRateStoresAHandEnteredRate(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	res := s.do(t, "PUT", "/api/fx/rates",
		`{"quote":"HUF","rate":"391.5","asOf":"2026-08-04"}`, cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", res.StatusCode, body(t, res))
	}
	got := decodeBody[RateDTO](t, res)
	if got.Quote != "HUF" || got.Rate != "391.5" || got.Base != "EUR" {
		t.Fatalf("stored = %+v", got)
	}
	if got.Source != "manual" {
		t.Errorf("source = %q, want %q — a typed rate must be distinguishable", got.Source, "manual")
	}

	// And it comes back out of the read endpoints the client actually uses.
	history := decodeBody[struct{ Rates []RateDTO }](t,
		s.do(t, "GET", "/api/fx/rates?quote=HUF&from=2026-08-01&to=2026-08-31", "", cookie))
	if len(history.Rates) != 1 || history.Rates[0].Rate != "391.5" {
		t.Fatalf("history = %+v", history.Rates)
	}
	latest := decodeBody[struct{ Rates []RateDTO }](t, s.do(t, "GET", "/api/fx/latest", "", cookie))
	if len(latest.Rates) != 1 || latest.Rates[0].Quote != "HUF" {
		t.Fatalf("latest = %+v", latest.Rates)
	}
}

// Re-entering a rate corrects it rather than accumulating rows, and the
// correction is what every lookup then returns.
func TestSetRateIsAnEdit(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	for _, rate := range []string{"380", "391.5"} {
		if res := s.do(t, "PUT", "/api/fx/rates",
			`{"quote":"HUF","rate":"`+rate+`","asOf":"2026-08-04"}`, cookie); res.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s: status = %d", rate, res.StatusCode)
		}
	}
	history := decodeBody[struct{ Rates []RateDTO }](t,
		s.do(t, "GET", "/api/fx/rates?quote=HUF&from=2026-08-01&to=2026-08-31", "", cookie))
	if len(history.Rates) != 1 || history.Rates[0].Rate != "391.5" {
		t.Fatalf("history = %+v, want one row reading 391.5", history.Rates)
	}
}

func TestSetRateRejectsNonsense(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	for _, tc := range []struct{ name, in string }{
		{"not a currency code", `{"quote":"HUFF","rate":"391.5"}`},
		{"not a number", `{"quote":"HUF","rate":"about four hundred"}`},
		{"zero", `{"quote":"HUF","rate":"0"}`},
		{"negative", `{"quote":"HUF","rate":"-391.5"}`},
		{"impossible date", `{"quote":"HUF","rate":"391.5","asOf":"04/08/2026"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := s.do(t, "PUT", "/api/fx/rates", tc.in, cookie)
			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", res.StatusCode)
			}
		})
	}
}

func TestSetRateRequiresAuth(t *testing.T) {
	s := newTestServer(t, "")
	res := s.do(t, "PUT", "/api/fx/rates", `{"quote":"HUF","rate":"391.5"}`, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}
