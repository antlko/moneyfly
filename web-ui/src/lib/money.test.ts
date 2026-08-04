import { describe, expect, it } from 'vitest'

import { exponent, formatMoney, splitMoney, toMajor, toMinor } from './money'

describe('exponent', () => {
  it('defaults to two', () => {
    expect(exponent('EUR')).toBe(2)
    expect(exponent('usd')).toBe(2)
    expect(exponent('XYZ')).toBe(2)
  })

  // Assuming two decimals is a real bug, not a theoretical one: the project's
  // reference Monefy export contains HUF amounts like -1000.
  it('knows the currencies that are not', () => {
    expect(exponent('HUF')).toBe(0)
    expect(exponent('JPY')).toBe(0)
    expect(exponent('KWD')).toBe(3)
  })
})

describe('toMinor / toMajor', () => {
  it('round-trips', () => {
    expect(toMinor(14.4, 'EUR')).toBe(1440)
    expect(toMajor(1440, 'EUR')).toBe(14.4)
    expect(toMinor(1000, 'HUF')).toBe(1000)
    expect(toMajor(1000, 'HUF')).toBe(1000)
  })

  it('survives binary floating point', () => {
    // 0.1 + 0.2 is 0.30000000000000004; the cent must not go missing.
    expect(toMinor(0.1 + 0.2, 'EUR')).toBe(30)
    expect(toMinor(1.005, 'EUR')).toBe(101)
    expect(toMinor(8.29 * 100, 'EUR')).toBe(82900)
  })

  it('rounds away from zero on both signs', () => {
    expect(toMinor(-1.005, 'EUR')).toBe(-101)
    expect(toMinor(-0.005, 'EUR')).toBe(-1)
  })
})

describe('splitMoney', () => {
  // The parts exist so the UI can render '-€317.' large and '03' small, the way
  // the reference screenshots do.
  it('separates the pieces the dashboard renders differently', () => {
    const p = splitMoney(-31703, 'EUR', 'en-US')
    expect(p.sign).toBe('-')
    expect(p.symbol).toBe('€')
    expect(p.whole).toBe('317')
    expect(p.separator).toBe('.')
    expect(p.fraction).toBe('03')
  })

  it('has no fractional part for a zero-decimal currency', () => {
    const p = splitMoney(-1000, 'HUF', 'en-US')
    expect(p.fraction).toBe('')
    expect(p.separator).toBe('')
    expect(p.whole).toBe('1,000')
  })

  // Scanning for the decimal separator from the end is what makes both
  // 1,234.56 and 1.234,56 come out right.
  it('finds the decimal separator whichever way the locale groups', () => {
    const us = splitMoney(123456, 'EUR', 'en-US')
    expect(us.whole).toBe('1,234')
    expect(us.fraction).toBe('56')

    const de = splitMoney(123456, 'EUR', 'de-DE')
    expect(de.whole).toBe('1.234')
    expect(de.fraction).toBe('56')
  })

  // A bad currency code must not blank out the screen.
  it('falls back rather than throwing on an unknown code', () => {
    expect(() => splitMoney(1234, 'NOPE', 'en-US')).not.toThrow()
    expect(formatMoney(1234, 'NOPE', 'en-US')).toContain('12.34')
  })
})

describe('formatMoney', () => {
  it('reassembles the parts', () => {
    expect(formatMoney(-31703, 'EUR', 'en-US')).toBe('-€317.03')
    expect(formatMoney(0, 'EUR', 'en-US')).toBe('€0.00')
    expect(formatMoney(25000, 'EUR', 'en-US')).toBe('€250.00')
  })
})
