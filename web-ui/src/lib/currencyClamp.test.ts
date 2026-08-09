import { describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'

import { initialState, press, total, type CalcState, type Key } from './calculator'
import { clampAmountString, useClampOnCurrencyChange, type Truncation } from './currencyClamp'
import { toMinor } from './money'

/** Same keystroke helper the calculator's own tests use. */
function type(keys: string, maxDecimals = 2): CalcState {
  return keys
    .split(' ')
    .filter(Boolean)
    .reduce((state, key) => press(state, key as Key, maxDecimals), initialState())
}

/**
 * The composable registers a `watch`, which needs an active effect scope
 * outside of a component. Returns the refs plus whatever truncations fired.
 */
function harness(startingCurrency: string, calcState: CalcState) {
  const currency = ref(startingCurrency)
  const calc = ref(calcState)
  const truncations: Truncation[] = []
  const scope = effectScope()
  scope.run(() => {
    useClampOnCurrencyChange(currency, calc, (change) => truncations.push(change))
  })
  return { currency, calc, truncations, stop: () => scope.stop() }
}

describe('useClampOnCurrencyChange', () => {
  it('trims a typed amount when the currency loses its minor unit', async () => {
    const { currency, calc } = harness('EUR', type('1 2 . 3 4'))

    currency.value = 'HUF'
    await nextTick()

    expect(calc.value.entry).toBe('12')
    expect(total(calc.value)).toBe(12)
  })

  it('reports what the amount was and what it became', async () => {
    const { currency, truncations } = harness('EUR', type('1 2 . 3 4'))

    currency.value = 'HUF'
    await nextTick()

    expect(truncations).toEqual([{ before: 12.34, after: 12, currency: 'HUF' }])
  })

  // The case that reads as "the app ate my amount": clamping to zero also
  // disables the confirm button, so the caller has to be able to say why.
  it('reports a clamp all the way to zero', async () => {
    const { currency, calc, truncations } = harness('EUR', type('. 9 9'))

    currency.value = 'HUF'
    await nextTick()

    expect(total(calc.value)).toBe(0)
    expect(truncations).toEqual([{ before: 0.99, after: 0, currency: 'HUF' }])
  })

  it('says nothing when the amount already fits the new currency', async () => {
    const { currency, calc, truncations } = harness('EUR', type('1 2'))

    currency.value = 'HUF'
    await nextTick()

    expect(calc.value.entry).toBe('12')
    expect(truncations).toEqual([])
  })

  it('clamps a pending expression, not only the entry', async () => {
    const { currency, calc } = harness('EUR', type('1 2 . 3 4 + 5'))

    currency.value = 'HUF'
    await nextTick()

    expect(total(calc.value)).toBe(17)
  })

  // The whole reason this is a watch on the currency rather than a handler on
  // the account picker: on the record screen the account arrives from a Dexie
  // liveQuery after mount, with no tap involved.
  it('fires when the currency moves without any picker interaction', async () => {
    const { currency, calc } = harness('EUR', type('1 2 . 3 4'))

    // Simulating the replica resolving and swapping the remembered account.
    currency.value = 'JPY'
    await nextTick()

    expect(calc.value.entry).toBe('12')
  })

  it('is inert once its scope is disposed', async () => {
    const { currency, calc, stop } = harness('EUR', type('1 2 . 3 4'))
    stop()

    currency.value = 'HUF'
    await nextTick()

    expect(calc.value.entry).toBe('12.34')
  })

  it('tolerates a missing callback', async () => {
    const currency = ref('EUR')
    const calc = ref(type('1 2 . 3 4'))
    const scope = effectScope()
    scope.run(() => useClampOnCurrencyChange(currency, calc))

    currency.value = 'HUF'
    await expect(nextTick()).resolves.not.toThrow()
    expect(calc.value.entry).toBe('12')
  })

  it('does not fire on a currency that changes to itself', async () => {
    const onTruncated = vi.fn()
    const currency = ref('EUR')
    const calc = ref(type('1 2 . 3 4'))
    const scope = effectScope()
    scope.run(() => useClampOnCurrencyChange(currency, calc, onTruncated))

    currency.value = 'EUR'
    await nextTick()

    expect(onTruncated).not.toHaveBeenCalled()
  })
})

describe('clampAmountString', () => {
  it('drops decimals a zero-decimal currency cannot express', () => {
    expect(clampAmountString('1234.56', 'HUF')).toBe('1235')
  })

  it('rounds rather than truncating', () => {
    expect(clampAmountString('10.567', 'EUR')).toBe('10.57')
  })

  it('leaves a value that already fits alone', () => {
    expect(clampAmountString('10.5', 'EUR')).toBe('10.5')
  })

  it('normalises a trailing zero away', () => {
    expect(clampAmountString('10.50', 'EUR')).toBe('10.5')
  })

  it('passes an empty box straight through', () => {
    expect(clampAmountString('', 'HUF')).toBe('')
    expect(clampAmountString('   ', 'HUF')).toBe('   ')
  })

  it('passes something unparseable straight through rather than zeroing it', () => {
    expect(clampAmountString('not a number', 'EUR')).toBe('not a number')
  })

  it('honours a three-decimal currency', () => {
    expect(clampAmountString('1.2345', 'KWD')).toBe('1.235')
  })

  // The whole point of clamping is that what is shown is what gets saved, so
  // this must agree with toMinor on every half. `toFixed` does not: it rounds
  // the binary value, giving 1.234 for KWD where toMinor gives 1.235.
  it('rounds the same way the value will actually be saved', () => {
    for (const [value, currency] of [
      ['1.2345', 'KWD'],
      ['10.565', 'EUR'],
      ['0.5', 'HUF'],
      ['2.5', 'JPY'],
      ['1.005', 'EUR'],
      ['1234.567', 'BHD'],
    ] as const) {
      const clamped = Number(clampAmountString(value, currency))
      expect(toMinor(clamped, currency)).toBe(toMinor(Number(value), currency))
      // …and clamping again is a no-op, so re-rendering cannot drift.
      expect(clampAmountString(String(clamped), currency)).toBe(String(clamped))
    }
  })
})
