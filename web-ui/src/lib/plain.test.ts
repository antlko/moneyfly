import { reactive, ref } from 'vue'
import { describe, expect, it } from 'vitest'

import { plain } from './plain'

describe('plain', () => {
  it('passes primitives through untouched', () => {
    expect(plain(null)).toBe(null)
    expect(plain(undefined)).toBe(undefined)
    expect(plain(42)).toBe(42)
    expect(plain('EUR')).toBe('EUR')
    expect(plain(false)).toBe(false)
  })

  it('unwraps a reactive proxy into a structured-cloneable object', () => {
    const profile = reactive({ id: 'u1', email: 'a@b.c', baseCurrency: 'EUR' })

    const out = plain(profile)

    expect(out).toEqual({ id: 'u1', email: 'a@b.c', baseCurrency: 'EUR' })
    // The point of the exercise: this is what IndexedDB does on every write.
    expect(() => structuredClone(out)).not.toThrow()
    expect(() => structuredClone(profile)).toThrow()
  })

  it('unwraps nested proxies, which toRaw alone would leave in place', () => {
    const state = reactive({ accounts: ['a1', 'a2'], nested: reactive({ n: 1 }) })

    const out = plain({ value: state.accounts, deep: state })

    expect(out).toEqual({
      value: ['a1', 'a2'],
      deep: { accounts: ['a1', 'a2'], nested: { n: 1 } },
    })
    expect(() => structuredClone(out)).not.toThrow()
  })

  it('unwraps a ref read through .value', () => {
    const user = ref<{ id: string } | null>(null)
    user.value = { id: 'u1' }

    expect(() => structuredClone(plain(user.value))).not.toThrow()
    expect(plain(user.value)).toEqual({ id: 'u1' })
  })

  it('drops members structured clone would reject', () => {
    const row = { id: 't1', amountMinor: -500, onSave: () => undefined, missing: undefined }

    expect(plain(row)).toEqual({ id: 't1', amountMinor: -500 })
  })

  it('returns a copy, so later mutation of the source cannot reach the replica', () => {
    const source = reactive({ note: 'coffee' })
    const out = plain(source)

    source.note = 'tea'

    expect(out.note).toBe('coffee')
  })
})
