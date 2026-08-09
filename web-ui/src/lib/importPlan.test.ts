import { describe, expect, it } from 'vitest'

import type { ImportNameStatus, ImportPreview } from '@/api/http'
import { CATEGORY_COLORS } from '@/lib/categories'
import {
  isSettled,
  planImport,
  planRows,
  plannedAccounts,
  plannedCategories,
  undeclaredCurrencies,
} from './importPlan'

const cat = (key: string, name: string, resolved = false, kind = 'expense'): ImportNameStatus => ({
  key,
  name,
  kind,
  resolved,
  count: 0,
})

const acc = (key: string, name: string, currency: string, resolved = false): ImportNameStatus => ({
  key,
  name,
  currency,
  resolved,
  count: 0,
})

describe('planRows', () => {
  it('counts a row once even when both its ends are unmapped', () => {
    // The bug this exists to prevent: summing the two unresolved name counts
    // reports 2 skipped rows for 1 actual row, so the screen claims to skip
    // more than the file contains.
    const plan = planRows(
      [{ categoryKey: 'expense:Utilities', accountKey: 'HUF:Cash', count: 5 }],
      new Set(),
      new Set(),
    )
    expect(plan).toEqual({ importable: 0, skipped: 5 })
  })

  it('needs both ends settled before a row is importable', () => {
    const groups = [{ categoryKey: 'expense:Food', accountKey: 'HUF:Cash', count: 3 }]
    expect(planRows(groups, new Set(['expense:Food']), new Set()).importable).toBe(0)
    expect(planRows(groups, new Set(), new Set(['HUF:Cash'])).importable).toBe(0)
    expect(
      planRows(groups, new Set(['expense:Food']), new Set(['HUF:Cash'])).importable,
    ).toBe(3)
  })

  it('splits a mixed file', () => {
    const plan = planRows(
      [
        { categoryKey: 'expense:Food', accountKey: 'EUR:Cash', count: 100 },
        { categoryKey: 'expense:Food', accountKey: 'HUF:Cash', count: 20 },
        { categoryKey: 'expense:Utilities', accountKey: 'EUR:Cash', count: 7 },
      ],
      new Set(['expense:Food']),
      new Set(['EUR:Cash']),
    )
    expect(plan).toEqual({ importable: 100, skipped: 27 })
  })

  it('handles an empty file', () => {
    expect(planRows([], new Set(), new Set())).toEqual({ importable: 0, skipped: 0 })
  })
})

describe('isSettled', () => {
  it('accepts an already-resolved name', () => {
    expect(isSettled(cat('expense:Food', 'Food', true), {})).toBe(true)
  })

  it('accepts a name the operator mapped', () => {
    expect(isSettled(cat('expense:Utils', 'Utils'), { 'expense:Utils': 'cat:bills' })).toBe(true)
  })

  it('rejects an empty mapping, which is what an untouched select holds', () => {
    expect(isSettled(cat('expense:Utils', 'Utils'), { 'expense:Utils': '' })).toBe(false)
  })

  it('is keyed on the composite, not the name', () => {
    // Mapping expense:Gifts must not settle income:Gifts.
    const mapping = { 'expense:Gifts': 'cat:gifts-out' }
    expect(isSettled(cat('expense:Gifts', 'Gifts', false, 'expense'), mapping)).toBe(true)
    expect(isSettled(cat('income:Gifts', 'Gifts', false, 'income'), mapping)).toBe(false)
  })
})

