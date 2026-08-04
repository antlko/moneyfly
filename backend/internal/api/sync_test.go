package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	syncproto "moneyfly/internal/sync"
)

const expenseData = `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":-1440,"currency":"EUR"}`

func pushBody(deviceID string, ops ...string) string {
	body := fmt.Sprintf(`{"deviceId":%q,"ops":[`, deviceID)
	for i, op := range ops {
		if i > 0 {
			body += ","
		}
		body += op
	}
	return body + "]}"
}

func txnOpJSON(id string, lamport int, device string) string {
	return fmt.Sprintf(`{"entity":"txn","id":%q,"lamport":%d,"deviceId":%q,"data":%s}`,
		id, lamport, device, expenseData)
}

// signUp registers an account and returns its session cookie.
func signUp(t *testing.T, s *Server, email, deviceID string) string {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":"hunter2hunter2","deviceId":%q}`, email, deviceID)
	return sessionCookie(t, s.do(t, "POST", "/api/auth/register", body, ""))
}

func TestSyncRequiresAuth(t *testing.T) {
	s := newTestServer(t, "")
	for _, path := range []string{"/api/sync/pull", "/api/sync/snapshot", "/api/sync/events"} {
		if res := s.do(t, "GET", path, "", ""); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s: status = %d, want 401", path, res.StatusCode)
		}
	}
	if res := s.do(t, "POST", "/api/sync/push", pushBody("d"), ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("push: status = %d, want 401", res.StatusCode)
	}
}

func TestPushThenPull(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	res := s.do(t, "POST", "/api/sync/push",
		pushBody("dev-a", txnOpJSON("t1", 1, "dev-a")), cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("push status = %d", res.StatusCode)
	}
	push := decodeBody[pushResponse](t, res)
	if push.Accepted != 1 || push.ServerSeq != 1 || push.Lamport != 1 {
		t.Fatalf("push = %+v", push)
	}

	pull := decodeBody[pullResponse](t, s.do(t, "GET", "/api/sync/pull?since=0", "", cookie))
	if len(pull.Changes) != 1 || pull.Changes[0].ID != "t1" || pull.HasMore {
		t.Fatalf("pull = %+v", pull)
	}

	// Pulling from the returned cursor yields nothing new.
	again := decodeBody[pullResponse](t, s.do(t,
		"GET", fmt.Sprintf("/api/sync/pull?since=%d", pull.ServerSeq), "", cookie))
	if len(again.Changes) != 0 {
		t.Errorf("second pull returned %d changes, want 0", len(again.Changes))
	}
}

// The device that pushed sees its own op come back on pull. That is deliberate:
// filtering by device id would be a fragile optimisation standing in for
// idempotence, which has to hold anyway.
func TestPullReturnsTheCallersOwnOps(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	s.do(t, "POST", "/api/sync/push", pushBody("dev-a", txnOpJSON("t1", 1, "dev-a")), cookie)

	pull := decodeBody[pullResponse](t, s.do(t, "GET", "/api/sync/pull?since=0&deviceId=dev-a", "", cookie))
	if len(pull.Changes) != 1 {
		t.Fatalf("want the caller's own change back, got %d", len(pull.Changes))
	}
}

func TestSnapshotBootstrapsANewDevice(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	s.do(t, "POST", "/api/sync/push",
		pushBody("dev-a", txnOpJSON("t1", 1, "dev-a"), txnOpJSON("t2", 2, "dev-a")), cookie)

	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", cookie))
	if len(snap.Rows) != 2 {
		t.Fatalf("snapshot has %d rows, want 2", len(snap.Rows))
	}
	// The new device must start its clock above everyone else's, or every write
	// it makes loses.
	if snap.Lamport != 2 {
		t.Errorf("lamport = %d, want the high-water mark of 2", snap.Lamport)
	}
	if snap.ServerSeq != 2 {
		t.Errorf("serverSeq = %d, want 2", snap.ServerSeq)
	}
}

// Two accounts on one instance must be invisible to each other, even when they
// use identical row ids.
func TestSyncIsIsolatedBetweenAccounts(t *testing.T) {
	s := newTestServer(t, "")
	a := signUp(t, s, "a@example.com", "dev-a")
	b := signUp(t, s, "b@example.com", "dev-b")

	s.do(t, "POST", "/api/sync/push", pushBody("dev-a", txnOpJSON("shared", 1, "dev-a")), a)
	s.do(t, "POST", "/api/sync/push", pushBody("dev-b", txnOpJSON("shared", 99, "dev-b")), b)

	for name, cookie := range map[string]string{"a": a, "b": b} {
		pull := decodeBody[pullResponse](t, s.do(t, "GET", "/api/sync/pull?since=0", "", cookie))
		if len(pull.Changes) != 1 {
			t.Errorf("account %s sees %d changes, want only its own", name, len(pull.Changes))
		}
	}

	snap := decodeBody[snapshotResponse](t, s.do(t, "GET", "/api/sync/snapshot", "", a))
	if len(snap.Rows) != 1 || snap.Rows[0].Lamport != 1 {
		t.Errorf("account a's snapshot = %+v; b's higher lamport must not have overwritten it", snap.Rows)
	}
}

