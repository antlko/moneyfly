// Package sync defines the device-sync protocol: the operation shape, what a
// valid operation looks like, and the last-write-wins rule that decides which of
// two versions of a row survives.
//
// The rule here and the one in web-ui/src/sync must stay identical. If they ever
// disagree, devices silently diverge — each keeps its own winner and neither
// notices. docs/SYNC.md is the shared specification.
package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Entities is every synced table, in a fixed order so a snapshot is
// deterministic.
var Entities = []string{
	"account",
	"category",
	"txn",
	"budget",
	"recurring_rule",
	"user_setting",
}

// Limits on what a client may send. They bound abuse; they are not business
// rules.
const (
	MaxIDLen       = 64
	MaxDeviceIDLen = 64
	MaxDataBytes   = 16 << 10
	MaxOpsPerPush  = 1000
	// DefaultPullLimit is how many changes one pull returns. Small enough that a
	// phone on a slow connection makes progress every round trip.
	DefaultPullLimit = 500
	MaxPullLimit     = 2000
)

// Op is one create, update or delete of a single row.
type Op struct {
	Entity   string          `json:"entity"`
	ID       string          `json:"id"`
	Lamport  int64           `json:"lamport"`
	DeviceID string          `json:"deviceId"`
	Deleted  bool            `json:"deleted"`
	Data     json.RawMessage `json:"data"`
}

// Change is an Op as it appears in the change log, carrying its server order.
type Change struct {
	Op
	Seq int64 `json:"seq"`
}