describe('planImport', () => {
  const preview: ImportPreview = {
    totalRows: 30,
    parseErrors: [],
    categories: [cat('expense:Food', 'Food', true), cat('expense:Utilities', 'Utilities')],
    accounts: [acc('EUR:Cash', 'Cash', 'EUR', true), acc('HUF:Cash', 'Cash', 'HUF')],
    currencies: [
      { code: 'EUR', count: 25 },
      { code: 'HUF', count: 5 },
    ],
    groups: [
      { categoryKey: 'expense:Food', accountKey: 'EUR:Cash', count: 20 },
      { categoryKey: 'expense:Utilities', accountKey: 'EUR:Cash', count: 5 },
      { categoryKey: 'expense:Food', accountKey: 'HUF:Cash', count: 5 },
    ],
  }

  it('counts only what already resolves when nothing is mapped', () => {
    expect(planImport(preview, {}, {})).toEqual({ importable: 20, skipped: 10 })
  })

  it('picks up rows as mappings are made', () => {
    expect(planImport(preview, { 'expense:Utilities': 'cat:bills' }, {})).toEqual({
      importable: 25,
      skipped: 5,
    })
    expect(
      planImport(preview, { 'expense:Utilities': 'cat:bills' }, { 'HUF:Cash': 'acc:huf' }),
    ).toEqual({ importable: 30, skipped: 0 })
  })

  it('accounts for every row in the file', () => {
    const plan = planImport(preview, {}, {})
    expect(plan.importable + plan.skipped).toBe(preview.totalRows)
  })
})

describe('plannedCategories', () => {
  it('carries the kind through so an income category is not created as an expense', () => {
    const planned = plannedCategories([
      cat('income:Salary', 'Salary', false, 'income'),
      cat('expense:Food', 'Food', false, 'expense'),
    ])
    expect(planned.map((p) => p.kind)).toEqual(['income', 'expense'])
  })

  it('is deterministic, so the same file always proposes the same appearance', () => {
    const once = plannedCategories([cat('expense:Food', 'Food')])
    const twice = plannedCategories([cat('expense:Food', 'Food')])
    expect(once).toEqual(twice)
  })

  it('picks a palette key, never a hex', () => {
    // Category rows store the key so a re-theme need not rewrite synced rows.
    for (const name of ['Food', 'Bills', 'Hotel/Trip', 'Коммисия', '']) {
      const [planned] = plannedCategories([cat(`expense:${name}`, name)])
      expect(planned.color).not.toMatch(/^#/)
      expect(CATEGORY_COLORS as readonly string[]).toContain(planned.color)
      expect(planned.icon).toBeTruthy()
    }
  })

  it('gives different names different appearances', () => {
    const planned = plannedCategories(
      ['Food', 'Bills', 'Transport', 'Health'].map((n) => cat(`expense:${n}`, n)),
    )
    expect(new Set(planned.map((p) => `${p.icon}/${p.color}`)).size).toBeGreaterThan(1)
  })
})

describe('plannedAccounts', () => {
  it('uses the currency the file actually used', () => {
    expect(plannedAccounts([acc('HUF:Wallet', 'Wallet', 'HUF')], 'EUR')[0].currency).toBe('HUF')
  })

  it('reads an account named after its currency, the way the reference export writes them', () => {
    const [planned] = plannedAccounts([{ key: 'x', name: 'huf', resolved: false, count: 0 }], 'EUR')
    expect(planned.currency).toBe('HUF')
  })

  it('falls back to the base currency when there is nothing to go on', () => {
    const [planned] = plannedAccounts(
      [{ key: 'x', name: 'Savings', resolved: false, count: 0 }],
      'EUR',
    )
    expect(planned.currency).toBe('EUR')
  })
})

describe('undeclaredCurrencies', () => {
  const preview = (codes: string[]): ImportPreview => ({
    totalRows: 0,
    parseErrors: [],
    categories: [],
    accounts: [],
    currencies: codes.map((code) => ({ code, count: 1 })),
    groups: [],
  })

  it('ignores the base currency, which never needs a rate', () => {
    expect(undeclaredCurrencies(preview(['EUR']), 'EUR', [], () => false)).toEqual([])
  })

  it('reports a currency the user has never declared', () => {
    expect(undeclaredCurrencies(preview(['EUR', 'HUF']), 'EUR', [], () => true)).toEqual(['HUF'])
  })

  // The case that made rows import and then vanish from every total: declared,
  // but with no rate to convert it.
  it('reports a declared currency that still cannot be converted', () => {
    expect(undeclaredCurrencies(preview(['UAH']), 'EUR', ['UAH'], () => false)).toEqual(['UAH'])
  })

  it('says nothing when everything is declared and convertible', () => {
    expect(undeclaredCurrencies(preview(['HUF', 'UAH']), 'EUR', ['HUF', 'UAH'], () => true)).toEqual(
      [],
    )
  })
})
