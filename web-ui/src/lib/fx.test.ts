import { describe, expect, it } from 'vitest'

import { convert, convertMinor, parseRate, rateFor, type Ratio } from './fx'

const rate = (text: string): Ratio => {
  const parsed = parseRate(text)
  if (!parsed) throw new Error(`${text} is not a decimal`)
  return parsed
}

/** A cache stand-in: EUR -> quote, the only direction that is ever stored. */
const cache = (rates: Record<string, string>) => (quote: string) => {
  const text = rates[quote]
  return text ? rate(text) : null
}

describe('parseRate', () => {
  it('reads a decimal exactly', () => {
    expect(parseRate('1.138108')).toEqual({ num: 1138108n, den: 1000000n })
    expect(parseRate('400')).toEqual({ num: 400n, den: 1n })
    expect(parseRate('0.0025')).toEqual({ num: 25n, den: 10000n })
  })

  it('rejects anything that is not a plain decimal', () => {
    for (const bad of ['', '1.2.3', '1e5', 'soon', '1,5', ' ']) {
      expect(parseRate(bad)).toBeNull()
    }
  })
})

/*
 * This table is mirrored verbatim in TestConvertMinor_MatchesTheClientPort in
 * backend/internal/fx/fx_test.go. The two implementations convert the same
 * money — the client for the screen, the server for CSV export — and they must
 * not disagree on the last unit.
 */
describe('convertMinor agrees with the Go implementation', () => {
  const cases: [number, string, number, number, number][] = [
    [32000, '0.019531', 0, 2, 62499],
    [1000, '1.138108', 2, 2, 1138],
    [-5000, '1.138108', 2, 2, -5691],
    [743, '360.409427', 2, 0, 2678],
    [0, '1.5', 2, 2, 0],
  ]

  it.each(cases)('%d @ %s (%d→%d) = %d', (minor, r, expFrom, expTo, want) => {
    expect(convertMinor(minor, rate(r), expFrom, expTo)).toBe(want)
  })
})

describe('convertMinor', () => {
  it('shifts the exponent when the currencies disagree about minor units', () => {
    // 14.40 EUR at 400 HUF/EUR is 5760 forint — and HUF has no minor unit, so
    // that is 5760 stored, not 576000.
    expect(convertMinor(1440, rate('400'), 2, 0)).toBe(5760)
    // 2858 forint back the other way: 7.145 EUR, one rounding, 715 cents.
    expect(convertMinor(2858, rate('0.0025'), 0, 2)).toBe(715)
  })

  it('rounds half away from zero, symmetrically', () => {
    expect(convertMinor(1, rate('0.5'), 2, 2)).toBe(1)
    expect(convertMinor(-1, rate('0.5'), 2, 2)).toBe(-1)
  })

  it('rounds once, not once per step', () => {
    // Rounding after the multiply and again after the shift would give 5761
    // here; the whole point of the BigInt ratio is that it does not.
    expect(convertMinor(1441, rate('399.9'), 2, 0)).toBe(5763)
  })
})

describe('rateFor', () => {
  const lookup = cache({ USD: '1.25', HUF: '400' })

  it('is 1 for a currency against itself', () => {
    expect(rateFor('HUF', 'HUF', cache({}))).toEqual({ num: 1n, den: 1n })
  })

  it('reads a stored EUR-based rate directly', () => {
    expect(convertMinor(10000, rateFor('EUR', 'USD', lookup)!, 2, 2)).toBe(12500)
  })

  it('computes the inverse rather than storing it', () => {
    // The reason only one direction is ever stored: holding EUR→USD 1.25 and
    // USD→EUR 0.79 at the same time is a contradiction waiting to surface.
    expect(convertMinor(12500, rateFor('USD', 'EUR', lookup)!, 2, 2)).toBe(10000)
  })

  it('crosses through the storage base', () => {
    // USD→HUF = (EUR→HUF) / (EUR→USD) = 400 / 1.25 = 320.
    expect(convertMinor(100, rateFor('USD', 'HUF', lookup)!, 2, 0)).toBe(320)
  })

  it('returns null when a leg is missing', () => {
    expect(rateFor('USD', 'JPY', lookup)).toBeNull()
    expect(rateFor('EUR', 'JPY', lookup)).toBeNull()
  })
})

describe('convert', () => {
  const lookup = cache({ HUF: '400' })

  it('passes an amount through untouched when the currency already matches', () => {
    expect(convert(1440, 'EUR', 'EUR', cache({}))).toBe(1440)
  })

  it('uses each currency’s own exponent', () => {
    expect(convert(1440, 'EUR', 'HUF', lookup)).toBe(5760)
  })

  it('returns null for an unknown rate rather than zero', () => {
    // Zero would be indistinguishable from an amount of nothing, and would sum
    // into a total as if it were one. Null makes the caller decide.
    expect(convert(1440, 'EUR', 'JPY', lookup)).toBeNull()
  })
})
