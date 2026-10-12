import { describe, expect, it } from 'vitest'

import { dragSlop, releaseVelocity, settleSheet } from './sheet'

describe('settleSheet', () => {
  it.each([
    // [from, progress, velocity, expected] — each with a real distance moved
    // A flick decides by direction, however little it travelled.
    [0, 0.05, 0.6, 1],
    [1, 0.95, -0.6, 0],
    // …including against the distance already covered.
    [0, 0.8, -0.5, 0],
    [1, 0.2, 0.5, 1],
    // A slow drag commits past a quarter of the way.
    [0, 0.3, 0, 1],
    [1, 0.7, 0, 0],
    [0, 0.25, 0.1, 1],
    // Less than that goes home.
    [0, 0.2, 0.1, 0],
    [1, 0.85, -0.1, 1],
    // A tap that never moved stays put.
    [0, 0, 0, 0],
    [1, 1, 0, 1],
  ] as const)('from %i at %d moving %d → %i', (from, progress, velocity, expected) => {
    expect(settleSheet(from, progress, velocity, 40)).toBe(expected)
  })

  it('does not treat a thumb’s jitter as a flick', () => {
    // 8px at high speed: the finger rolling off a tap, not a swipe.
    expect(settleSheet(0, 0.02, 0.9, 8)).toBe(0)
    expect(settleSheet(1, 0.98, -0.9, -8)).toBe(1)
  })
})

describe('dragSlop', () => {
  it('asks more of a finger than of a mouse', () => {
    expect(dragSlop('touch')).toBeGreaterThan(dragSlop('mouse'))
    expect(dragSlop('pen')).toBe(dragSlop('touch'))
  })
})

describe('releaseVelocity', () => {
  it('measures upward movement as positive', () => {
    const samples = [
      { t: 0, y: 500 },
      { t: 50, y: 470 },
      { t: 100, y: 440 },
    ]
    expect(releaseVelocity(samples, 100)).toBeCloseTo(0.6)
  })

  it('measures downward movement as negative', () => {
    const samples = [
      { t: 0, y: 300 },
      { t: 40, y: 340 },
    ]
    expect(releaseVelocity(samples, 40)).toBeCloseTo(-1)
  })

  it('ignores movement older than the window', () => {
    // A fast drag, then the finger held still for 300ms before lifting.
    const samples = [
      { t: 0, y: 600 },
      { t: 30, y: 500 },
    ]
    expect(releaseVelocity(samples, 330)).toBe(0)
  })

  it('needs two samples to have a speed at all', () => {
    expect(releaseVelocity([], 0)).toBe(0)
    expect(releaseVelocity([{ t: 10, y: 100 }], 10)).toBe(0)
  })
})
