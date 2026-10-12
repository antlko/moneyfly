import { addMonths, type MonthKey } from './period'

/**
 * Capital: what you hold, month by month, and what that says about spending.
 *
 * The model is the spreadsheet people already keep — one row per place money
 * sits (cash, each bank, gold, deposits…), one column per month, a number typed
 * into each cell — and it is kept that literal on purpose. A month's figure is a
 * **check-in**, not something derived from the ledger: gold and investments
 * change value with no transaction at all, and cash leaks in ways nobody
 * records. What the ledger knows (income, spending) is then laid *against*
 * those check-ins, and the gap between the two is the interesting part.
 *
 * Everything here is pure arithmetic over plain numbers, so it is tested;
 * stores/capital.ts feeds it from the replica.
 */

/** One account's check-in for one month, in the account's own currency. */
export interface SnapshotValue {
  accountId: string
  month: MonthKey
  amountMinor: number
  currency: string
}

/**
 * The row id of an account's check-in for a month.
 *
 * Deterministic, so two devices filling in the same cell write the same row and
 * last-write-wins settles it — the way user_setting rows are keyed — instead of
 * leaving two competing check-ins for one month.
 */
export const snapshotId = (accountId: string, month: MonthKey) => `${accountId}:${month}`

/** Longest range ever produced — a corrupt date must not build a ten-thousand-column table. */
const MAX_MONTHS = 600

/** Every month from `from` to `to`, inclusive. Empty when `from` is later. */
export function monthRange(from: MonthKey, to: MonthKey): MonthKey[] {
  const out: MonthKey[] = []
  for (let m = from; m <= to && out.length < MAX_MONTHS; m = addMonths(m, 1)) out.push(m)
  return out
}

/** Check-ins grouped by account, each list in month order. */
export function byAccount(snapshots: readonly SnapshotValue[]): Map<string, SnapshotValue[]> {
  const out = new Map<string, SnapshotValue[]>()
  for (const s of snapshots) {
    const list = out.get(s.accountId)
    if (list) list.push(s)
    else out.set(s.accountId, [s])
  }
  for (const list of out.values()) list.sort((a, b) => a.month.localeCompare(b.month))
  return out
}

/** What an account held in a month, and whether that was typed in or carried over. */
export interface Held {
  amountMinor: number
  currency: string
  /** The month the figure was actually entered for. */
  from: MonthKey
  /** True when `from` is an earlier month — nothing was entered for this one. */
  carried: boolean
}

/**
 * What an account held at the end of `month`: its own check-in, or failing that
 * the latest earlier one, **carried forward**.
 *
 * Carrying forward is what makes the table bearable. Gold does not need
 * re-typing every month to still be there, and a month where one account was
 * forgotten should not show the whole net worth collapsing. A carried figure is
 * marked as such, so it never passes for one somebody checked. Nothing is
 * carried *backwards*: before an account's first check-in it simply has no value.
 */
export function heldAt(list: readonly SnapshotValue[] | undefined, month: MonthKey): Held | null {
  if (!list) return null
  for (let i = list.length - 1; i >= 0; i--) {
    const s = list[i]!
    if (s.month <= month) {
      return {
        amountMinor: s.amountMinor,
        currency: s.currency,
        from: s.month,
        carried: s.month !== month,
      }
    }
  }
  return null
}

/**
 * The share of income not spent: (income − spending) ÷ income.
 *
 * `null` without income, because the ratio means nothing then — a month with no
 * salary recorded is not a month in which 0% or −∞% was saved.
 */
export function savedShare(incomeMinor: number, spentMinor: number): number | null {
  if (incomeMinor <= 0) return null
  return (incomeMinor - spentMinor) / incomeMinor
}

/**
 * How many months what is ready to use would last at the average rate of
 * spending. `null` with no spending to divide by.
 */
export function runwayMonths(readyMinor: number, avgSpentMinor: number): number | null {
  if (avgSpentMinor <= 0) return null
  return Math.max(0, readyMinor) / avgSpentMinor
}

/**
 * For each position, the mean of the last `window` values that exist, ending
 * there. Gaps are skipped rather than counted as zero — an unrecorded month is
 * missing information, not a month of spending nothing.
 */
export function trailingMean(values: readonly (number | null)[], window: number): (number | null)[] {
  return values.map((_, i) => {
    const seen: number[] = []
    for (let j = i; j >= 0 && seen.length < window; j--) {
      const v = values[j]
      if (v !== null && v !== undefined) seen.push(v)
    }
    return seen.length ? seen.reduce((a, b) => a + b, 0) / seen.length : null
  })
}

/** Average monthly spending now against before. */
export interface SpendingChange {
  /** How many months each side averages over. */
  months: number
  recentAvg: number
  previousAvg: number
  /** recent ÷ previous − 1. */
  change: number
}

/**
 * Personal inflation: average monthly spending over the latest `n` complete
 * months against the `n` before them.
 *
 * Twelve against twelve when there are two years to compare — the only window
 * that cancels out seasons (December against June is not inflation). With less
 * history it falls back to six and then three, which is a **trend** rather than
 * inflation and is labelled that way by the caller. Under six months there is
 * nothing honest to say.
 *
 * `spent` is oldest → newest, complete months only (the current month is still
 * filling up and would read as a fall every time).
 */
export function spendingChange(spent: readonly number[]): SpendingChange | null {
  const n = spent.length >= 24 ? 12 : spent.length >= 12 ? 6 : spent.length >= 6 ? 3 : 0
  if (n === 0) return null
  const recent = spent.slice(-n)
  const previous = spent.slice(-2 * n, -n)
  const mean = (xs: readonly number[]) => xs.reduce((a, b) => a + b, 0) / xs.length
  const recentAvg = mean(recent)
  const previousAvg = mean(previous)
  if (previousAvg <= 0) return null
  return { months: n, recentAvg, previousAvg, change: recentAvg / previousAvg - 1 }
}
