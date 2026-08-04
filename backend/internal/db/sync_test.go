package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	syncproto "moneyfly/internal/sync"
)

func txnOp(id string, lamport int64, device, note string) syncproto.Op {
	return syncproto.Op{
		Entity:   "txn",
		ID:       id,
		Lamport:  lamport,
		DeviceID: device,
		Data: json.RawMessage(fmt.Sprintf(
			`{"kind":"expense","occurredOn":"2026-08-03","amountMinor":-1440,"currency":"EUR","note":%q}`,
			note)),
	}
}

func tombstone(id string, lamport int64, device string) syncproto.Op {
	return syncproto.Op{Entity: "txn", ID: id, Lamport: lamport, DeviceID: device, Deleted: true}
}

// liveState reads back what a user's replica should converge to.
func liveState(t *testing.T, d *DB, userID string) map[string]string {
	t.Helper()
	rows, err := d.Query(`SELECT id, lamport, device_id, deleted, data FROM txn WHERE user_id = ?`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var id, device, data string
		var lamport int64
		var deleted bool
		if err := rows.Scan(&id, &lamport, &device, &deleted, &data); err != nil {
			t.Fatal(err)
		}
		out[id] = fmt.Sprintf("%d/%s/%t/%s", lamport, device, deleted, data)
	}
	return out
}

