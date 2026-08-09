/**
 * Keep a typed amount expressible in the currency it is about to be saved in.
 *
 * Every screen that lets you type an amount *and* change the account is a place
 * where the currency can change under a half-typed figure. EUR allows two
 * decimals, HUF allows none, and `toMinor` rounds to the target exponent at save
 * time — so `12.34` typed against a EUR account and then switched to HUF is
 * displayed as `12.34` and written as `12`. The displayed number and the saved
 * number disagree, silently, in the user's favour or against it depending on the
 * rounding. That is worse than a visible failure.
 *
 * This is a `watch` on the *currency*, not a handler on the account picker,
 * because the picker is not the only thing that moves it. On the record screen
 * the currency derives from an account that itself derives from a Dexie
 * `liveQuery` and a remembered setting — both resolve after mount and both can
 * change from a background sync, with no tap involved. Watching the derived
 * value covers every path at once; wiring the handler covers the one path
 * someone remembered.
 */
import { watch, type Ref } from 'vue'

import { clampDecimals, total, type CalcState } from './calculator'
import { exponent, roundToDecimals } from './money'

/** What was lost, for a caller that wants to say so out loud. */
export interface Truncation {
  before: number
  after: number
  currency: string
}

/**
 * Re-normalise `calc` whenever `currency` changes, and report it when that
 * actually changed the number.
 *
 * `onTruncated` is not decoration. Clamping `0.99` to a zero-decimal currency
 * yields `0`, which disables the confirm button — from the user's seat that is
 * indistinguishable from the app having thrown their amount away, which is the
 * exact bug this function exists to fix. Say what happened.
 */
export function useClampOnCurrencyChange(
  currency: Ref<string>,
  calc: Ref<CalcState>,
  onTruncated?: (change: Truncation) => void,
): void {
  watch(currency, (next) => {
    const before = calc.value
    const clamped = clampDecimals(before, exponent(next))
    // `clampDecimals` returns the same object when there was nothing to do.
    if (clamped === before) return
    calc.value = clamped
    onTruncated?.({ before: total(before), after: total(clamped), currency: next })
  })
}

/**
 * The same rule for a plain text/number input rather than a `CalcState` — the
 * transfer screen's hand-typed "received" figure.
 *
 * Returns the input unchanged when it is empty or unparseable, so it can be
 * applied to whatever is in the box without having to guard at the call site.
 *
 * Rounds through `roundToDecimals`, not `toFixed`: this figure is saved by
 * `toMinor`, and the two must not disagree about a half — see money.ts.
 */
export function clampAmountString(value: string, currency: string): string {
  const trimmed = value.trim()
  if (trimmed === '') return value
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed)) return value
  return String(roundToDecimals(parsed, exponent(currency)))
}
