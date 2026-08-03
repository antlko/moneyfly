import { describe, expect, it } from 'vitest'
import { formatMoney, formatPercent, money, parseAmount, toDisplayNumber } from './money'

describe('formatMoney', () => {
  it('renders EUR with two decimals', () => {
    expect(formatMoney(money(1250, 'EUR', 2))).toBe('€12.50')
  })

  it('renders HUF with no decimals and a trailing symbol', () => {
    // 4,562,000 Ft must be readable at a glance, and HUF has no cents.
    expect(formatMoney(money(4562000, 'HUF', 0))).toBe('4,562,000 Ft')
  })

  it('renders an absent amount as an em dash, never as zero', () => {
    expect(formatMoney(null)).toBe('—')
    expect(formatMoney(undefined)).toBe('—')
    // A recorded zero is a different fact and shows as zero.
    expect(formatMoney(money(0, 'EUR', 2))).toBe('€0.00')
  })

  it('can omit the symbol for tabular columns', () => {
    expect(formatMoney(money(39730, 'UAH', 2), { symbol: false })).toBe('397.30')
  })

  it('falls back to the code for an unknown currency', () => {
    expect(formatMoney(money(100, 'PLN', 2))).toBe('PLN1.00')
  })

  it('renders negatives', () => {
    expect(formatMoney(money(-375000, 'EUR', 2))).toBe('-€3,750.00')
  })
})

describe('toDisplayNumber', () => {
  it('scales by the exponent', () => {
    expect(toDisplayNumber(money(1250, 'EUR', 2))).toBe(12.5)
    expect(toDisplayNumber(money(1000, 'HUF', 0))).toBe(1000)
  })
})

describe('parseAmount', () => {
  it('parses keypad input into minor units', () => {
    expect(parseAmount('12.50', 2)).toBe(1250)
    expect(parseAmount('12.5', 2)).toBe(1250)
    expect(parseAmount('12', 2)).toBe(1200)
    expect(parseAmount('.5', 2)).toBe(50)
    expect(parseAmount('0', 2)).toBe(0)
  })

  it('treats HUF as a whole-number currency', () => {
    expect(parseAmount('4000', 0)).toBe(4000)
    // 10.5 HUF is not representable, so it is rejected rather than rounded.
    expect(parseAmount('10.5', 0)).toBeNull()
    expect(parseAmount('10.0', 0)).toBe(10)
  })

  it('rejects excess precision rather than rounding', () => {
    expect(parseAmount('1.234', 2)).toBeNull()
    expect(parseAmount('1.2300', 2)).toBe(123)
  })

  it('rejects anything that is not a plain decimal', () => {
    for (const input of ['', '.', 'abc', '1,50', '1.2.3', '-5', '1e5', ' ']) {
      expect(parseAmount(input, 2), input).toBeNull()
    }
  })
})

describe('formatPercent', () => {
  it('renders a ratio', () => {
    expect(formatPercent(0.0416666)).toBe('4%')
    expect(formatPercent(3.0858, 1)).toBe('308.6%')
  })

  it('renders negatives unclamped', () => {
    // January was -3.75 and that is the truth.
    expect(formatPercent(-3.751067461, 1)).toBe('-375.1%')
  })

  it('renders an unknown ratio as an em dash', () => {
    expect(formatPercent(null)).toBe('—')
    expect(formatPercent(undefined)).toBe('—')
    expect(formatPercent(0)).toBe('0%')
  })
})
