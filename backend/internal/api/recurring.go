package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"moneyfly/internal/db"
	syncproto "moneyfly/internal/sync"
)

// serverDeviceID marks a change the server made on someone's behalf rather
// than any browser — this worker today, the CSV importer as of phase 8, and
// any future background writer besides. It takes part in the Lamport
// tiebreak like any device id, but in practice never needs to: a freshly
// written row starts at lamport 1 and an existing row's advance is
// `stored lamport + 1`, so a person's own edit — at their device's
// already-higher clock — wins outright on lamport alone, the ordinary case
// the whole protocol is built around (docs/SYNC.md). See CLAUDE.md's
// "Conventions that bite" — reuse this constant rather than inventing a
// second sentinel.
const serverDeviceID = "server"

// maxCatchUpPerTick bounds how many occurrences one rule may produce in a
// single pass. A rule is normally at most one tick behind; this only matters
// for a rule that has sat broken for a long time, and it exists to keep one
// bad row from consuming a whole tick rather than as a limit anyone is meant
// to hit. Whatever is left over is picked up on the next tick.
const maxCatchUpPerTick = 500

// recurringLoop materialises due recurring_rule rows into transactions, once
// at startup and then hourly.
//
// Hourly rather than anchored to a wall-clock minute the way fxLoop is:
// nothing here depends on firing at a precise time, only on having run at
// least once since the day changed, and running it a few extra times over one
// day is harmless — materialisation is idempotent (see TxnNaturalKeyExists).
func (s *Server) recurringLoop() {
	run := func() { s.materialiseDue(time.Now().UTC().Format("2006-01-02")) }
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			run()
		}
	}
}

// materialiseDue finds every rule due on or before `today` across every user
// and processes each independently, so one user's broken rule cannot stop
// another's from firing.
//
// `today` is a parameter rather than read from time.Now() internally so a
// test can drive it without depending on wall-clock timing — the same reason
// nextOccurrence in fx.go takes `from` explicitly.
func (s *Server) materialiseDue(today string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rules, err := s.conn().DueRecurringRules(ctx, today)
	if err != nil {
		slog.Error("recurring: reading due rules", "error", err)
		return
	}

	for _, rule := range rules {
		ops, err := s.buildRecurringOps(ctx, rule, today)
		if err != nil {
			// One rule's bad data must not stop the rest of the tick — it is
			// retried, and logged again, on every tick until it is fixed.
			slog.Error("recurring: building ops", "rule", rule.ID, "user", rule.UserID, "error", err)
			continue
		}
		if len(ops) == 0 {
			continue
		}

		// Chunked defensively, not because this path can currently overflow:
		// maxCatchUpPerTick caps a rule at 500 occurrences plus its own advance,
		// comfortably under syncproto.MaxOpsPerPush. The two constants are
		// declared in different packages for different reasons, though, and
		// nothing but arithmetic keeps them in that order — raise the catch-up
		// cap past the op limit and this rule would 500 here, never advance its
		// `nextOn`, and retry the identical failing batch on every tick
		// afterwards, wedged permanently behind a single log line.
		// TestCatchUpCapStaysUnderTheOpLimit guards the relationship; this
		// guards the consequence.
		accepted, lastSeq, err := applyInChunks(
			func(chunk []syncproto.Op) (db.ApplyResult, error) {
				return s.conn().ApplyOps(rule.UserID, chunk)
			}, ops)
		if err != nil {
			slog.Error("recurring: applying ops", "rule", rule.ID, "user", rule.UserID,
				"accepted", accepted, "error", err)
			// Fall through rather than `continue`: whatever landed still has to
			// be published, exactly as in the import path.
		}
		if accepted == 0 {
			continue
		}
		slog.Info("recurring: materialised", "rule", rule.ID, "user", rule.UserID,
			"transactions", len(ops)-1)
		// No originating device to skip here, unlike a client's own push — the
		// transaction just created sits on no device yet, so every one of the
		// user's devices, including whichever created the rule, needs the pull.
		s.events.publish(rule.UserID, syncEvent{Seq: lastSeq, DeviceID: serverDeviceID})
	}
}

