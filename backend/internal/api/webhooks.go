package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"moneyfly/internal/db"
	syncproto "moneyfly/internal/sync"
)

// webhookHTTPClient refuses to connect to a private, loopback or link-local
// address — checked after DNS resolution, not against the URL's hostname
// alone.
//
// A webhook URL is entered by whoever is signed in to this instance, which on
// a multi-user instance is not necessarily the operator, and the server is
// what makes the request: an unchecked URL is a way to make this server probe
// its own network on someone else's behalf. Checking only the hostname is
// exactly what a DNS-rebinding attack defeats — a name that resolves to a
// public address during any validation step and a private one at request
// time — so the check runs inside the dial itself, against the address
// actually being connected to.
var webhookHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		DialContext: dialWebhook,
	},
}

func dialWebhook(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("webhook: %s did not resolve", host)
	}
	for _, ip := range ips {
		if isDisallowedWebhookTarget(ip) {
			return nil, fmt.Errorf("webhook: %s resolves to a disallowed address", host)
		}
	}
	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

func isDisallowedWebhookTarget(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// notifyWebhooks fires every one of a user's webhooks with the transactions a
// push just accepted.
//
// Scoped to the ordinary push path only — not the recurring worker, not the
// CSV importer. A recurring transaction is exactly as predictable as the rule
// that produced it, and an import is a few thousand rows in one burst that
// nobody wants a few thousand deliveries for; both would make "a webhook
// fires when I record something" a much noisier promise than it sounds.
//
// Best-effort and asynchronous: nothing in a request path waits on a
// provider (docs/ARCHITECTURE.md §4b makes the identical argument for FX),
// and an external URL is exactly that. There is no retry queue — this is a
// notification, not a delivery guarantee.
func (s *Server) notifyWebhooks(userID string, ops []syncproto.Op) {
	payload, ok := buildWebhookPayload(ops)
	if !ok {
		return
	}

	hooks, err := s.conn().ListWebhooks(userID)
	if err != nil {
		slog.Error("webhook: listing", "user", userID, "error", err)
		return
	}
	for _, hook := range hooks {
		go deliverWebhook(hook, payload)
	}
}

// buildWebhookPayload turns the ops one push actually applied into the JSON
// body every registered webhook is sent. Split out from notifyWebhooks so the
// "which ops become which JSON" question is testable without any network
// involved — a webhook firing at all is not the same claim as its payload
// being the right one.
//
// ok is false when there is nothing worth a delivery: neither an empty ops
// slice nor a batch that was entirely categories and accounts is a reason to
// look up a user's webhooks at all.
func buildWebhookPayload(ops []syncproto.Op) (payload []byte, ok bool) {
	var txns []map[string]any
	for _, op := range ops {
		if op.Entity != "txn" || op.Deleted {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(op.Data, &body); err != nil {
			continue
		}
		body["id"] = op.ID
		txns = append(txns, body)
	}
	if len(txns) == 0 {
		return nil, false
	}

	payload, err := json.Marshal(map[string]any{"event": "txn.created", "transactions": txns})
	if err != nil {
		slog.Error("webhook: encoding payload", "error", err)
		return nil, false
	}
	return payload, true
}

func deliverWebhook(hook db.Webhook, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		slog.Warn("webhook: building request", "webhook", hook.ID, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Moneyfly-Event", "txn.created")
	req.Header.Set("X-Moneyfly-Signature", signPayload(hook.Secret, payload))

	res, err := webhookHTTPClient.Do(req)
	if err != nil {
		slog.Warn("webhook: delivery failed", "webhook", hook.ID, "error", err)
		return
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		slog.Warn("webhook: receiver rejected the delivery",
			"webhook", hook.ID, "status", res.StatusCode)
	}
}

// signPayload is what lets the receiving end tell this instance sent a
// delivery from anyone who found the URL: HMAC-SHA256 over the exact bytes
// sent, hex-encoded, in X-Moneyfly-Signature.
func signPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
