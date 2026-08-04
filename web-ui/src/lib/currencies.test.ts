import { describe, expect, it } from 'vitest'

import { CURRENCY_OPTIONS, currencyName, searchCurrencies } from './currencies'

describe('currency options', () => {
  it('lists each code once', () => {
    const codes = CURRENCY_OPTIONS.map((c) => c.code)
    expect(codes).toHaveLength(new Set(codes).size)
  })

  it('uses valid three-letter codes throughout', () => {
    for (const { code } of CURRENCY_OPTIONS) expect(code).toMatch(/^[A-Z]{3}$/)
  })
})

describe('searchCurrencies', () => {
  it('matches on code and on name', () => {
    expect(searchCurrencies('huf').map((c) => c.code)).toContain('HUF')
    expect(searchCurrencies('forint').map((c) => c.code)).toContain('HUF')
  })

  it('offers an unlisted but valid code rather than nothing', () => {
    // The shortlist is a suggestion. The server may well hold a rate for a code
    // nobody thought to list, and refusing to let it be typed would be a limit
    // invented by this file.
    const results = searchCurrencies('XAU')
    expect(results[0]).toEqual({ code: 'XAU', name: 'XAU' })
  })

  it('does not duplicate a code that is already listed', () => {
    const results = searchCurrencies('EUR')
    expect(results.filter((c) => c.code === 'EUR')).toHaveLength(1)
  })

  it('returns everything for an empty query', () => {
    expect(searchCurrencies('  ')).toHaveLength(CURRENCY_OPTIONS.length)
  })
})

describe('currencyName', () => {
  it('names a known code and passes an unknown one through', () => {
    expect(currencyName('HUF')).toBe('Hungarian forint')
    expect(currencyName('XAU')).toBe('XAU')
  })
})
