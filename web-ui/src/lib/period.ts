/**
 * Calendar months, as plain strings.
 *
 * Dates here are `YYYY-MM-DD` with **no time and no timezone**, matching both
 * Monefy and its CSV export. A transaction happens on a date, not at an instant;
 * storing a timestamp would put the same purchase in different months for two
 * devices in different zones, which is the kind of bug nobody ever finds.
 */

/** `YYYY-MM`. */
export type MonthKey = string
/** `YYYY-MM-DD`. */
export type DayKey = string

/** Today, in the device's own calendar. */
export function today(now: Date = new Date()): DayKey {
  // Built from local parts, not toISOString(), which would convert to UTC and
  // hand back yesterday for anyone east of Greenwich late in the evening.
  const y = now.getFullYear()
  const m = `${now.getMonth() + 1}`.padStart(2, '0')
  const d = `${now.getDate()}`.padStart(2, '0')
  return `${y}-${m}-${d}`
}

export function monthOf(day: DayKey): MonthKey {
  return day.slice(0, 7)
}

export function currentMonth(now: Date = new Date()): MonthKey {
  return monthOf(today(now))
}

/** Shift a month key. `addMonths('2026-01', -1)` is `'2025-12'`. */
export function addMonths(month: MonthKey, delta: number): MonthKey {
  const [y, m] = month.split('-').map(Number)
  const total = y * 12 + (m - 1) + delta
  const year = Math.floor(total / 12)
  const index = total - year * 12
  return `${year}-${`${index + 1}`.padStart(2, '0')}`
}

/** Inclusive day bounds of a month, for a range query. */
export function monthBounds(month: MonthKey): { from: DayKey; to: DayKey } {
  const [y, m] = month.split('-').map(Number)
  const lastDay = new Date(y, m, 0).getDate()
  return { from: `${month}-01`, to: `${month}-${lastDay}` }
}

/** `'August'` — the month name alone, as the carousel shows it. */
export function monthLabel(month: MonthKey, locale?: string): string {
  const [y, m] = month.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString(locale, { month: 'long' })
}

/** `'August 2025'` — used when the month is not in the current year. */
export function monthLabelWithYear(month: MonthKey, locale?: string): string {
  const [y, m] = month.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString(locale, { month: 'long', year: 'numeric' })
}

/**
 * `'Monday, 3 August'` — the record screen's date row.
 *
 * Composed from two calls rather than one, because the comma the reference
 * screenshots show is not what every locale produces on its own (en-GB gives
 * "Monday 3 August"). Day and month order still comes from the locale.
 */
export function longDate(day: DayKey, locale?: string): string {
  const [y, m, d] = day.split('-').map(Number)
  const date = new Date(y, m - 1, d)
  const weekday = date.toLocaleDateString(locale, { weekday: 'long' })
  const rest = date.toLocaleDateString(locale, { day: 'numeric', month: 'long' })
  return `${weekday}, ${rest}`
}

// --- Periods ---------------------------------------------------------------------

/**
 * The dashboard is not month-only.
 *
 * The reference's left drawer offers day, week, month, year, all and a custom
 * interval, and every total on the screen is over whichever is selected. Keeping
 * a bare `MonthKey` in the store instead is what makes that change expensive
 * later, so the period is modelled properly from the start.
 */
export type PeriodKind = 'day' | 'week' | 'month' | 'year' | 'all' | 'interval'

export interface Period {
  kind: PeriodKind
  /** A day inside the period; for an interval, its first day. */
  anchor: DayKey
  /** Only meaningful for an interval. */
  until?: DayKey
}

/** Dates outside any plausible ledger, used as the open bounds of "all time". */
const DAWN: DayKey = '0001-01-01'
const DUSK: DayKey = '9999-12-31'

const toDate = (day: DayKey) => {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(y, m - 1, d)
}

const fromDate = (date: Date): DayKey => today(date)

const addDays = (day: DayKey, delta: number): DayKey => {
  const date = toDate(day)
  date.setDate(date.getDate() + delta)
  return fromDate(date)
}

/**
 * Monday-based week start.
 *
 * A setting one day, but a constant until then — and constant is better than
 * guessing from the locale, because a week that silently changes shape when the
 * phone's language changes is the sort of thing nobody debugs.
 */
const startOfWeek = (day: DayKey): DayKey => {
  const date = toDate(day)
  const shift = (date.getDay() + 6) % 7
  return addDays(day, -shift)
}

export function periodBounds(period: Period): { from: DayKey; to: DayKey } {
  switch (period.kind) {
    case 'day':
      return { from: period.anchor, to: period.anchor }
    case 'week': {
      const from = startOfWeek(period.anchor)
      return { from, to: addDays(from, 6) }
    }
    case 'month':
      return monthBounds(monthOf(period.anchor))
    case 'year': {
      const year = period.anchor.slice(0, 4)
      return { from: `${year}-01-01`, to: `${year}-12-31` }
    }
    case 'all':
      return { from: DAWN, to: DUSK }
    case 'interval':
      return { from: period.anchor, to: period.until ?? period.anchor }
  }
}

/** Move a period forward (+1) or back (−1). */
export function shiftPeriod(period: Period, delta: number): Period {
  switch (period.kind) {
    case 'day':
      return { ...period, anchor: addDays(period.anchor, delta) }
    case 'week':
      return { ...period, anchor: addDays(startOfWeek(period.anchor), 7 * delta) }
    case 'month': {
      const month = addMonths(monthOf(period.anchor), delta)
      return { ...period, anchor: `${month}-01` }
    }
    case 'year': {
      const year = Number(period.anchor.slice(0, 4)) + delta
      return { ...period, anchor: `${year}-01-01` }
    }
    case 'all':
      // There is nothing on either side of everything.
      return period
    case 'interval': {
      // Shift by the interval's own length, so paging through custom ranges
      // walks in equal steps rather than jumping to an arbitrary month.
      const { from, to } = periodBounds(period)
      const span = Math.round((toDate(to).getTime() - toDate(from).getTime()) / 86_400_000) + 1
      return {
        kind: 'interval',
        anchor: addDays(from, span * delta),
        until: addDays(to, span * delta),
      }
    }
  }
}

/** What the carousel shows for a period. */
export function periodLabel(period: Period, locale?: string): string {
  const { from, to } = periodBounds(period)
  switch (period.kind) {
    case 'day':
      return longDate(period.anchor, locale)
    case 'week': {
      const short = (day: DayKey) =>
        toDate(day).toLocaleDateString(locale, { day: 'numeric', month: 'short' })
      return `${short(from)} – ${short(to)}`
    }
    case 'month': {
      const month = monthOf(period.anchor)
      return month.slice(0, 4) === currentMonth().slice(0, 4)
        ? monthLabel(month, locale)
        : monthLabelWithYear(month, locale)
    }
    case 'year':
      return period.anchor.slice(0, 4)
    case 'all':
      return 'All time'
    case 'interval': {
      const short = (day: DayKey) =>
        toDate(day).toLocaleDateString(locale, { day: 'numeric', month: 'short', year: 'numeric' })
      return `${short(from)} – ${short(to)}`
    }
  }
}

/** A period of the given kind containing today. */
export function periodOfKind(kind: PeriodKind, anchor: DayKey = today()): Period {
  return kind === 'interval' ? { kind, anchor, until: anchor } : { kind, anchor }
}
