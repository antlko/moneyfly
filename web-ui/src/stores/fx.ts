import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import * as http from '@/api/http'
import { db, rateKey } from '@/db'
import { useLiveQuery } from '@/db/live'
import { convert as convertWith, parseRate, rateFor, STORAGE_BASE, type Ratio } from '@/lib/fx'
import { today } from '@/lib/period'
import type { Row } from '@/sync/types'

/** How far back rates are pulled. Beyond this the oldest stored rate is used. */
const MAX_HISTORY_DAYS = 800

/**
 * Exchange rates, cached locally.
 *
 * Rates are the one thing the app reads that is not in the op-log: they belong
 * to the world, not to the user, so they arrive over plain REST and live in
 * their own IndexedDB table. Everything above this store converts **from the
 * cache, synchronously** — a total that needs a round trip is a total that
 * disappears on the underground.
 *
 * A missing rate is reported as such rather than guessed at. The dashboard shows
 * the count; it does not quietly leave the row out of the sum, which is what the
 * pre-phase-5 behaviour did and what made a month look cheaper than it was.
 */
export const useFxStore = defineStore('fx', () => {
  const cached = useLiveQuery(() => db.fx_rate.toArray(), [])
  const refreshing = ref(false)
  const lastError = ref<string | null>(null)

  /**
   * quote → its rates, ascending by date.
   *
   * Built once per change rather than queried per lookup: converting a month's
   * worth of rows touches this hundreds of times per render, and each of those
   * has to be synchronous.
   */
  const byQuote = computed(() => {
    const out = new Map<string, { asOf: string; ratio: Ratio }[]>()
    for (const row of cached.value) {
      const ratio = parseRate(row.rate)
      if (!ratio) continue
      const list = out.get(row.quote)
      if (list) list.push({ asOf: row.asOf, ratio })
      else out.set(row.quote, [{ asOf: row.asOf, ratio }])
    }
    for (const list of out.values()) list.sort((a, b) => a.asOf.localeCompare(b.asOf))
    return out
  })

  /**
   * The stored EUR→quote rate on a day: the exact date, else the nearest
   * *earlier* one.
   *
   * Never a later one. A total computed for last March must not change because
   * a rate arrived in April — the same rule the server applies, because the two
   * have to agree about what a past month was worth.
   */
  function rateOn(quote: string, day: string): Ratio | null {
    const list = byQuote.value.get(quote.toUpperCase())
    if (!list?.length) return null

    let lo = 0
    let hi = list.length - 1
    let found: Ratio | null = null
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if (list[mid].asOf <= day) {
        found = list[mid].ratio
        lo = mid + 1
      } else {
        hi = mid - 1
      }
    }
    // Nothing on or before that day: fall back to the oldest rate held rather
    // than refusing. An imported history predates anything this instance ever
    // fetched, and the oldest known rate beats no figure at all.
    return found ?? list[0].ratio
  }

  /** Convert between currencies as of a day, or null when no rate is known. */
  function convert(minor: number, from: string, to: string, day: string): number | null {
    return convertWith(minor, from, to, (quote) => rateOn(quote, day))
  }

  /** Whether a pair can be converted at all, for deciding what to warn about. */
  const canConvert = (from: string, to: string, day: string) =>
    rateFor(from, to, (quote) => rateOn(quote, day)) !== null

  /**
   * The newest rate held for each quote, with its age in days — what the
   * Currencies screen shows. A large age is how a dead provider becomes visible
   * instead of silently freezing every total.
   */
  const latest = computed(() => {
    const now = today()
    const out: { quote: string; rate: Ratio; asOf: string; ageDays: number }[] = []
    for (const [quote, list] of byQuote.value) {
      const newest = list[list.length - 1]
      out.push({
        quote,
        rate: newest.ratio,
        asOf: newest.asOf,
        ageDays: daysBetween(newest.asOf, now),
      })
    }
    return out.sort((a, b) => a.quote.localeCompare(b.quote))
  })

  /**
   * Pull the rates this replica needs.
   *
   * The quote list is the currencies the person has **declared**, plus anything
   * their data already mentions. Declaring is what breaks the circle: inferring
   * the list purely from existing accounts meant a currency could only become
   * available after something already used it, and nothing could use it first.
   *
   * The range starts at the oldest record, so a month opened six months from now
   * is still priced with its own rate.
   *
   * Failure is recorded, not thrown. The app converts from whatever is cached
   * and the screens carry on; that is the entire point of caching them.
   */
  async function refresh(baseCurrency: string, declared: string[] = []): Promise<void> {
    if (refreshing.value) return
    refreshing.value = true
    try {
      const { quotes, from } = await needed(baseCurrency, declared)
      const to = today()
      for (const quote of quotes) await pullQuote(quote, from, to)
      lastError.value = null
    } catch (e) {
      lastError.value = e instanceof Error ? e.message : String(e)
    } finally {
      refreshing.value = false
    }
  }

  /**
   * Fetch one currency, waiting for the server to have it.
   *
   * Turning a currency on is the first moment anyone has ever asked about it, so
   * the server has to go and fetch it — which takes a second or two of talking to
   * a rate provider. Asking once lands in that gap and reports "no rate yet" for
   * a currency that is about to be perfectly fine, which reads as the feature
   * being broken.
   *
   * So this retries, briefly and a bounded number of times, and gives up quietly:
   * the daily refresh will have it by tomorrow regardless, and a screen that
   * spins forever would be worse than one that says "no rate yet".
   */
  async function addQuote(quote: string, baseCurrency: string): Promise<boolean> {
    if (quote === STORAGE_BASE || quote === baseCurrency) return true
    refreshing.value = true
    try {
      const to = today()
      const from = daysAgo(MAX_HISTORY_DAYS)
      for (let attempt = 0; attempt < 6; attempt++) {
        if (attempt > 0) await sleep(1200)
        if (await pullQuote(quote, from, to)) {
          lastError.value = null
          return true
        }
      }
      return false
    } catch (e) {
      lastError.value = e instanceof Error ? e.message : String(e)
      return false
    } finally {
      refreshing.value = false
    }
  }

  /** Store one quote's history. Returns whether the server had anything. */
  async function pullQuote(quote: string, from: string, to: string): Promise<boolean> {
    const { rates } = await http.fxHistory(quote, from, to)
    if (rates.length === 0) return false
    await db.fx_rate.bulkPut(
      rates.map((r) => ({
        key: rateKey(r.quote, r.asOf),
        quote: r.quote,
        asOf: r.asOf,
        rate: r.rate,
        source: r.source,
      })),
    )
    return true
  }

  return { rateOn, convert, canConvert, latest, refresh, addQuote, refreshing, lastError }
})

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