// buildRecurringOps turns one due rule into the ops that bring it up to date:
// one new transaction per occurrence not yet materialised, plus one update
// advancing the rule's own next_on past `today`.
//
// A rule that is several periods behind — the server was down, most likely —
// catches up fully rather than skipping to today, because those were real
// missed days and the honest ledger has one transaction for each. Each
// candidate occurrence is checked against its natural key first (below)
// specifically so a crash partway through this loop cannot double-post on the
// next tick.
func (s *Server) buildRecurringOps(ctx context.Context, rule db.DueRule, today string) ([]syncproto.Op, error) {
	data, err := decodeRuleData(rule.Data)
	if err != nil {
		return nil, fmt.Errorf("rule %s: %w", rule.ID, err)
	}
	freq, _ := data["freq"].(string)
	nextOn, _ := data["nextOn"].(string)

	var ops []syncproto.Op
	occurrences := 0
	for nextOn <= today && occurrences < maxCatchUpPerTick {
		// The check-then-insert here is the same structural de-duplication
		// idx_txn_natural_key exists for (see docs/SYNC.md "How rows are
		// stored"): never a constraint, because the write path must not be able
		// to fail on data.
		naturalKey := "recurring:" + rule.ID + ":" + nextOn
		exists, err := s.conn().TxnNaturalKeyExists(ctx, rule.UserID, naturalKey)
		if err != nil {
			return nil, err
		}
		if !exists {
			op, err := newTxnOp(data, nextOn, naturalKey)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %w", rule.ID, err)
			}
			ops = append(ops, op)
		}

		advanced, err := advanceDate(nextOn, freq)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", rule.ID, err)
		}
		nextOn = advanced
		occurrences++
	}
	if occurrences == 0 {
		return nil, nil
	}
	if occurrences >= maxCatchUpPerTick {
		slog.Warn("recurring: rule hit the catch-up cap, continuing next tick",
			"rule", rule.ID, "limit", maxCatchUpPerTick)
	}

	data["nextOn"] = nextOn
	newBody, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("rule %s: encoding advanced rule: %w", rule.ID, err)
	}
	return append(ops, syncproto.Op{
		Entity:   "recurring_rule",
		ID:       rule.ID,
		Lamport:  rule.Lamport + 1,
		DeviceID: serverDeviceID,
		Data:     newBody,
	}), nil
}

// newTxnOp builds the op for one materialised occurrence. Fields are copied
// verbatim from the rule's own body — a rule is validated against exactly
// txn's required shape (see validateRecurring in internal/sync/op.go) for
// this reason.
func newTxnOp(rule map[string]any, occurredOn, naturalKey string) (syncproto.Op, error) {
	body, err := json.Marshal(map[string]any{
		"kind":        rule["kind"],
		"occurredOn":  occurredOn,
		"amountMinor": rule["amountMinor"],
		"currency":    rule["currency"],
		"categoryId":  rule["categoryId"],
		"accountId":   rule["accountId"],
		"note":        rule["note"],
		"naturalKey":  naturalKey,
	})
	if err != nil {
		return syncproto.Op{}, err
	}
	return syncproto.Op{
		Entity:   "txn",
		ID:       db.NewID(),
		Lamport:  1,
		DeviceID: serverDeviceID,
		Data:     body,
	}, nil
}

// decodeRuleData parses a rule's stored JSON with UseNumber, so amountMinor
// round-trips as an exact integer rather than a float64 that could lose
// precision when it is re-marshalled into the materialised transaction —
// money is integer minor units everywhere, never a float.
func decodeRuleData(data string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding rule data: %w", err)
	}
	return out, nil
}

// advanceDate moves a YYYY-MM-DD date forward by one period of freq.
//
// The error case only fires on data that internal/sync.Validate has already
// rejected before it could reach here — freq is checked to be one of these
// four, nextOn to be a real date — so this exists as a backstop, not as
// input handling, to guarantee forward progress and rule out ever building
// two ops for the same occurrence within one tick.
func advanceDate(day, freq string) (string, error) {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("nextOn %q is not a date: %w", day, err)
	}
	switch freq {
	case "daily":
		t = t.AddDate(0, 0, 1)
	case "weekly":
		t = t.AddDate(0, 0, 7)
	case "monthly":
		t = addMonthsClamped(t, 1)
	case "yearly":
		t = addMonthsClamped(t, 12)
	default:
		return "", fmt.Errorf("freq %q is not daily/weekly/monthly/yearly", freq)
	}
	return t.Format("2006-01-02"), nil
}

// addMonthsClamped adds calendar months, clamping the day to the last valid
// day of the resulting month instead of overflowing into the month after.
//
// time.Date normalises an out-of-range day by rolling the calendar forward,
// so a plain t.AddDate(0, 1, 0) takes 31 January to 3 March, not 28 February
// — silently drifting a rent payment from the last day of the month to the
// third of the next one is exactly the kind of bug nobody notices until a
// total is wrong. The same overflow hits 29 February plus one year.
func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	firstOfTarget := time.Date(y, m+time.Month(months), 1, 0, 0, 0, 0, t.Location())
	lastDayOfTarget := firstOfTarget.AddDate(0, 1, -1).Day()
	if d > lastDayOfTarget {
		d = lastDayOfTarget
	}
	return time.Date(firstOfTarget.Year(), firstOfTarget.Month(), d, 0, 0, 0, 0, t.Location())
}
