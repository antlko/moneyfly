import { describe, expect, it } from 'vitest'

import { compactMoney, labelledMonths, monthTick, niceTicks, percent, wholeMoney } from './chart'

describe('niceTicks', () => {
  it('rounds to clean steps and includes zero', () => {
    expect(niceTicks(130, 3870)).toEqual([0, 1000, 2000, 3000, 4000])
  })
  it('spans negative values through zero', () => {
    expect(niceTicks(-0.38, 0.71)).toEqual([-0.5, 0, 0.5, 1])
  })
  it('does not drift on fractional steps', () => {
    expect(niceTicks(0, 0.3, 3)).toEqual([0, 0.1, 0.2, 0.3])
  })
  it('survives a flat series', () => {
    expect(niceTicks(0, 0)).toEqual([0, 0.5, 1])
  })
})

describe('labelledMonths', () => {
  it('labels every month when there is room', () => {
    expect([...labelledMonths(4, 10)].sort()).toEqual([0, 1, 2, 3])
  })
  it('thins labels out from the latest month backwards', () => {
    expect([...labelledMonths(7, 3)].sort()).toEqual([0, 3, 6])
    expect(labelledMonths(8, 3).has(7)).toBe(true)
  })
})

describe('monthTick', () => {
  it('names the month, adding the year where one starts', () => {
    expect(monthTick('2026-08', false, 'en-GB')).toBe('Aug')
    expect(monthTick('2026-01', false, 'en-GB')).toBe('Jan ’26')
    expect(monthTick('2025-11', true, 'en-GB')).toBe('Nov ’25')
  })
})

describe('compactMoney', () => {
  it('shortens large figures', () => {
    expect(compactMoney(1_240_000, 'EUR', 'en-GB')).toBe('€12.4K')
  })
  it('respects a currency with no minor unit', () => {
    // 16,724,698 HUF is stored as 16724698 minor units — exponent 0.
    // Intl separates the code with a no-break space; normalise it to compare.
    expect(compactMoney(16_724_698, 'HUF', 'en-GB').replace(/\s/g, ' ')).toBe('HUF 16.7M')
  })
})

describe('wholeMoney', () => {
  it('drops the minor units for a figure in a sentence', () => {
    expect(wholeMoney(286_601, 'EUR', 'en-GB')).toBe('€2,866')
  })
})

describe('percent', () => {
  it('formats a share, signing it when asked', () => {
    expect(percent(0.7091)).toBe('70.9%')
    expect(percent(0.084, true)).toBe('+8.4%')
    expect(percent(-0.375, true)).toBe('-37.5%')
  })
})