/** The quotes this replica needs, and how far back. */
async function needed(
  baseCurrency: string,
  declared: string[],
): Promise<{ quotes: string[]; from: string }> {
  const [accounts, txns] = await Promise.all([
    db.account.where('deleted').equals(0).toArray(),
    db.txn.where('deleted').equals(0).toArray(),
  ])

  const quotes = new Set<string>()
  const add = (code: unknown) => {
    const value = String(code ?? '').toUpperCase()
    // The storage base needs no rate against itself.
    if (value.length === 3 && value !== STORAGE_BASE) quotes.add(value)
  }
  add(baseCurrency)
  for (const code of declared) add(code)
  for (const a of accounts) add(a.currency)
  for (const t of txns) {
    add(t.currency)
    add((t as Row).toCurrency)
  }

  const earliest = txns.reduce<string>((min, t) => {
    const day = String(t.occurredOn ?? '')
    return day && day < min ? day : min
  }, today())
  return { quotes: [...quotes].sort(), from: maxDay(earliest, daysAgo(MAX_HISTORY_DAYS)) }
}

const DAY_MS = 86_400_000

function daysBetween(from: string, to: string): number {
  const diff = Date.parse(`${to}T00:00:00Z`) - Date.parse(`${from}T00:00:00Z`)
  return Number.isFinite(diff) ? Math.max(0, Math.round(diff / DAY_MS)) : 0
}

function daysAgo(days: number): string {
  return new Date(Date.now() - days * DAY_MS).toISOString().slice(0, 10)
}

const maxDay = (a: string, b: string) => (a > b ? a : b)
