/**
 * The record screen's keypad is a real calculator, not a number field.
 *
 * That is deliberate in the app we are reproducing, and it earns its keep: a
 * shared bill is `28.40 / 3`, a top-up is `12 + 3.50`. Doing that arithmetic in
 * your head before typing is exactly the friction that stops people recording
 * spends at all.
 *
 * The whole thing is a pure reducer so it can be tested without a DOM — every
 * awkward case below (trailing operators, chained operations, division by zero,
 * a currency with no minor unit) is a test rather than a thing to click through.
 */

export type Operator = '+' | '-' | '*' | '/'
/** Spelled out rather than `${number}`, which would also admit '42'. */
export type Digit = '0' | '1' | '2' | '3' | '4' | '5' | '6' | '7' | '8' | '9'
export type Key = Operator | '=' | '.' | 'backspace' | 'clear' | Digit

export interface CalcState {
  /** The digits currently being typed, always a valid decimal string. */
  entry: string
  /** The left-hand side of a pending operation. */
  accumulator: number | null
  operator: Operator | null
  /** True right after `=` or an operator, when the next digit starts fresh. */
  replaceOnNextDigit: boolean
}

export const initialState = (): CalcState => ({
  entry: '0',
  accumulator: null,
  operator: null,
  replaceOnNextDigit: true,
})

/**
 * A state showing an amount that is already known — the edit screen opening on
 * an existing record.
 *
 * `replaceOnNextDigit` is the interesting part: it is **false**, so typing a
 * digit appends rather than wiping the figure. Someone opening a record to turn
 * 14.40 into 144.00 types a zero; someone who wants a different number entirely
 * presses backspace. Starting in "replace" mode would make the first key press
 * destroy the value they came to look at.
 */
export function typed(amount: string): CalcState {
  const entry = /^\d+(\.\d+)?$/.test(amount) ? amount : '0'
  return { entry, accumulator: null, operator: null, replaceOnNextDigit: false }
}

const isDigit = (key: Key): key is Digit => key.length === 1 && key >= '0' && key <= '9'

/**
 * Apply one key press.
 *
 * `maxDecimals` comes from the currency: HUF has none, so the decimal point is
 * simply inert rather than producing an amount that cannot be stored.
 */
export function press(state: CalcState, key: Key, maxDecimals = 2): CalcState {
  if (isDigit(key)) return pressDigit(state, key, maxDecimals)

  switch (key) {
    case '.':
      return pressDot(state, maxDecimals)
    case 'backspace':
      return pressBackspace(state)
    case 'clear':
      return initialState()
    case '=':
      return pressEquals(state)
    default:
      return pressOperator(state, key)
  }
}

function pressDigit(state: CalcState, digit: Digit, maxDecimals: number): CalcState {
  if (state.replaceOnNextDigit) {
    return { ...state, entry: digit, replaceOnNextDigit: false }
  }
  const dot = state.entry.indexOf('.')
  // Silently ignore a digit that would not survive the round trip to minor
  // units. Accepting it and rounding later means the amount saved is not the
  // one on screen.
  if (dot >= 0 && state.entry.length - dot - 1 >= maxDecimals) return state
  if (state.entry === '0') return { ...state, entry: digit }
  return { ...state, entry: state.entry + digit }
}

function pressDot(state: CalcState, maxDecimals: number): CalcState {
  if (maxDecimals === 0) return state
  if (state.replaceOnNextDigit) {
    return { ...state, entry: '0.', replaceOnNextDigit: false }
  }
  if (state.entry.includes('.')) return state
  return { ...state, entry: `${state.entry}.` }
}

function pressBackspace(state: CalcState): CalcState {
  // After `=` the entry is a result, not something that was typed; rubbing out
  // one of its digits would be meaningless, so backspace clears it.
  if (state.replaceOnNextDigit) {
    return { ...state, entry: '0', replaceOnNextDigit: false }
  }
  const next = state.entry.slice(0, -1)
  return { ...state, entry: next === '' || next === '-' ? '0' : next }
}

function pressOperator(state: CalcState, operator: Operator): CalcState {
  // Pressing another operator before typing anything just changes your mind
  // about which one, rather than folding the same number in twice.
  if (state.replaceOnNextDigit && state.operator !== null) {
    return { ...state, operator }
  }

  const left = state.accumulator === null ? current(state) : evaluate(state)
  if (left === null) return state

  return {
    entry: format(left),
    accumulator: left,
    operator,
    replaceOnNextDigit: true,
  }
}

function pressEquals(state: CalcState): CalcState {
  if (state.operator === null || state.accumulator === null) {
    return { ...state, replaceOnNextDigit: true }
  }
  const result = evaluate(state)
  if (result === null) return state // division by zero: leave the input alone
  return {
    entry: format(result),
    accumulator: null,
    operator: null,
    replaceOnNextDigit: true,
  }
}

/** The number currently in the entry field. */
export function current(state: CalcState): number {
  const value = Number(state.entry)
  return Number.isFinite(value) ? value : 0
}

/** Fold the pending operation, or null when it cannot be done. */
function evaluate(state: CalcState): number | null {
  if (state.accumulator === null || state.operator === null) return current(state)
  const right = current(state)

  switch (state.operator) {
    case '+':
      return state.accumulator + right
    case '-':
      return state.accumulator - right
    case '*':
      return state.accumulator * right
    case '/':
      // Not an error state to recover from — just refuse, and let them fix the
      // divisor. A calculator that blanks itself loses the amount you typed.
      return right === 0 ? null : state.accumulator / right
  }
}

/**
 * The value to record: the pending operation folded in, so `12 + 3` saves 15
 * even when `=` was never pressed.
 */
export function total(state: CalcState): number {
  return evaluate(state) ?? current(state)
}

/** Trim a computed result to something a person would have typed. */
function format(value: number): string {
  if (Number.isInteger(value)) return String(value)
  // Nine digits is past any currency's precision and keeps 0.1 + 0.2 from
  // rendering as 0.30000000000000004.
  return String(Number(value.toFixed(9)))
}

/** What the amount field shows. */
export const display = (state: CalcState): string => state.entry
