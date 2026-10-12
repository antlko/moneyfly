/**
 * Where a pull-up sheet comes to rest when the finger lets go.
 *
 * Progress is 0 docked (only the handle showing) and 1 open. Two rules, in this
 * order:
 *
 *   * **A flick decides by direction**, however short. A quick upward flick of
 *     the balance row is the whole gesture for most people; making them drag
 *     half the screen before it counts is what makes a sheet feel heavy.
 *   * **A slow drag commits once it has covered a quarter of the way.** Not
 *     half: pulling something up and watching it sink back reads as the app
 *     refusing, and a quarter is already well past anything accidental.
 *
 * Anything less returns to where it started.
 */

/** Release speed, in px per ms (≈ 350px/s), that counts as a flick. */
export const FLICK_PX_PER_MS = 0.35
/** Share of the travel a slow drag has to cover to change state. */
export const COMMIT_SHARE = 0.25

/**
 * @param from     the state the gesture started in
 * @param progress how far open the sheet is at release, 0–1
 * @param velocity px per ms at release, positive **upwards** (towards open)
 */
export function settleSheet(from: 0 | 1, progress: number, velocity: number): 0 | 1 {
  if (Math.abs(velocity) >= FLICK_PX_PER_MS) return velocity > 0 ? 1 : 0
  if (Math.abs(progress - from) >= COMMIT_SHARE) return from === 1 ? 0 : 1
  return from
}

/** One pointer position, for measuring release speed. */
export interface Sample {
  t: number
  y: number
}

/** How far back release speed is measured over. Older movement is not intent. */
export const VELOCITY_WINDOW_MS = 100

/**
 * Upward speed at release, from the samples inside the last window.
 *
 * A finger that stopped before lifting has no speed, whatever it did before —
 * which is what lets someone drag the sheet halfway, think better of it, hold
 * still and let go without it flying open.
 */
export function releaseVelocity(samples: readonly Sample[], now: number): number {
  const recent = samples.filter((s) => now - s.t <= VELOCITY_WINDOW_MS)
  if (recent.length < 2) return 0
  const first = recent[0]!
  const last = recent[recent.length - 1]!
  const dt = last.t - first.t
  return dt > 0 ? (first.y - last.y) / dt : 0
}
