package api

import (
	"context"
	"encoding/json"
	"testing"

	syncproto "moneyfly/internal/sync"
)

func TestAdvanceDate(t *testing.T) {
	tests := []struct {
		name, day, freq, want string
	}{
		{"daily", "2026-08-03", "daily", "2026-08-04"},
		{"weekly", "2026-08-03", "weekly", "2026-08-10"},
		{"monthly ordinary", "2026-08-03", "monthly", "2026-09-03"},
		// The clamp this function exists for: naively adding a month to the 31st
		// overflows into March, because Go's time.AddDate normalises an
		// out-of-range day forward instead of stopping at the month's end.
		{"monthly clamps into a shorter month", "2026-01-31", "monthly", "2026-02-28"},
		{"monthly clamps into February of a leap year", "2028-01-31", "monthly", "2028-02-29"},
		{"monthly from the last day carries the last day forward", "2026-04-30", "monthly", "2026-05-30"},
		{"yearly ordinary", "2026-08-03", "yearly", "2027-08-03"},
		// Same overflow, once a year instead of once a month.
		{"yearly clamps 29 February into a non-leap year", "2028-02-29", "yearly", "2029-02-28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := advanceDate(tt.day, tt.freq)
			if err != nil {
				t.Fatalf("advanceDate(%q, %q): %v", tt.day, tt.freq, err)
			}
			if got != tt.want {
				t.Errorf("advanceDate(%q, %q) = %q, want %q", tt.day, tt.freq, got, tt.want)
			}
		})
	}
}

func TestAdvanceDateRejectsBadInput(t *testing.T) {
	if _, err := advanceDate("2026-08-03", "fortnightly"); err == nil {
		t.Error("unknown freq accepted")
	}
	if _, err := advanceDate("03.08.2026", "daily"); err == nil {
		t.Error("non-ISO date accepted")
	}
}

// Every integration test below anchors on a date far in the future rather
// than near the real one.
//
// newTestServer starts the real recurringLoop goroutine, whose first pass
// runs immediately against the actual wall-clock date — so a rule seeded due
// "today" races that automatic pass and can be double-materialised by both.
// Anchoring everything a lifetime away keeps the automatic pass's real-now
// query from ever seeing these rules as due, which is what makes calling
// materialiseDue explicitly, with a fixed date, actually deterministic.
const testToday = "2099-06-15"

// ruleOp builds a recurring_rule op with the fields the worker reads. Kept
// separate from the txn helpers in sync_test.go because a rule additionally
// carries freq and nextOn in place of occurredOn.
func ruleOp(id, freq, nextOn string, lamport int64) syncproto.Op {
	data, _ := json.Marshal(map[string]any{
		"kind":        "expense",
		"freq":        freq,
		"nextOn":      nextOn,
		"amountMinor": -500,
		"currency":    "EUR",
		"categoryId":  "cat:food",
		"accountId":   "acc:cash",
		"note":        "coffee",
	})
	return syncproto.Op{Entity: "recurring_rule", ID: id, Lamport: lamport, DeviceID: "dev-a", Data: data}
}

func snapshotByEntity(t *testing.T, s *Server, userID, entity string) []syncproto.Change {
	t.Helper()
	rows, _, err := s.conn().SnapshotRows(userID)
	if err != nil {
		t.Fatalf("SnapshotRows: %v", err)
	}
	var out []syncproto.Change
	for _, r := range rows {
		if r.Entity == entity {
			out = append(out, r)
		}
	}
	return out
}

// What actually keeps the recurring worker safe from db.ApplyOps' batch limit
// is that one tick can never build a batch that large: maxCatchUpPerTick bounds
// the occurrences, plus one op for the rule's own advance.
//
// Nothing enforces that ordering but arithmetic, and the two constants live in
// different packages for unrelated reasons. Raise the catch-up cap past the op
// limit and a long-dormant rule 500s on every tick forever, advancing nothing,
// visible only as a log line. This is the guard on that.
func TestCatchUpCapStaysUnderTheOpLimit(t *testing.T) {
	const opsPerTick = maxCatchUpPerTick + 1 // occurrences + the rule advance
	if opsPerTick > syncproto.MaxOpsPerPush {
		t.Fatalf("one tick can build %d ops but ApplyOps takes at most %d — "+
			"a catching-up rule would be rejected wholesale and wedge itself",
			opsPerTick, syncproto.MaxOpsPerPush)
	}
}

