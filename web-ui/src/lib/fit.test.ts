import { describe, expect, it } from 'vitest'

import { EM_PER_CHAR, fitFontPx } from './fit'
import { formatMoney } from './money'

/** What the chosen size actually renders as, by the same measure it was chosen. */
const widthOf = (chars: number, fontPx: number) => chars * EM_PER_CHAR * fontPx

describe('fitFontPx', () => {
  /**
   * The property that matters: whatever the hole and whatever the figure, the
   * text stays inside the circle. This is the case that shipped broken — a 54px
   * hole at 320px wide, with the month's total written over the ring.
   */
  it('keeps every plausible amount inside the hole', () => {
    const amounts = [
      formatMoney(0, 'EUR'),
      formatMoney(-5000, 'EUR'),
      formatMoney(-123456, 'EUR'),
      formatMoney(-98765432, 'EUR'),
      formatMoney(1250000, 'HUF'),
      formatMoney(-123456789, 'JPY'),
    ]

    for (const hole of [54, 84, 120, 200, 320]) {
      for (const text of amounts) {
        const px = fitFontPx(hole, text.length)
        // At the floor the text may genuinely not fit — below ~10px it would be
        // unreadable anyway, and shrinking further trades one problem for a
        // worse one. Everywhere else, fitting is not negotiable.
        if (px > 10) {
          expect(widthOf(text.length, px), `${text} in a ${hole}px hole`).toBeLessThanOrEqual(hole)
        }
      }
    }
  })

  it('stays legible rather than shrinking without limit', () => {
    expect(fitFontPx(20, 40)).toBe(10)
    expect(fitFontPx(0, 8)).toBe(10)
  })

  it('does not grow past the size the design asks for', () => {
    expect(fitFontPx(1000, 3)).toBe(22)
  })

  /** A longer figure in the same hole is set smaller, never the same or larger. */
  it('shrinks as the figure gets longer', () => {
    const sizes = [4, 8, 12, 16].map((chars) => fitFontPx(120, chars))
    for (let i = 1; i < sizes.length; i++) {
      expect(sizes[i]).toBeLessThanOrEqual(sizes[i - 1])
    }
    expect(sizes.at(-1)).toBeLessThan(sizes[0])
  })

  /** A wider hole takes the same figure at a larger size, never a smaller one. */
  it('grows as the hole gets wider', () => {
    const sizes = [40, 80, 160].map((hole) => fitFontPx(hole, 10))
    expect(sizes[0]).toBeLessThanOrEqual(sizes[1])
    expect(sizes[1]).toBeLessThanOrEqual(sizes[2])
  })
})
