import { describe, expect, it } from 'vitest'

import { current, display, initialState, press, total, typed, type Key } from './calculator'

/** Type a sequence of keys and return the final state. */
function type(keys: string, maxDecimals = 2) {
  let state = initialState()
  for (const key of keys.split(' ')) {
    state = press(state, key as Key, maxDecimals)
  }
  return state
}

const shown = (keys: string, maxDecimals = 2) => display(type(keys, maxDecimals))

describe('entering a number', () => {
  it('starts at zero and replaces it', () => {
    expect(display(initialState())).toBe('0')
    expect(shown('7')).toBe('7')
    expect(shown('1 2 4 0')).toBe('1240')
  })

  it('takes a decimal point once', () => {
    expect(shown('1 2 . 4')).toBe('12.4')
    expect(shown('1 2 . 4 . 5')).toBe('12.45')
  })

  it('starts a decimal with a leading zero', () => {
    expect(shown('. 5')).toBe('0.5')
  })

  // Accepting a third decimal and rounding it away later would save an amount
  // that is not the one on screen.
  it('refuses more decimals than the currency has', () => {
    expect(shown('1 . 2 3 4')).toBe('1.23')
  })

  it('has no decimal point at all in a zero-decimal currency', () => {
    expect(shown('1 0 . 5', 0)).toBe('105')
  })

  it('allows three decimals where the currency has three', () => {
    expect(shown('1 . 2 3 4 5', 3)).toBe('1.234')
  })
})

describe('backspace', () => {
  it('rubs out one digit at a time', () => {
    expect(shown('1 2 3 backspace')).toBe('12')
    expect(shown('1 2 3 backspace backspace backspace')).toBe('0')
    expect(shown('1 backspace backspace')).toBe('0')
  })

  it('removes the decimal point too', () => {
    expect(shown('1 . backspace')).toBe('1')
  })

  // A result is not something you typed, so rubbing out one of its digits is
  // meaningless — clear it instead.
  it('clears a computed result rather than editing it', () => {
    expect(shown('8 + 2 = backspace')).toBe('0')
  })
})

describe('arithmetic', () => {
  it('adds, subtracts, multiplies and divides', () => {
    expect(shown('1 2 + 3 =')).toBe('15')
    expect(shown('1 2 - 3 =')).toBe('9')
    expect(shown('1 2 * 3 =')).toBe('36')
    expect(shown('1 2 / 3 =')).toBe('4')
  })

  // Splitting a bill is the reason the keypad has operators at all.
  it('handles the bill-splitting case', () => {
    expect(shown('2 8 . 4 0 / 3 =')).toBe('9.466666667')
  })

  it('chains without pressing equals', () => {
    expect(shown('2 + 3 + 4 =')).toBe('9')
    expect(shown('2 + 3 +')).toBe('5')
  })

  it('lets you change your mind about the operator', () => {
    expect(shown('1 2 + * 3 =')).toBe('36')
  })

  it('does not fold the same number in twice', () => {
    // '5 +' then '+' again must not become 10.
    expect(shown('5 + +')).toBe('5')
  })

  it('keeps floating point out of the display', () => {
    expect(shown('0 . 1 + 0 . 2 =')).toBe('0.3')
  })

  // A calculator that blanks itself loses the amount you already typed.
  it('refuses to divide by zero instead of breaking', () => {
    expect(shown('1 2 / 0 =')).toBe('0')
    expect(current(type('1 2 / 0 ='))).toBe(0)
    // The pending 12 is still there, so correcting the divisor works.
    expect(shown('1 2 / 0 backspace 3 =')).toBe('4')
  })

  it('ignores equals with nothing pending', () => {
    expect(shown('7 =')).toBe('7')
    expect(shown('=')).toBe('0')
  })
})

describe('total', () => {
  // Tapping a category saves immediately, so an unfinished sum must still be
  // folded in — otherwise "12 + 3" quietly records 3.
  it('folds a pending operation without equals', () => {
    expect(total(type('1 2 + 3'))).toBe(15)
    expect(total(type('2 8 . 4 0 / 4'))).toBe(7.1)
  })

  it('is the entry when nothing is pending', () => {
    expect(total(type('1 4 . 4'))).toBe(14.4)
    expect(total(initialState())).toBe(0)
  })

  it('falls back to the entry when the pending operation is impossible', () => {
    expect(total(type('1 2 / 0'))).toBe(0)
  })
})

describe('clear', () => {
  it('resets everything', () => {
    const state = type('1 2 + 3 clear')
    expect(display(state)).toBe('0')
    expect(total(state)).toBe(0)
  })
})

describe('typed', () => {
  it('opens on an existing amount and appends rather than replacing it', () => {
    // The edit screen's first key press must not destroy the figure someone
    // came to look at.
    let state = typed('14')
    expect(display(state)).toBe('14')

    state = press(state, '0')
    expect(display(state)).toBe('140')
  })

  it('still refuses more decimals than the currency has', () => {
    // Seeding the state must not smuggle in a third decimal place.
    const state = press(typed('14.40'), '0')
    expect(display(state)).toBe('14.40')
    expect(display(press(typed('3200'), '.', 0))).toBe('3200')
  })

  it('falls back to zero for anything that is not a plain decimal', () => {
    for (const bad of ['', '-5', '1e3', 'abc']) {
      expect(display(typed(bad))).toBe('0')
    }
  })

  it('lets backspace clear it the normal way', () => {
    let state = typed('14.4')
    state = press(state, 'backspace')
    state = press(state, 'backspace')
    expect(display(state)).toBe('14')
  })
})
