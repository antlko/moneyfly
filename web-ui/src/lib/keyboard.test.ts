import { describe, expect, it } from 'vitest'

import { KEYBOARD_MIN_PX, opensKeyboard, viewportHidesKeyboard } from './keyboard'

describe('opensKeyboard', () => {
  it.each([
    [{ tagName: 'INPUT', type: 'text' }, true],
    [{ tagName: 'input', type: 'number' }, true],
    [{ tagName: 'INPUT' }, true], // no type attribute is a text field
    [{ tagName: 'TEXTAREA' }, true],
    [{ tagName: 'DIV', isContentEditable: true }, true],
    // Pickers, not keyboards: these must not move the keypad.
    [{ tagName: 'INPUT', type: 'date' }, false],
    [{ tagName: 'INPUT', type: 'checkbox' }, false],
    [{ tagName: 'SELECT' }, false],
    [{ tagName: 'BUTTON' }, false],
    [null, false],
  ])('%o → %s', (el, expected) => {
    expect(opensKeyboard(el)).toBe(expected)
  })
})

describe('viewportHidesKeyboard', () => {
  it.each([
    // A phone keyboard: the viewport loses a third or more of its height.
    [844, 500, true],
    // The URL bar moving: tens of pixels, not a keyboard.
    [844, 790, false],
    [844, 844, false],
    [844, 844 - KEYBOARD_MIN_PX, true],
    [844, 844 - KEYBOARD_MIN_PX + 1, false],
  ])('tallest %i, now %i → %s', (tallest, current, expected) => {
    expect(viewportHidesKeyboard(tallest, current)).toBe(expected)
  })
})