// A rule dormant for years materialises up to the per-tick cap and, crucially,
// still advances — so the next tick makes progress rather than redoing the same
// work. Restoring an old backup is the ordinary way to land here.
func TestMaterialiseDueAdvancesThroughALongCatchUp(t *testing.T) {
	s := newTestServer(t, "")
	user, err := s.conn().CreateUser("catchup@example.com", "hash", "A", "EUR")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Far enough back that the catch-up saturates the per-tick cap.
	if _, err := s.conn().ApplyOps(user.ID, []syncproto.Op{
		ruleOp("rule-long", "daily", "2094-01-01", 1),
	}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	s.materialiseDue(testToday)

	txns := snapshotByEntity(t, s, user.ID, "txn")
	if len(txns) != maxCatchUpPerTick {
		t.Fatalf("got %d transactions, want the per-tick cap of %d",
			len(txns), maxCatchUpPerTick)
	}

	rules := snapshotByEntity(t, s, user.ID, "recurring_rule")
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	var body struct {
		NextOn string `json:"nextOn"`
	}
	if err := json.Unmarshal(rules[0].Data, &body); err != nil {
		t.Fatalf("unmarshal rule: %v", err)
	}
	// Not past today — the cap stopped it early — but it must have moved, or
	// the rule re-materialises the identical batch forever.
	if body.NextOn <= "2094-01-01" {
		t.Errorf("nextOn = %q, want advanced past the starting date — a rule "+
			"that does not advance repeats the same catch-up on every tick", body.NextOn)
	}
}

// A rule several days behind must produce one transaction per missed day, not
// just the most recent one — the server being briefly unreachable must not
// quietly drop days from the ledger.
func TestMaterialiseDueCatchesUpEveryMissedOccurrence(t *testing.T) {
	s := newTestServer(t, "")
	user, err := s.conn().CreateUser("a@example.com", "hash", "A", "EUR")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := s.conn().ApplyOps(user.ID, []syncproto.Op{
		ruleOp("rule-1", "daily", "2099-06-12", 1),
	}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	s.materialiseDue(testToday)

	txns := snapshotByEntity(t, s, user.ID, "txn")
	if len(txns) != 4 {
		t.Fatalf("got %d transactions, want one for each of 12/13/14/15 June", len(txns))
	}
	seen := map[string]bool{}
	for _, txn := range txns {
		var body struct {
			OccurredOn  string `json:"occurredOn"`
			AmountMinor int64  `json:"amountMinor"`
			NaturalKey  string `json:"naturalKey"`
		}
		if err := json.Unmarshal(txn.Data, &body); err != nil {
			t.Fatalf("unmarshal txn: %v", err)
		}
		seen[body.OccurredOn] = true
		if body.AmountMinor != -500 {
			t.Errorf("txn %s amountMinor = %d, want -500 (copied verbatim, not re-typed as float)",
				body.OccurredOn, body.AmountMinor)
		}
		if body.NaturalKey == "" {
			t.Errorf("txn %s has no naturalKey", body.OccurredOn)
		}
	}
	for _, day := range []string{"2099-06-12", "2099-06-13", "2099-06-14", "2099-06-15"} {
		if !seen[day] {
			t.Errorf("missing the occurrence for %s", day)
		}
	}

	rules := snapshotByEntity(t, s, user.ID, "recurring_rule")
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	var ruleBody struct {
		NextOn string `json:"nextOn"`
	}
	if err := json.Unmarshal(rules[0].Data, &ruleBody); err != nil {
		t.Fatalf("unmarshal rule: %v", err)
	}
	if ruleBody.NextOn != "2099-06-16" {
		t.Errorf("rule nextOn = %q, want 2099-06-16", ruleBody.NextOn)
	}
	if rules[0].Lamport != 2 {
		t.Errorf("rule lamport = %d, want 2 (one advance past the seeded version)", rules[0].Lamport)
	}
}

// The scenario TxnNaturalKeyExists exists for: the worker creates the
// transactions but the process dies before the rule's next_on is advanced —
// simulated here by applying only the leading ops from one materialisation
// pass. The following real pass must recover to the correct final state
// without duplicating anything already posted.
func TestMaterialiseDueRecoversFromACrashBetweenTxnAndAdvance(t *testing.T) {
	s := newTestServer(t, "")
	user, err := s.conn().CreateUser("a@example.com", "hash", "A", "EUR")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := s.conn().ApplyOps(user.ID, []syncproto.Op{
		ruleOp("rule-1", "daily", "2099-06-12", 1),
	}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	ctx := context.Background()
	due, err := s.conn().DueRecurringRules(ctx, testToday)
	if err != nil || len(due) != 1 {
		t.Fatalf("DueRecurringRules: %v, %d rules", err, len(due))
	}
	ops, err := s.buildRecurringOps(ctx, due[0], testToday)
	if err != nil {
		t.Fatalf("buildRecurringOps: %v", err)
	}
	if len(ops) != 5 { // 4 transactions + the rule's own advance
		t.Fatalf("got %d ops, want 5", len(ops))
	}
	// Apply everything except the last op — the rule update — so the row is
	// left exactly where a mid-tick crash would leave it: four transactions
	// posted, next_on still at its original value.
	if _, err := s.conn().ApplyOps(user.ID, ops[:len(ops)-1]); err != nil {
		t.Fatalf("apply partial batch: %v", err)
	}
	if got := len(snapshotByEntity(t, s, user.ID, "txn")); got != 4 {
		t.Fatalf("got %d transactions after the partial batch, want 4", got)
	}

	// The real path: it re-reads the still-stale rule and must not re-post any
	// of the four transactions it already sees by natural key.
	s.materialiseDue(testToday)

	txns := snapshotByEntity(t, s, user.ID, "txn")
	if len(txns) != 4 {
		t.Fatalf("got %d transactions after recovery, want still 4 (no duplicates)", len(txns))
	}
	rules := snapshotByEntity(t, s, user.ID, "recurring_rule")
	var ruleBody struct {
		NextOn string `json:"nextOn"`
	}
	if err := json.Unmarshal(rules[0].Data, &ruleBody); err != nil {
		t.Fatalf("unmarshal rule: %v", err)
	}
	if ruleBody.NextOn != "2099-06-16" {
		t.Errorf("rule nextOn = %q, want 2099-06-16 (still advanced despite the recovery path)",
			ruleBody.NextOn)
	}
}

// A rule that is not due yet must be left alone.
func TestMaterialiseDueSkipsRulesNotYetDue(t *testing.T) {
	s := newTestServer(t, "")
	user, err := s.conn().CreateUser("a@example.com", "hash", "A", "EUR")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := s.conn().ApplyOps(user.ID, []syncproto.Op{
		ruleOp("rule-1", "monthly", "2099-07-01", 1),
	}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	s.materialiseDue(testToday)

	if got := len(snapshotByEntity(t, s, user.ID, "txn")); got != 0 {
		t.Errorf("got %d transactions for a rule not yet due, want 0", got)
	}
}

// Two users' rules must never cross-pollinate — the same isolation
// TestSyncIsIsolatedBetweenAccounts checks for an ordinary push.
func TestMaterialiseDueStaysScopedPerUser(t *testing.T) {
	s := newTestServer(t, "")
	alice, err := s.conn().CreateUser("alice@example.com", "hash", "Alice", "EUR")
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := s.conn().CreateUser("bob@example.com", "hash", "Bob", "EUR")
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	if _, err := s.conn().ApplyOps(alice.ID, []syncproto.Op{
		ruleOp("rule-1", "daily", testToday, 1),
	}); err != nil {
		t.Fatalf("seed alice's rule: %v", err)
	}

	s.materialiseDue(testToday)

	if got := len(snapshotByEntity(t, s, alice.ID, "txn")); got != 1 {
		t.Errorf("alice has %d transactions, want 1", got)
	}
	if got := len(snapshotByEntity(t, s, bob.ID, "txn")); got != 0 {
		t.Errorf("bob has %d transactions from alice's rule, want 0", got)
	}
}
