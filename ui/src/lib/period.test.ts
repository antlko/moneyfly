import { describe, expect, it } from 'vitest'
import { currentPeriod, periodRange, shiftPeriod, today } from './period'

describe('shiftPeriod', () => {
  it('crosses year boundaries', () => {
    expect(shiftPeriod('2026-01', -1)).toBe('2025-12')
    expect(shiftPeriod('2025-12', 1)).toBe('2026-01')
  })

  it('does not overshoot from a 31-day month', () => {
    // The classic date trap: stepping back from March must land in February.
    expect(shiftPeriod('2026-03', -1)).toBe('2026-02')
    expect(shiftPeriod('2026-05', -1)).toBe('2026-04')
  })

  it('walks a fiscal year, August to July', () => {
    let period = '2025-08'
    const seen: string[] = []
    for (let i = 0; i < 12; i++) {
      seen.push(period)
      period = shiftPeriod(period, 1)
    }
    expect(seen).toHaveLength(12)
    expect(seen[0]).toBe('2025-08')
    expect(seen[11]).toBe('2026-07')
  })
})

describe('periodRange', () => {
  it('covers the whole month', () => {
    expect(periodRange('2026-07')).toEqual({ from: '2026-07-01', to: '2026-07-31' })
    expect(periodRange('2026-02')).toEqual({ from: '2026-02-01', to: '2026-02-28' })
    expect(periodRange('2028-02')).toEqual({ from: '2028-02-01', to: '2028-02-29' })
  })
})

describe('currentPeriod and today', () => {
  it('formats with a zero-padded month', () => {
    expect(currentPeriod(new Date(2026, 6, 15))).toBe('2026-07')
    expect(currentPeriod(new Date(2026, 0, 1))).toBe('2026-01')
    expect(today(new Date(2026, 6, 5))).toBe('2026-07-05')
  })
})
