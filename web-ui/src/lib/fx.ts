import { exponent } from './money'

/**
 * Currency conversion, offline.
 *
 * The client converts rather than asking the server, because the whole app has
 * to work in a tunnel: a total that needs a round trip is a total that
 * disappears on the underground. Rates are pulled into IndexedDB when there is
 * a connection and used from there afterwards.
 *
 * **This file must agree with `backend/internal/fx/fx.go` exactly.** Same
 * arrangement as the LWW rule in `lww.ts` / `sync.Wins`: duplicated on purpose,
 * because both sides genuinely need it — the client for the screen, the server
 * for CSV export — and half a cent of disagreement between them is a bug report
 * nobody can reproduce. `ConvertMinor`'s case table is mirrored between
 * `fx.test.ts` and `fx_test.go`; change one and change the other.
 */

/** The only base rates are stored against, matching the server. */
export const STORAGE_BASE = 'EUR'

/** One dated rate, as `/api/fx/latest` and `/api/fx/rates` return it. */
export interface Rate {
  asOf: string
  base: string
  quote: string
  /** A decimal string, never a number — see the note in handlers_fx.go. */
  rate: string
  source: string
  ageDays: number
}

/**
 * A rate as used in arithmetic: numerator and denominator, so the decimal is
 * exact.
 *
 * Go uses `big.Rat`. JavaScript has no rational, and `Number` would put 1.13
 * through a binary float before it ever reached a multiplication — so the
 * decimal string is split into an integer over a power of ten and the whole
 * calculation runs in `BigInt`. That is what makes the two implementations
 * agree on the last unit.
 */
export interface Ratio {
  num: bigint
  den: bigint
}

/** Parse a decimal string into an exact ratio. Returns null for anything else. */
export function parseRate(text: string): Ratio | null {
  const trimmed = text.trim()
  if (!/^-?\d+(\.\d+)?$/.test(trimmed)) return null
  const [whole, fraction = ''] = trimmed.split('.')
  return {
    num: BigInt(whole + fraction),
    den: 10n ** BigInt(fraction.length),
  }
}

const ONE: Ratio = { num: 1n, den: 1n }

const multiply = (a: Ratio, b: Ratio): Ratio => ({ num: a.num * b.num, den: a.den * b.den })
const divide = (a: Ratio, b: Ratio): Ratio => ({ num: a.num * b.den, den: a.den * b.num })

/**
 * Apply a rate to minor units, rounding half away from zero **exactly once**, at
 * the target currency's exponent.
 *
 *     minor_to = minor_from × rate × 10^(expTo − expFrom)
 *
 * Rounding once is the point. Converting and then re-scaling rounds twice, and
 * the second rounding is applied to a number that has already lost information —
 * which is how a column of converted figures ends up not adding to its own
 * total.
 */
export function convertMinor(minor: number, rate: Ratio, expFrom: number, expTo: number): number {
  let value = multiply({ num: BigInt(minor), den: 1n }, rate)
  const shift = expTo - expFrom
  if (shift > 0) value = multiply(value, { num: 10n ** BigInt(shift), den: 1n })
  else if (shift < 0) value = divide(value, { num: 10n ** BigInt(-shift), den: 1n })
  return Number(roundHalfUp(value))
}

function roundHalfUp({ num, den }: Ratio): bigint {
  const negative = num < 0n !== den < 0n
  const absNum = num < 0n ? -num : num
  const absDen = den < 0n ? -den : den

  let quotient = absNum / absDen
  // Round half away from zero: 2·remainder >= denominator promotes.
  if ((absNum % absDen) * 2n >= absDen) quotient += 1n
  return negative ? -quotient : quotient
}

/**
 * The rate for a pair on a day, from a cache of EUR-based rates.
 *
 * Mirrors `fx.Service.RateOn`: identity for a same-currency pair, the computed
 * inverse when the quote is the storage base, and a cross rate through the
 * storage base otherwise.
 *
 * `lookup` returns the stored EUR->quote rate at or before the day, or null.
 * Nearest-*earlier* matters: a figure from last March must not change because a
 * rate arrived in April.
 */
export function rateFor(
  from: string,
  to: string,
  lookup: (quote: string) => Ratio | null,
): Ratio | null {
  const base = from.toUpperCase()
  const quote = to.toUpperCase()
  if (base === quote) return ONE
  if (base === STORAGE_BASE) return lookup(quote)
  if (quote === STORAGE_BASE) {
    const direct = lookup(base)
    if (!direct || direct.num === 0n) return null
    return { num: direct.den, den: direct.num }
  }

  const toQuote = lookup(quote)
  const toBase = lookup(base)
  if (!toQuote || !toBase || toBase.num === 0n) return null
  return divide(toQuote, toBase)
}

/**
 * Convert an amount between currencies, or return null when no rate is known.
 *
 * Null rather than a guess, and null rather than zero: a missing rate is a
 * different thing from an amount of nothing, and the UI shows the original
 * figure instead of a confident wrong one.
 */
export function convert(
  minor: number,
  from: string,
  to: string,
  lookup: (quote: string) => Ratio | null,
): number | null {
  if (from.toUpperCase() === to.toUpperCase()) return minor
  const rate = rateFor(from, to, lookup)
  if (!rate) return null
  return convertMinor(minor, rate, exponent(from), exponent(to))
}
