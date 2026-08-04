import { describe, expect, it } from 'vitest'

import {
  addMonths,
  currentMonth,
  longDate,
  monthBounds,
  monthLabel,
  monthOf,
  periodBounds,
  periodLabel,
  shiftPeriod,
  today,
} from './period'

describe('today', () => {
  // toISOString() would convert to UTC and return yesterday for anyone east of
  // Greenwich late in the evening — a spend recorded at 23:30 would land on the
  // wrong day, and in the wrong month once a year.
  it('uses the local calendar, not UTC', () => {
    const lateEvening = new Date(2026, 7, 3, 23, 30)
    expect(today(lateEvening)).toBe('2026-08-03')
  })

  it('pads single digits', () => {
    expect(today(new Date(2026, 0, 5))).toBe('2026-01-05')
  })
})

describe('addMonths', () => {
  it('moves within a year', () => {
    expect(addMonths('2026-08', 1)).toBe('2026-09')
    expect(addMonths('2026-08', -1)).toBe('2026-07')
  })

  it('crosses year boundaries in both directions', () => {
    expect(addMonths('2026-12', 1)).toBe('2027-01')
    expect(addMonths('2026-01', -1)).toBe('2025-12')
    expect(addMonths('2026-01', -13)).toBe('2024-12')
    expect(addMonths('2026-06', 30)).toBe('2028-12')
  })

  it('is reversible', () => {
    for (let d = -30; d <= 30; d++) {
      expect(addMonths(addMonths('2026-08', d), -d)).toBe('2026-08')
    }
  })
})

describe('monthBounds', () => {
  it('ends on the real last day', () => {
    expect(monthBounds('2026-08')).toEqual({ from: '2026-08-01', to: '2026-08-31' })
    expect(monthBounds('2026-09')).toEqual({ from: '2026-09-01', to: '2026-09-30' })
    expect(monthBounds('2026-02')).toEqual({ from: '2026-02-01', to: '2026-02-28' })
  })

  it('handles a leap February', () => {
    expect(monthBounds('2028-02').to).toBe('2028-02-29')
  })
})

describe('labels', () => {
  it('names the month', () => {
    expect(monthLabel('2026-08', 'en-GB')).toBe('August')
    expect(monthLabel('2026-01', 'en-GB')).toBe('January')
  })

  // The record screen's date row, per the reference screenshots.
  it('formats a long date', () => {
    expect(longDate('2026-08-03', 'en-GB')).toBe('Monday, 3 August')
  })
})

describe('monthOf / currentMonth', () => {
  it('slices the month off a day', () => {
    expect(monthOf('2026-08-03')).toBe('2026-08')
    expect(currentMonth(new Date(2026, 7, 3))).toBe('2026-08')
  })
})

describe('periods', () => {
  it('bounds a day, week, month and year', () => {
    expect(periodBounds({ kind: 'day', anchor: '2026-08-04' })).toEqual({
      from: '2026-08-04',
      to: '2026-08-04',
    })
    // Tuesday 4 August 2026 → the Monday-based week is 3–9 August.
    expect(periodBounds({ kind: 'week', anchor: '2026-08-04' })).toEqual({
      from: '2026-08-03',
      to: '2026-08-09',
    })
    expect(periodBounds({ kind: 'month', anchor: '2026-08-04' })).toEqual({
      from: '2026-08-01',
      to: '2026-08-31',
    })
    expect(periodBounds({ kind: 'year', anchor: '2026-08-04' })).toEqual({
      from: '2026-01-01',
      to: '2026-12-31',
    })
  })

  it('treats a Sunday as the end of its week, not the start', () => {
    expect(periodBounds({ kind: 'week', anchor: '2026-08-09' }).from).toBe('2026-08-03')
  })

  it('spans everything for "all"', () => {
    const { from, to } = periodBounds({ kind: 'all', anchor: '2026-08-04' })
    expect(from < '1900-01-01').toBe(true)
    expect(to > '2100-01-01').toBe(true)
  })

  it('shifts each kind by its own step', () => {
    expect(shiftPeriod({ kind: 'day', anchor: '2026-08-04' }, 1).anchor).toBe('2026-08-05')
    expect(shiftPeriod({ kind: 'week', anchor: '2026-08-04' }, 1).anchor).toBe('2026-08-10')
    expect(shiftPeriod({ kind: 'month', anchor: '2026-08-04' }, 1).anchor).toBe('2026-09-01')
    expect(shiftPeriod({ kind: 'year', anchor: '2026-08-04' }, -1).anchor).toBe('2025-01-01')
  })

  it('crosses a month boundary when shifting days', () => {
    expect(shiftPeriod({ kind: 'day', anchor: '2026-08-31' }, 1).anchor).toBe('2026-09-01')
    expect(shiftPeriod({ kind: 'day', anchor: '2026-01-01' }, -1).anchor).toBe('2025-12-31')
  })

  // There is nothing on either side of everything.
  it('does not shift "all"', () => {
    const all = { kind: 'all', anchor: '2026-08-04' } as const
    expect(shiftPeriod(all, 1)).toEqual(all)
    expect(shiftPeriod(all, -1)).toEqual(all)
  })

  // Paging a custom range should walk in equal steps, not jump to a month.
  it('shifts an interval by its own length', () => {
    const week = { kind: 'interval', anchor: '2026-08-04', until: '2026-08-10' } as const
    expect(shiftPeriod(week, 1)).toEqual({
      kind: 'interval',
      anchor: '2026-08-11',
      until: '2026-08-17',
    })
    expect(shiftPeriod(shiftPeriod(week, 1), -1)).toEqual(week)
  })

  it('labels each kind', () => {
    expect(periodLabel({ kind: 'month', anchor: '2026-08-04' }, 'en-GB')).toContain('August')
    expect(periodLabel({ kind: 'year', anchor: '2026-08-04' }, 'en-GB')).toBe('2026')
    expect(periodLabel({ kind: 'all', anchor: '2026-08-04' }, 'en-GB')).toBe('All time')
    expect(periodLabel({ kind: 'day', anchor: '2026-08-04' }, 'en-GB')).toBe('Tuesday, 4 August')
    expect(periodLabel({ kind: 'week', anchor: '2026-08-04' }, 'en-GB')).toBe('3 Aug – 9 Aug')
  })
})
