package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	syncproto "moneyfly/internal/sync"
)

func TestWebhooksRequireAuth(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "GET", "/api/webhooks", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET: status = %d, want 401", res.StatusCode)
	}
}

func TestCreateWebhookRejectsBadInput(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	tests := []string{
		`{"name":"","url":"https://example.com/hook"}`,
		`{"name":"x","url":"ftp://example.com/hook"}`,
		`{"name":"x","url":"not a url"}`,
	}
	for _, body := range tests {
		if res := s.do(t, "POST", "/api/webhooks", body, cookie); res.StatusCode != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, res.StatusCode)
		}
	}
}

func TestWebhookCRUD(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	created := decodeBody[webhookDTO](t, s.do(t, "POST", "/api/webhooks",
		`{"name":"my server","url":"https://example.com/hook"}`, cookie))
	if created.Secret == "" {
		t.Fatal("no secret in the create response")
	}

	list := decodeBody[[]webhookDTO](t, s.do(t, "GET", "/api/webhooks", "", cookie))
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	del := s.do(t, "DELETE", "/api/webhooks/"+created.ID, "", cookie)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	after := decodeBody[[]webhookDTO](t, s.do(t, "GET", "/api/webhooks", "", cookie))
	if len(after) != 0 {
		t.Errorf("webhook still listed after delete: %+v", after)
	}
}

func TestDeleteWebhookIsScopedPerUser(t *testing.T) {
	s := newTestServer(t, "")
	a := signUp(t, s, "a@example.com", "dev-a")
	b := signUp(t, s, "b@example.com", "dev-b")
	created := decodeBody[webhookDTO](t, s.do(t, "POST", "/api/webhooks",
		`{"name":"a's","url":"https://example.com/hook"}`, a))

	s.do(t, "DELETE", "/api/webhooks/"+created.ID, "", b)

	list := decodeBody[[]webhookDTO](t, s.do(t, "GET", "/api/webhooks", "", a))
	if len(list) != 1 {
		t.Errorf("a's webhook was removed by b's delete: %+v", list)
	}
}

// --- payload building ------------------------------------------------------------

func txnOpFor(id string, data string) syncproto.Op {
	return syncproto.Op{Entity: "txn", ID: id, Lamport: 1, DeviceID: "dev-a", Data: json.RawMessage(data)}
}

func TestBuildWebhookPayloadIncludesOnlyLiveTransactions(t *testing.T) {
	ops := []syncproto.Op{
		txnOpFor("t1", expenseData),
		{Entity: "category", ID: "c1", Lamport: 1, DeviceID: "dev-a", Data: json.RawMessage(`{"name":"Food","kind":"expense"}`)},
		{Entity: "txn", ID: "t2", Lamport: 1, DeviceID: "dev-a", Deleted: true, Data: json.RawMessage(`{}`)},
	}
	payload, ok := buildWebhookPayload(ops)
	if !ok {
		t.Fatal("expected a payload")
	}
	var decoded struct {
		Event        string           `json:"event"`
		Transactions []map[string]any `json:"transactions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Event != "txn.created" || len(decoded.Transactions) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Transactions[0]["id"] != "t1" {
		t.Errorf("transactions = %+v, want only t1 (not the category, not the deleted txn)", decoded.Transactions)
	}
}

func TestBuildWebhookPayloadEmptyForNonTxnOps(t *testing.T) {
	ops := []syncproto.Op{
		{Entity: "budget", ID: "b1", Lamport: 1, DeviceID: "dev-a", Data: json.RawMessage(`{"limitMinor":1,"currency":"EUR"}`)},
	}
	if _, ok := buildWebhookPayload(ops); ok {
		t.Error("expected no payload for a batch with no transactions")
	}
	if _, ok := buildWebhookPayload(nil); ok {
		t.Error("expected no payload for an empty batch")
	}
}

// --- signing -----------------------------------------------------------------------

func TestSignPayloadIsDeterministicAndKeyed(t *testing.T) {
	a := signPayload("secret-1", []byte("hello"))
	b := signPayload("secret-1", []byte("hello"))
	c := signPayload("secret-2", []byte("hello"))
	if a != b {
		t.Error("same secret and payload produced different signatures")
	}
	if a == c {
		t.Error("different secrets produced the same signature")
	}
}

// --- SSRF protection -----------------------------------------------------------------

func TestIsDisallowedWebhookTarget(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.5", true},
		{"172.16.0.5", true},
		{"192.168.1.5", true},
		{"169.254.169.254", true}, // cloud metadata endpoints live here
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("ParseIP(%q) failed", tt.ip)
			}
			if got := isDisallowedWebhookTarget(ip); got != tt.want {
				t.Errorf("isDisallowedWebhookTarget(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

// --- end-to-end delivery -------------------------------------------------------------

// A push that creates a transaction must actually reach a registered
// webhook, signed, with the right event header.
//
// This swaps webhookHTTPClient for a plain client with no dial restriction,
// for the run of this test only: the production client refuses loopback
// addresses on purpose (isDisallowedWebhookTarget above), and httptest.Server
// listens on 127.0.0.1, so the real client would reject the very server this
// test uses to observe a delivery. The restriction itself is what
// TestIsDisallowedWebhookTarget checks directly.
func TestPushDeliversToRegisteredWebhook(t *testing.T) {
	received := make(chan *http.Request, 1)
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = b
		received <- r
	}))
	defer srv.Close()

	orig := webhookHTTPClient
	webhookHTTPClient = srv.Client()
	t.Cleanup(func() { webhookHTTPClient = orig })

	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	hook := decodeBody[webhookDTO](t, s.do(t, "POST", "/api/webhooks",
		`{"name":"test","url":"`+srv.URL+`"}`, cookie))

	s.do(t, "POST", "/api/sync/push", pushBody("dev-a", txnOpJSON("t1", 1, "dev-a")), cookie)

	select {
	case r := <-received:
		if r.Header.Get("X-Moneyfly-Event") != "txn.created" {
			t.Errorf("event header = %q", r.Header.Get("X-Moneyfly-Event"))
		}
		want := signPayload(hook.Secret, body)
		if got := r.Header.Get("X-Moneyfly-Signature"); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not delivered within 2s")
	}
}