func TestApplyInsertsAndLogs(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	res, err := d.ApplyOps(u.ID, []syncproto.Op{txnOp("t1", 1, "dev-a", "coffee")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Lamport != 1 || res.ServerSeq != 1 {
		t.Errorf("lamport = %d, serverSeq = %d; want 1, 1", res.Lamport, res.ServerSeq)
	}

	changes, seq, hasMore, err := d.PullChanges(u.ID, 0, 100)
	if err != nil || len(changes) != 1 || seq != 1 || hasMore {
		t.Fatalf("pull = %v, %d, %v, %v", changes, seq, hasMore, err)
	}
}

// The property the whole protocol rests on: any delivery order of the same ops
// converges to the same state.
func TestConvergenceUnderEveryOrdering(t *testing.T) {
	// Two devices editing an overlapping set of rows, including deletes and a
	// resurrection.
	ops := []syncproto.Op{
		txnOp("t1", 1, "dev-a", "a writes first"),
		txnOp("t1", 2, "dev-b", "b overwrites"),
		txnOp("t2", 1, "dev-b", "b's own row"),
		tombstone("t2", 3, "dev-a"),
		txnOp("t2", 4, "dev-b", "resurrected"),
		txnOp("t3", 7, "dev-a", "a only"),
		txnOp("t1", 7, "dev-b", "tie on lamport 7"),
		txnOp("t1", 7, "dev-a", "tie on lamport 7, lower device"),
	}

	var want map[string]string
	for attempt := range 40 {
		d := openTest(t)
		u := mustUser(t, d, "a@example.com")

		shuffled := append([]syncproto.Op(nil), ops...)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		// Deliver in randomly sized batches too, since a real device pushes in
		// chunks rather than one op at a time.
		for i := 0; i < len(shuffled); {
			n := 1 + rand.IntN(3)
			if i+n > len(shuffled) {
				n = len(shuffled) - i
			}
			if _, err := d.ApplyOps(u.ID, shuffled[i:i+n]); err != nil {
				t.Fatal(err)
			}
			i += n
		}

		got := liveState(t, d, u.ID)
		if attempt == 0 {
			want = got
			continue
		}
		if len(got) != len(want) {
			t.Fatalf("attempt %d: %d rows, want %d", attempt, len(got), len(want))
		}
		for id, v := range want {
			if got[id] != v {
				t.Fatalf("attempt %d: row %s = %q, want %q", attempt, id, got[id], v)
			}
		}
	}

	// And the tie really was broken by device id, not by arrival.
	if want["t1"] == "" {
		t.Fatal("t1 missing")
	}
	if got := want["t1"]; got[:9] != "7/dev-b/f" {
		t.Errorf("t1 = %q, want the lamport-7 write from dev-b to win the tie", got)
	}
}

// Replaying the log must change nothing — this is what makes retrying a push
// after a flaky connection safe.
func TestApplyIsIdempotent(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	ops := []syncproto.Op{txnOp("t1", 1, "dev-a", "x"), txnOp("t1", 2, "dev-a", "y")}
	if _, err := d.ApplyOps(u.ID, ops); err != nil {
		t.Fatal(err)
	}
	before := liveState(t, d, u.ID)

	res, err := d.ApplyOps(u.ID, ops)
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 0 {
		t.Errorf("replay accepted %d ops, want 0", res.Accepted)
	}
	after := liveState(t, d, u.ID)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("state changed on replay:\n before %v\n after  %v", before, after)
	}

	// And it did not grow the journal, which a device would otherwise re-pull
	// forever.
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM change_log WHERE user_id = ?`, u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("change_log has %d rows, want 2", n)
	}
}

// An edit with a higher lamport than the delete genuinely happened later.
func TestEditAfterDeleteResurrects(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if _, err := d.ApplyOps(u.ID, []syncproto.Op{
		txnOp("t1", 1, "dev-a", "original"),
		tombstone("t1", 2, "dev-a"),
		txnOp("t1", 3, "dev-b", "edited on another device"),
	}); err != nil {
		t.Fatal(err)
	}

	var deleted bool
	if err := d.QueryRow(`SELECT deleted FROM txn WHERE user_id = ? AND id = 't1'`, u.ID).
		Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Error("a later edit did not resurrect the row")
	}
}

// One bad op must not cost a device the rest of its batch.
func TestInvalidOpsAreRejectedIndividually(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	res, err := d.ApplyOps(u.ID, []syncproto.Op{
		txnOp("t1", 1, "dev-a", "good"),
		{Entity: "txn", ID: "t2", Lamport: 1, DeviceID: "dev-a", Data: json.RawMessage(`{"kind":"nope"}`)},
		txnOp("t3", 1, "dev-a", "also good"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 2 || len(res.Rejected) != 1 {
		t.Fatalf("accepted %d, rejected %d", res.Accepted, len(res.Rejected))
	}
	if res.Rejected[0].ID != "t2" {
		t.Errorf("rejected %q, want t2", res.Rejected[0].ID)
	}
}

// The scoping invariant, structurally: two accounts using the same row id must
// not touch each other's data.
func TestOpsAreScopedByUser(t *testing.T) {
	d := openTest(t)
	a := mustUser(t, d, "a@example.com")
	b := mustUser(t, d, "b@example.com")

	if _, err := d.ApplyOps(a.ID, []syncproto.Op{txnOp("shared-id", 1, "dev-a", "a's money")}); err != nil {
		t.Fatal(err)
	}
	// b pushes a much higher lamport for the same id — it must not win anything
	// of a's, because it is a different row entirely.
	if _, err := d.ApplyOps(b.ID, []syncproto.Op{txnOp("shared-id", 99, "dev-b", "b's money")}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		user *User
		want string
	}{{a, "a's money"}, {b, "b's money"}} {
		var data string
		if err := d.QueryRow(`SELECT data FROM txn WHERE user_id = ? AND id = 'shared-id'`,
			tc.user.ID).Scan(&data); err != nil {
			t.Fatal(err)
		}
		if !jsonHasNote(data, tc.want) {
			t.Errorf("user %s sees %q, want note %q", tc.user.Email, data, tc.want)
		}
	}

	// And neither sees the other's change log.
	changes, _, _, err := d.PullChanges(a.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Errorf("a sees %d changes, want only its own", len(changes))
	}
}

func jsonHasNote(data, want string) bool {
	var f map[string]any
	if err := json.Unmarshal([]byte(data), &f); err != nil {
		return false
	}
	return f["note"] == want
}

func TestPullPaginates(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	ops := make([]syncproto.Op, 0, 25)
	for i := range 25 {
		ops = append(ops, txnOp(fmt.Sprintf("t%d", i), 1, "dev-a", "x"))
	}
	if _, err := d.ApplyOps(u.ID, ops); err != nil {
		t.Fatal(err)
	}

	seen := 0
	cursor := int64(0)
	for range 10 {
		changes, seq, hasMore, err := d.PullChanges(u.ID, cursor, 10)
		if err != nil {
			t.Fatal(err)
		}
		seen += len(changes)
		cursor = seq
		if !hasMore {
			break
		}
	}
	if seen != 25 {
		t.Errorf("paginated pull returned %d changes, want 25", seen)
	}
	if cursor != 25 {
		t.Errorf("final cursor = %d, want 25", cursor)
	}
}

// Once the journal is trimmed, an old cursor cannot replay — the server must say
// so rather than hand over a silently partial delta.
func TestTrimForcesResync(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if _, err := d.ApplyOps(u.ID, []syncproto.Op{
		txnOp("t1", 1, "dev-a", "old"),
		txnOp("t2", 2, "dev-a", "old"),
	}); err != nil {
		t.Fatal(err)
	}

	// Everything so far is "old".
	removed, err := d.TrimChangeLog(time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("trimmed %d rows, want 2", removed)
	}

	if _, _, _, err := d.PullChanges(u.ID, 1, 100); !errors.Is(err, ErrResyncRequired) {
		t.Errorf("pull with a stale cursor: err = %v, want ErrResyncRequired", err)
	}

	// A device that bootstraps now gets the full state and a usable cursor.
	rows, seq, err := d.SnapshotRows(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("snapshot has %d rows, want 2 — the data must survive trimming", len(rows))
	}
	if _, _, _, err := d.PullChanges(u.ID, seq, 100); err != nil {
		t.Errorf("pull from the snapshot's cursor failed: %v", err)
	}
}

// A fresh device has nothing to delete, so tombstones are dead weight.
func TestSnapshotOmitsTombstones(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if _, err := d.ApplyOps(u.ID, []syncproto.Op{
		txnOp("t1", 1, "dev-a", "kept"),
		txnOp("t2", 1, "dev-a", "removed"),
		tombstone("t2", 2, "dev-a"),
	}); err != nil {
		t.Fatal(err)
	}

	rows, _, err := d.SnapshotRows(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "t1" {
		t.Errorf("snapshot = %+v, want only t1", rows)
	}
}

// The high-water mark only rises: a device that was offline for a month must not
// drag the shared clock back when it finally pushes.
func TestLamportHighWaterOnlyRises(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if _, err := d.ApplyOps(u.ID, []syncproto.Op{txnOp("t1", 50, "dev-a", "x")}); err != nil {
		t.Fatal(err)
	}
	res, err := d.ApplyOps(u.ID, []syncproto.Op{txnOp("t2", 3, "dev-b", "stale device")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Lamport != 50 {
		t.Errorf("lamport = %d, want it held at 50", res.Lamport)
	}
}

// The generated columns are what the reporting and export phases will query, so
// they have to actually track the JSON body.
func TestGeneratedColumnsTrackTheData(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if _, err := d.ApplyOps(u.ID, []syncproto.Op{{
		Entity: "txn", ID: "t1", Lamport: 1, DeviceID: "dev-a",
		Data: json.RawMessage(
			`{"kind":"expense","occurredOn":"2026-08-03","amountMinor":-1440,"currency":"EUR","categoryId":"cat-food"}`),
	}}); err != nil {
		t.Fatal(err)
	}

	var kind, occurredOn, categoryID, currency string
	var amount int64
	if err := d.QueryRow(`
		SELECT kind, occurred_on, category_id, currency, amount_minor
		FROM txn WHERE user_id = ? AND id = 't1'`, u.ID).
		Scan(&kind, &occurredOn, &categoryID, &currency, &amount); err != nil {
		t.Fatal(err)
	}
	if kind != "expense" || occurredOn != "2026-08-03" || categoryID != "cat-food" ||
		currency != "EUR" || amount != -1440 {
		t.Errorf("generated columns = %s/%s/%s/%s/%d", kind, occurredOn, categoryID, currency, amount)
	}

	// And a query through the index finds it.
	var n int
	if err := d.QueryRow(`
		SELECT count(*) FROM txn
		WHERE user_id = ? AND occurred_on BETWEEN '2026-08-01' AND '2026-08-31' AND deleted = 0`,
		u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("month query found %d rows, want 1", n)
	}
}

// Every entity has to survive a round trip, or a later phase discovers its table
// was never wired up.
func TestAllEntitiesRoundTrip(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	ops := []syncproto.Op{
		{Entity: "account", ID: "a1", Lamport: 1, DeviceID: "d", Data: json.RawMessage(`{"name":"Cash","currency":"EUR"}`)},
		{Entity: "category", ID: "c1", Lamport: 1, DeviceID: "d", Data: json.RawMessage(`{"name":"Food","kind":"expense"}`)},
		txnOp("t1", 1, "d", "x"),
		{Entity: "budget", ID: "b1", Lamport: 1, DeviceID: "d", Data: json.RawMessage(`{"limitMinor":50000,"currency":"EUR"}`)},
		{Entity: "recurring_rule", ID: "r1", Lamport: 1, DeviceID: "d", Data: json.RawMessage(`{"freq":"monthly","nextOn":"2026-09-01"}`)},
		{Entity: "user_setting", ID: "view.mode", Lamport: 1, DeviceID: "d", Data: json.RawMessage(`{"value":"donut"}`)},
	}
	res, err := d.ApplyOps(u.ID, ops)
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != len(ops) {
		t.Fatalf("accepted %d of %d: %+v", res.Accepted, len(ops), res.Rejected)
	}

	rows, _, err := d.SnapshotRows(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(ops) {
		t.Fatalf("snapshot returned %d rows, want %d", len(rows), len(ops))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Entity] = true
	}
	for _, e := range syncproto.Entities {
		if !seen[e] {
			t.Errorf("entity %q missing from the snapshot", e)
		}
	}
}
