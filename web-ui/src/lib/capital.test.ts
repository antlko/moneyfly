import { describe, expect, it } from 'vitest'

import {
  byAccount,
  heldAt,
  monthRange,
  runwayMonths,
  savedShare,
  snapshotId,
  spendingChange,
  trailingMean,
  type SnapshotValue,
} from './capital'

const snap = (accountId: string, month: string, amountMinor: number): SnapshotValue => ({
  accountId,
  month,
  amountMinor,
  currency: 'EUR',
})

describe('snapshotId', () => {
  it('is one row per account and month, whatever device writes it', () => {
    expect(snapshotId('acc:cash', '2026-08')).toBe('acc:cash:2026-08')
  })
})

describe('monthRange', () => {
  it('is inclusive and crosses years', () => {
    expect(monthRange('2025-11', '2026-02')).toEqual(['2025-11', '2025-12', '2026-01', '2026-02'])
  })
  it('is a single month when the ends meet', () => {
    expect(monthRange('2026-08', '2026-08')).toEqual(['2026-08'])
  })
  it('is empty when the start is later', () => {
    expect(monthRange('2026-09', '2026-08')).toEqual([])
  })
})

describe('heldAt', () => {
  const list = byAccount([snap('gold', '2026-08', 2400), snap('gold', '2026-06', 2300)]).get('gold')

  it('returns the month’s own check-in', () => {
    expect(heldAt(list, '2026-08')).toEqual({
      amountMinor: 2400,
      currency: 'EUR',
      from: '2026-08',
      carried: false,
    })
  })

  it('carries the latest earlier check-in forward, and says so', () => {
    expect(heldAt(list, '2026-07')).toMatchObject({ amountMinor: 2300, from: '2026-06', carried: true })
    expect(heldAt(list, '2026-12')).toMatchObject({ amountMinor: 2400, from: '2026-08', carried: true })
  })

  it('never carries backwards', () => {
    expect(heldAt(list, '2026-05')).toBeNull()
  })

  it('has nothing for an account never checked in', () => {
    expect(heldAt(undefined, '2026-08')).toBeNull()
  })
})

describe('byAccount', () => {
  it('sorts each account’s check-ins by month, whatever order they arrived in', () => {
    const grouped = byAccount([snap('a', '2026-09', 1), snap('b', '2026-01', 2), snap('a', '2026-03', 3)])
    expect(grouped.get('a')!.map((s) => s.month)).toEqual(['2026-03', '2026-09'])
    expect(grouped.get('b')!.map((s) => s.month)).toEqual(['2026-01'])
  })
})

describe('savedShare', () => {
  it.each([
    // The spreadsheet's own figures: salary 4167, spent 1212 → 70.9%.
    [416700, 121200, 0.7091],
    // Spending more than earned is a negative share, not zero.
    [295200, 406100, -0.3757],
    [100000, 0, 1],
  ])('income %i, spent %i → %d', (income, spent, expected) => {
    expect(savedShare(income, spent)).toBeCloseTo(expected, 4)
  })

  it('is undefined without income, rather than 0% or −∞%', () => {
    expect(savedShare(0, 5000)).toBeNull()
    expect(savedShare(-100, 5000)).toBeNull()
  })
})

describe('runwayMonths', () => {
  it('divides what is ready to use by the average month', () => {
    // The spreadsheet: 22,254 ready at ~2,180 a month is 10.2 months.
    expect(runwayMonths(2225400, 218000)).toBeCloseTo(10.2, 1)
  })
  it('is zero, not negative, when what is ready to use is overdrawn', () => {
    expect(runwayMonths(-5000, 1000)).toBe(0)
  })
  it('is undefined with no spending to divide by', () => {
    expect(runwayMonths(5000, 0)).toBeNull()
  })
})

describe('trailingMean', () => {
  it('averages the window ending at each position', () => {
    expect(trailingMean([10, 20, 30, 40], 3)).toEqual([10, 15, 20, 30])
  })
  it('skips gaps instead of counting them as zero', () => {
    expect(trailingMean([10, null, 30], 2)).toEqual([10, 10, 20])
  })
  it('has nothing before the first value', () => {
    expect(trailingMean([null, 5], 3)).toEqual([null, 5])
  })
})

describe('spendingChange', () => {
  const months = (n: number, value: (i: number) => number) => Array.from({ length: n }, (_, i) => value(i))

  it('compares a year against the year before once there are two', () => {
    const spent = [...months(12, () => 1000), ...months(12, () => 1100)]
    expect(spendingChange(spent)).toEqual({ months: 12, recentAvg: 1100, previousAvg: 1000, change: expect.closeTo(0.1, 6) })
  })

  it('falls back to shorter windows with less history', () => {
    expect(spendingChange(months(12, (i) => (i < 6 ? 100 : 150)))?.months).toBe(6)
    expect(spendingChange(months(6, (i) => (i < 3 ? 100 : 80)))).toMatchObject({ months: 3, change: expect.closeTo(-0.2, 6) })
  })

  it('says nothing with under six months', () => {
    expect(spendingChange(months(5, () => 100))).toBeNull()
  })

  it('says nothing when the earlier window spent nothing', () => {
    expect(spendingChange([0, 0, 0, 100, 100, 100])).toBeNull()
  })
})
