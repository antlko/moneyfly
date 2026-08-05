/**
 * Fitting text into a space whose size is only known at runtime.
 *
 * The donut's hole is a fraction of a window, and the figures inside it are a
 * month's spending — neither is known when the CSS is written, so a fixed font
 * size can only ever fit by luck. At 320px it did not: a 54px hole with
 * `-€1,234.56` written across the ring, in the one element on the screen that
 * exists to be read at a glance.
 */

/**
 * Width of one character, in ems, for the app's tabular figures.
 *
 * Measured in the browser at the weight the totals use: a digit comes out at
 * about 0.55em. The value here is deliberately a little **larger** than that, so
 * the arithmetic errs towards text that is smaller than it strictly needs to be
 * — the failure mode of an underestimate is a figure sitting on top of the
 * chart, and of an overestimate is a figure a point smaller than it could be.
 *
 * It is also an over-estimate for a second reason: amounts are rendered with
 * their fractional part at 0.72em, so a real string is narrower than a uniform
 * one of the same length.
 */
export const EM_PER_CHAR = 0.58

/**
 * How much of a circle's diameter a line of text may occupy.
 *
 * Not all of it: a chord across a circle is only as long as the diameter
 * through the middle, and the two lines of the donut's centre sit above and
 * below that. The curve comes in to meet them well before the full width.
 */
const USABLE = 0.86

/**
 * The largest font size, in px, at which `chars` characters fit across a circle
 * of diameter `holePx` — bounded to a legible range.
 *
 * Measured in characters rather than in the value, because length is what
 * decides: `HUF 1,250,000` is long from grouping and a three-letter symbol, not
 * from being a large number.
 */
export function fitFontPx(holePx: number, chars: number, min = 10, max = 22): number {
  const usableWidth = Math.max(0, holePx) * USABLE
  const ideal = usableWidth / (Math.max(1, chars) * EM_PER_CHAR)
  return Math.max(min, Math.min(max, ideal))
}
