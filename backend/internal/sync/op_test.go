package sync

import (
	"encoding/json"
	"testing"
)

func TestWins(t *testing.T) {
	tests := []struct {
		name                    string
		inLamport, storedLampor int64
		inDevice, storedDevice  string
		want                    bool
	}{
		{"higher lamport wins", 5, 4, "a", "z", true},
		{"lower lamport loses", 4, 5, "z", "a", false},
		{"tie broken by greater device id", 5, 5, "z", "a", true},
		{"tie broken against lesser device id", 5, 5, "a", "z", false},
		// Strictness is what makes replay idempotent: an op identical to what is
		// stored must not "win" and re-log itself.
		{"identical version does not win", 5, 5, "a", "a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Wins(tt.inLamport, tt.inDevice, tt.storedLampor, tt.storedDevice)
			if got != tt.want {
				t.Errorf("Wins = %v, want %v", got, tt.want)
			}
		})
	}
}

// Whatever order two devices' writes arrive in, both sides must pick the same
// winner. This is the property the whole protocol rests on.
func TestWinsIsATotalOrder(t *testing.T) {
	type version struct {
		lamport int64
		device  string
	}
	versions := []version{
		{1, "a"}, {1, "b"}, {2, "a"}, {2, "b"}, {3, "a"}, {10, "a"}, {10, "z"},
	}
	for _, x := range versions {
		for _, y := range versions {
			xy := Wins(x.lamport, x.device, y.lamport, y.device)
			yx := Wins(y.lamport, y.device, x.lamport, x.device)
			switch {
			case x == y && (xy || yx):
				t.Errorf("%v vs itself: both directions must be false", x)
			case x != y && xy == yx:
				t.Errorf("%v vs %v: exactly one direction must win, got %v/%v", x, y, xy, yx)
			}
		}
	}
}

func op(entity, id string, data string) Op {
	return Op{Entity: entity, ID: id, Lamport: 1, DeviceID: "dev-1", Data: json.RawMessage(data)}
}

func TestValidateAcceptsGoodOps(t *testing.T) {
	good := []Op{
		op("txn", "t1", `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":-1440,"currency":"EUR"}`),
		op("txn", "t2", `{"kind":"transfer","occurredOn":"2026-08-03","amountMinor":-1000,"currency":"EUR","toAmountMinor":900,"toCurrency":"USD"}`),
		op("account", "a1", `{"name":"Cash","currency":"HUF"}`),
		op("category", "c1", `{"name":"Food","kind":"expense"}`),
		op("budget", "b1", `{"limitMinor":50000,"currency":"EUR"}`),
		op("budget", "b2", `{"limitMinor":50000,"currency":"EUR","period":"2026-08"}`),
		op("recurring_rule", "r1", `{"kind":"expense","freq":"monthly","nextOn":"2026-09-01","amountMinor":-1200,"currency":"EUR"}`),
		op("user_setting", "view.mode", `{"value":"donut"}`),
	}
	for _, o := range good {
		if err := o.Validate(); err != nil {
			t.Errorf("%s/%s rejected: %v", o.Entity, o.ID, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		op   Op
	}{
		{"unknown entity", op("sqlite_master", "x", `{}`)},
		{"empty id", op("txn", "", `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":1,"currency":"EUR"}`)},
		{"id with a quote", op("txn", `a'b`, `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":1,"currency":"EUR"}`)},
		{"data is not an object", op("txn", "t", `[1,2,3]`)},
		{"data is null", op("txn", "t", `null`)},
		{"missing amount", op("txn", "t", `{"kind":"expense","occurredOn":"2026-08-03","currency":"EUR"}`)},
		{"fractional minor units", op("txn", "t", `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":14.4,"currency":"EUR"}`)},
		{"amount as string", op("txn", "t", `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":"1440","currency":"EUR"}`)},
		{"bad date", op("txn", "t", `{"kind":"expense","occurredOn":"03.08.2026","amountMinor":1,"currency":"EUR"}`)},
		{"bad kind", op("txn", "t", `{"kind":"refund","occurredOn":"2026-08-03","amountMinor":1,"currency":"EUR"}`)},
		{"lowercase currency", op("txn", "t", `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":1,"currency":"eur"}`)},
		{"income category kind on a category", op("category", "c", `{"name":"X","kind":"transfer"}`)},
		{"bad budget period", op("budget", "b", `{"limitMinor":1,"currency":"EUR","period":"August"}`)},
		{"bad recurrence", op("recurring_rule", "r", `{"kind":"expense","freq":"fortnightly","nextOn":"2026-09-01","amountMinor":-1200,"currency":"EUR"}`)},
		{"recurring rule missing amount", op("recurring_rule", "r", `{"kind":"expense","freq":"monthly","nextOn":"2026-09-01","currency":"EUR"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.op.Validate(); err == nil {
				t.Error("want an error, got nil")
			}
		})
	}
}

func TestValidateRejectsBadEnvelope(t *testing.T) {
	base := `{"kind":"expense","occurredOn":"2026-08-03","amountMinor":1,"currency":"EUR"}`

	zero := op("txn", "t", base)
	zero.Lamport = 0
	if err := zero.Validate(); err == nil {
		t.Error("lamport 0 accepted")
	}

	noDevice := op("txn", "t", base)
	noDevice.DeviceID = ""
	if err := noDevice.Validate(); err == nil {
		t.Error("empty deviceId accepted")
	}

	big := op("txn", "t", `{"note":"`+string(make([]byte, MaxDataBytes))+`"}`)
	if err := big.Validate(); err == nil {
		t.Error("oversized data accepted")
	}
}

// A tombstone must not have to satisfy the live-row shape: otherwise deleting a
// row written before a field became required would be impossible.
func TestValidateTombstoneNeedsNoBody(t *testing.T) {
	o := op("txn", "t1", `{}`)
	o.Deleted = true
	if err := o.Validate(); err != nil {
		t.Errorf("tombstone rejected: %v", err)
	}

	empty := Op{Entity: "txn", ID: "t1", Lamport: 2, DeviceID: "d", Deleted: true}
	if err := empty.Validate(); err != nil {
		t.Errorf("tombstone with no data rejected: %v", err)
	}
}
