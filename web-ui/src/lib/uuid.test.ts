import { describe, expect, it } from 'vitest'

import { uuidv7 } from './uuid'

describe('uuidv7', () => {
  it('has the right shape, version and variant', () => {
    const id = uuidv7()
    expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
  })

  it('is unique', () => {
    const ids = new Set(Array.from({ length: 10_000 }, () => uuidv7()))
    expect(ids.size).toBe(10_000)
  })

  // The whole reason for v7 over v4: rows must sort in creation order, including
  // a burst created inside one millisecond (an import does exactly that).
  it('sorts in creation order within a single millisecond', () => {
    const ids = Array.from({ length: 5_000 }, () => uuidv7(1_700_000_000_000))
    expect([...ids].sort()).toEqual(ids)
  })

  it('sorts in creation order across milliseconds', () => {
    const ids = [uuidv7(1_000), uuidv7(2_000), uuidv7(3_000)]
    expect([...ids].sort()).toEqual(ids)
  })

  // A laptop waking up or an NTP correction must not produce an id that sorts
  // into the past and lands a fresh row in the middle of old history.
  it('stays ordered when the clock jumps backwards', () => {
    const before = uuidv7(9_000_000)
    const after = uuidv7(1_000_000)
    expect(after > before).toBe(true)
  })
})
