import { toMajor } from './money'
import type { MonthKey } from './period'

/**
 * The arithmetic behind components/charts: axis ticks and labels. Kept apart
 * from the SVG so it can be tested — a chart whose axis reads 0, 3,333, 6,667
 * is the kind of thing nobody notices until a screenshot.
 */

/** Round ticks covering `[min, max]`, always including zero, about `count` of them. */
export function niceTicks(min: number, max: number, count = 4): number[] {
  let lo = Math.min(0, min)
  let hi = Math.max(0, max)
  if (lo === hi) hi = lo + 1
  const raw = (hi - lo) / count
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const norm = raw / magnitude
  const step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10) * magnitude
  lo = Math.floor(lo / step) * step
  hi = Math.ceil(hi / step) * step
  const ticks: number[] = []
  // Built by index, not by repeated addition, so 0.1-sized steps do not drift
  // into 0.30000000000000004.
  for (let i = 0; lo + i * step <= hi + step / 1e6; i++) ticks.push(Number((lo + i * step).toPrecision(12)))
  return ticks
}

/**
 * Which month labels fit: every `step`-th, counted back from the latest so the
 * most recent month — the one people look for — is always labelled.
 */
export function labelledMonths(count: number, room: number): Set<number> {
  const step = Math.max(1, Math.ceil(count / Math.max(1, room)))
  const out = new Set<number>()
  for (let i = count - 1; i >= 0; i -= step) out.add(i)
  return out
}

/** `Aug`, or `Jan ’26` where a year starts (and for the first label shown). */
export function monthTick(month: MonthKey, withYear: boolean, locale?: string): string {
  const [y, m] = month.split('-').map(Number)
  const name = new Date(y, m - 1, 1).toLocaleDateString(locale, { month: 'short' })
  return withYear || m === 1 ? `${name} ’${String(y).slice(2)}` : name
}

/** €12.4K — for axis ticks and tight labels, never for a figure someone reconciles. */
export function compactMoney(minor: number, currency: string, locale?: string): string {
  const major = toMajor(minor, currency)
  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      notation: 'compact',
      maximumFractionDigits: Math.abs(major) < 1000 ? 0 : 1,
    }).format(major)
  } catch {
    return `${currency} ${Math.round(major)}`
  }
}

/** €2,866 — whole units, for a figure inside a sentence rather than a ledger. */
export function wholeMoney(minor: number, currency: string, locale?: string): string {
  const major = toMajor(minor, currency)
  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      // Both ends set: a currency style defaults the minimum to 2, and a
      // minimum above the maximum is a RangeError on older engines.
      minimumFractionDigits: 0,
      maximumFractionDigits: 0,
    }).format(major)
  } catch {
    return `${currency} ${Math.round(major)}`
  }
}

/** 70.9%, signed when asked: +8.4%. */
export function percent(share: number, signed = false, digits = 1): string {
  const value = (share * 100).toFixed(digits)
  return `${signed && share > 0 ? '+' : ''}${value}%`
}