func TestPushRejectsBadOpsWithoutFailingTheBatch(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	bad := `{"entity":"txn","id":"t2","lamport":1,"deviceId":"dev-a","data":{"kind":"nope"}}`
	res := s.do(t, "POST", "/api/sync/push",
		pushBody("dev-a", txnOpJSON("t1", 1, "dev-a"), bad), cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a bad op is reported, not fatal", res.StatusCode)
	}
	push := decodeBody[pushResponse](t, res)
	if push.Accepted != 1 || len(push.Rejected) != 1 || push.Rejected[0].ID != "t2" {
		t.Errorf("push = %+v", push)
	}
}

// A table name is not a place to accept client input.
func TestPushRejectsUnknownEntity(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	evil := `{"entity":"users","id":"x","lamport":1,"deviceId":"dev-a","data":{}}`
	push := decodeBody[pushResponse](t, s.do(t, "POST", "/api/sync/push", pushBody("dev-a", evil), cookie))
	if push.Accepted != 0 || len(push.Rejected) != 1 {
		t.Fatalf("push = %+v", push)
	}
}

func TestPushRejectsOversizedBatch(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	ops := make([]string, syncproto.MaxOpsPerPush+1)
	for i := range ops {
		ops[i] = txnOpJSON(fmt.Sprintf("t%d", i), 1, "dev-a")
	}
	res := s.do(t, "POST", "/api/sync/push", pushBody("dev-a", ops...), cookie)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", res.StatusCode)
	}
}

func TestPullRejectsBadCursor(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	for _, q := range []string{"since=-1", "since=abc"} {
		if res := s.do(t, "GET", "/api/sync/pull?"+q, "", cookie); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, res.StatusCode)
		}
	}
}

// Pushing updates the device row, so the devices screen shows real progress.
func TestPushTouchesTheDevice(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	s.do(t, "POST", "/api/sync/push", pushBody("dev-a", txnOpJSON("t1", 1, "dev-a")), cookie)

	devices := decodeBody[[]DeviceDTO](t, s.do(t, "GET", "/api/devices", "", cookie))
	if len(devices) != 1 || devices[0].LastSeenAt == 0 {
		t.Errorf("devices = %+v", devices)
	}
}

// --- broker ---------------------------------------------------------------------

func TestBrokerDeliversPerUser(t *testing.T) {
	b := newBroker()
	alice, stopAlice := b.subscribe("alice")
	bob, stopBob := b.subscribe("bob")
	defer stopAlice()
	defer stopBob()

	b.publish("alice", syncEvent{Seq: 7, DeviceID: "dev-a"})

	select {
	case ev := <-alice:
		if ev.Seq != 7 {
			t.Errorf("seq = %d, want 7", ev.Seq)
		}
	default:
		t.Fatal("alice received nothing")
	}
	select {
	case ev := <-bob:
		t.Fatalf("bob received another account's event: %+v", ev)
	default:
	}
}

// A slow reader must not block a write. The pending event already says "there is
// something new", so dropping the second one costs nothing.
func TestBrokerDropsRatherThanBlocks(t *testing.T) {
	b := newBroker()
	ch, stop := b.subscribe("alice")
	defer stop()

	done := make(chan struct{})
	go func() {
		for i := range 100 {
			b.publish("alice", syncEvent{Seq: int64(i)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-timeoutAfterASecond():
		t.Fatal("publish blocked on a full subscriber buffer")
	}
	if len(ch) != 1 {
		t.Errorf("buffered %d events, want exactly 1", len(ch))
	}
}

func TestBrokerUnsubscribes(t *testing.T) {
	b := newBroker()
	_, stop := b.subscribe("alice")
	if n := b.subscriberCount("alice"); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
	stop()
	if n := b.subscriberCount("alice"); n != 0 {
		t.Errorf("subscribers = %d after unsubscribe, want 0", n)
	}
	// Publishing to nobody must not panic on the closed channel.
	b.publish("alice", syncEvent{Seq: 1})
}

func timeoutAfterASecond() <-chan time.Time { return time.After(time.Second) }
