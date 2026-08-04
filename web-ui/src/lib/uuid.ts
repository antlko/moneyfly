/**
 * UUIDv7 — a time-ordered id the client can mint without asking the server.
 *
 * That property is what makes offline creation work: a transaction recorded in
 * a tunnel gets its final id immediately, and because ids sort by creation time
 * they also index well in both SQLite and IndexedDB.
 *
 * Layout (RFC 9562): 48-bit millisecond timestamp, 4-bit version, 12-bit
 * counter, 2-bit variant, 62 random bits.
 */

let lastTimestamp = -1
let counter = 0

// The 12-bit rand_a field is used as a within-millisecond counter (RFC 9562
// "method 2"), so ids minted in the same millisecond still sort in creation
// order. Plain randomness there would break ordering exactly when a burst of
// rows is created — which is what an import does.
const MAX_COUNTER = 0xfff

export function uuidv7(now: number = Date.now()): string {
  if (now > lastTimestamp) {
    lastTimestamp = now
    counter = 0
  } else {
    // Either the same millisecond, or the wall clock went backwards (an NTP
    // correction, or a laptop waking up). Both are handled the same way: keep
    // the last timestamp and advance the counter, so an id never sorts into the
    // past. The timestamp is an id component, not a record of when.
    counter++
    if (counter > MAX_COUNTER) {
      // Borrowing a millisecond from the future is what keeps a burst of more
      // than 4096 ids per millisecond ordered instead of wrapping.
      lastTimestamp++
      counter = 0
    }
  }
  const ts = lastTimestamp

  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)

  // 48-bit timestamp, big-endian.
  bytes[0] = Math.floor(ts / 2 ** 40) & 0xff
  bytes[1] = Math.floor(ts / 2 ** 32) & 0xff
  bytes[2] = Math.floor(ts / 2 ** 24) & 0xff
  bytes[3] = Math.floor(ts / 2 ** 16) & 0xff
  bytes[4] = Math.floor(ts / 2 ** 8) & 0xff
  bytes[5] = ts & 0xff

  // Version 7 in the high nibble, counter in the remaining 12 bits.
  bytes[6] = 0x70 | ((counter >>> 8) & 0x0f)
  bytes[7] = counter & 0xff

  // RFC 4122 variant.
  bytes[8] = (bytes[8] & 0x3f) | 0x80

  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
