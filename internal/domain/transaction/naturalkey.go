package transaction

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// keySeparator joins the natural-key fields. A unit separator cannot appear in
// any of them, so no combination of values can be made to collide by
// concatenation.
const keySeparator = "\x1f"

// NaturalKey is THE dedup contract. Stage 04 depends on it byte-for-byte.
//
// SHA-256 of the fields joined by "\x1f", in exactly this order:
//
//	occurredOn(YYYY-MM-DD) | accountSourceName | categorySourceName |
//	amountMinor(base-10)   | currency          | rawDescription
//
// rawDescription is UNTRIMMED: the observed export contains "Продукты " with a
// trailing space, which must not collide with "Продукты".
//
// Monefy exports carry no transaction id and no time, and every export is full
// history, so identity has to be structural. Changing this definition invalidates
// every stored key and needs a migration that recomputes them — hence the golden
// test (docs/adr/0008-natural-key-dedup.md).
func NaturalKey(
	occurredOn time.Time,
	accountName, categoryName string,
	amountMinor int64,
	currency, rawDescription string,
) string {
	fields := []string{
		occurredOn.UTC().Format(DateLayout),
		accountName,
		categoryName,
		strconv.FormatInt(amountMinor, 10),
		strings.ToUpper(currency),
		rawDescription,
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, keySeparator)))
	return hex.EncodeToString(sum[:])
}