// Rejection explains why one op in a batch was not applied. A rejected op is a
// client bug or an attack — never a conflict, which is resolved silently.
type Rejection struct {
	Entity string `json:"entity"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Wins reports whether an incoming version supersedes a stored one.
//
// Higher Lamport wins. On a tie, the lexicographically greater device id wins.
// The tiebreak is not cosmetic: without it, two devices that write concurrently
// can each keep their own version forever and never converge.
//
// Note this is strict — an op equal to what is stored does NOT win, which is
// exactly what makes replaying the log idempotent and lets a device pull back
// its own pushes harmlessly.
func Wins(inLamport int64, inDevice string, storedLamport int64, storedDevice string) bool {
	if inLamport != storedLamport {
		return inLamport > storedLamport
	}
	return inDevice > storedDevice
}

// IsEntity reports whether name is a synced table.
func IsEntity(name string) bool {
	for _, e := range Entities {
		if e == name {
			return true
		}
	}
	return false
}

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// A calendar month, the granularity budgets and reports work in.
	periodPattern = regexp.MustCompile(`^\d{4}-\d{2}$`)
)

// spec is the structural contract for one entity. The server checks shape only:
// that a transaction has a date and an integer amount, not that its category
// exists. Cross-row integrity cannot be checked here because ops arrive in any
// order — a transaction routinely lands before the category it names.
type spec struct {
	required []string
	validate func(fields) error
}

var specs = map[string]spec{
	"account":        {required: []string{"name", "currency"}, validate: validateAccount},
	"category":       {required: []string{"name", "kind"}, validate: validateCategory},
	"txn":            {required: []string{"kind", "occurredOn", "amountMinor", "currency"}, validate: validateTxn},
	"budget":         {required: []string{"limitMinor", "currency"}, validate: validateBudget},
	"recurring_rule": {required: []string{"freq", "nextOn"}, validate: validateRecurring},
	"user_setting":   {required: []string{"value"}},
}

// Validate checks an op is structurally sound. A returned error is the text put
// in the Rejection.
func (o *Op) Validate() error {
	if !IsEntity(o.Entity) {
		return fmt.Errorf("unknown entity %q", o.Entity)
	}
	if !idPattern.MatchString(o.ID) {
		return fmt.Errorf("id must be 1-%d characters of [A-Za-z0-9._:-]", MaxIDLen)
	}
	if o.Lamport < 1 {
		return fmt.Errorf("lamport must be positive")
	}
	if o.DeviceID == "" || len(o.DeviceID) > MaxDeviceIDLen {
		return fmt.Errorf("deviceId must be 1-%d characters", MaxDeviceIDLen)
	}
	if len(o.Data) > MaxDataBytes {
		return fmt.Errorf("data exceeds %d bytes", MaxDataBytes)
	}

	f, err := parseFields(o.Data)
	if err != nil {
		return err
	}

	// A tombstone carries whatever body the row had, or none. Requiring the full
	// shape here would make deleting a row that predates a new required field
	// impossible.
	if o.Deleted {
		return nil
	}

	sp := specs[o.Entity]
	for _, key := range sp.required {
		if _, ok := f[key]; !ok {
			return fmt.Errorf("missing required field %q", key)
		}
	}
	if sp.validate != nil {
		return sp.validate(f)
	}
	return nil
}

// fields is a decoded JSON object. Numbers stay as json.Number so an integer
// amount cannot silently round-trip through float64.
type fields map[string]any

func parseFields(raw json.RawMessage) (fields, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return fields{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var f fields
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("data must be a JSON object")
	}
	if f == nil {
		return nil, fmt.Errorf("data must be a JSON object, not null")
	}
	return f, nil
}

func (f fields) str(key string) (string, bool) {
	s, ok := f[key].(string)
	return s, ok
}

// integer returns the value only when it is a whole number. Money is integer
// minor units everywhere; a fractional amountMinor means the client has a bug
// worth surfacing rather than rounding away.
func (f fields) integer(key string) (int64, bool) {
	n, ok := f[key].(json.Number)
	if !ok {
		return 0, false
	}
	v, err := n.Int64()
	return v, err == nil
}

func (f fields) oneOf(key string, allowed ...string) error {
	v, ok := f.str(key)
	if !ok {
		return fmt.Errorf("%s must be a string", key)
	}
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
}

func (f fields) currency(key string) error {
	v, ok := f.str(key)
	if !ok || len(v) != 3 || strings.ToUpper(v) != v {
		return fmt.Errorf("%s must be a three-letter uppercase code", key)
	}
	return nil
}

func (f fields) date(key string) error {
	v, ok := f.str(key)
	if !ok || !datePattern.MatchString(v) {
		return fmt.Errorf("%s must be a YYYY-MM-DD date", key)
	}
	return nil
}

func validateAccount(f fields) error {
	if _, ok := f.str("name"); !ok {
		return fmt.Errorf("name must be a string")
	}
	return f.currency("currency")
}

func validateCategory(f fields) error {
	if _, ok := f.str("name"); !ok {
		return fmt.Errorf("name must be a string")
	}
	return f.oneOf("kind", "expense", "income")
}

func validateTxn(f fields) error {
	if err := f.oneOf("kind", "expense", "income", "transfer"); err != nil {
		return err
	}
	if err := f.date("occurredOn"); err != nil {
		return err
	}
	if _, ok := f.integer("amountMinor"); !ok {
		return fmt.Errorf("amountMinor must be a whole number of minor units")
	}
	if err := f.currency("currency"); err != nil {
		return err
	}
	// Cross-currency transfers carry the receiving side explicitly, because the
	// rate on the day of the transfer is not recoverable later.
	if _, ok := f["toAmountMinor"]; ok {
		if _, ok := f.integer("toAmountMinor"); !ok {
			return fmt.Errorf("toAmountMinor must be a whole number of minor units")
		}
	}
	// The receiving currency was previously unchecked, so a transfer could name
	// a receiving amount with no valid currency for it — and the two only mean
	// something together.
	if _, ok := f["toCurrency"]; ok {
		if err := f.currency("toCurrency"); err != nil {
			return err
		}
	}
	return nil
}

func validateBudget(f fields) error {
	if _, ok := f.integer("limitMinor"); !ok {
		return fmt.Errorf("limitMinor must be a whole number of minor units")
	}
	if err := f.currency("currency"); err != nil {
		return err
	}
	// An absent period means "every month"; a present one must be a real month.
	if v, ok := f.str("period"); ok && v != "" && !periodPattern.MatchString(v) {
		return fmt.Errorf("period must be YYYY-MM")
	}
	return nil
}

func validateRecurring(f fields) error {
	if err := f.oneOf("freq", "daily", "weekly", "monthly", "yearly"); err != nil {
		return err
	}
	return f.date("nextOn")
}
