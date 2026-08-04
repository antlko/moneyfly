import { describe, expect, it } from 'vitest'

import { mergeChange, wins } from './lww'
import type { Change, Row, Version } from './types'

const v = (lamport: number, deviceId: string): Version => ({ lamport, deviceId })

describe('wins', () => {
  /*
   * These cases mirror TestWins in backend/internal/sync/op_test.go one for one.
   * The two implementations have to agree exactly — if they drift, devices keep
   * different winners and nothing ever reports a problem. Changing a case here
   * without changing it there is the bug this table exists to make obvious.
   */
  it.each([
    ['higher lamport wins', v(5, 'a'), v(4, 'z'), true],
    ['lower lamport loses', v(4, 'z'), v(5, 'a'), false],
    ['tie broken by greater device id', v(5, 'z'), v(5, 'a'), true],
    ['tie broken against lesser device id', v(5, 'a'), v(5, 'z'), false],
    ['identical version does not win', v(5, 'a'), v(5, 'a'), false],
  ])('%s', (_name, incoming, stored, want) => {
    expect(wins(incoming as Version, stored as Version)).toBe(want)
  })

  it('a first sighting always wins', () => {
    expect(wins(v(1, 'a'), undefined)).toBe(true)
  })

  // Whatever order two devices' writes arrive in, both must pick the same
  // winner. This is the property the protocol rests on.
  it('is a total order', () => {
    const versions = [v(1, 'a'), v(1, 'b'), v(2, 'a'), v(2, 'b'), v(10, 'a'), v(10, 'z')]
    for (const x of versions) {
      for (const y of versions) {
        const same = x.lamport === y.lamport && x.deviceId === y.deviceId
        if (same) {
          expect(wins(x, y) || wins(y, x)).toBe(false)
        } else {
          expect(wins(x, y)).not.toBe(wins(y, x))
        }
      }
    }
  })
})

describe('mergeChange', () => {
  const change = (lamport: number, deviceId: string, note: string, deleted = false): Change => ({
    entity: 'txn',
    id: 't1',
    lamport,
    deviceId,
    deleted,
    data: { note },
  })

  const stored = (lamport: number, deviceId: string, note: string): Row => ({
    id: 't1',
    lamport,
    deviceId,
    updatedAt: 0,
    deleted: 0,
    note,
  })

  it('takes a newer change', () => {
    const row = mergeChange(change(2, 'b', 'newer'), stored(1, 'a', 'older'))
    expect(row?.note).toBe('newer')
    expect(row?.lamport).toBe(2)
  })

  it('keeps a newer stored row', () => {
    expect(mergeChange(change(1, 'a', 'older'), stored(2, 'b', 'newer'))).toBeNull()
  })

  // Re-applying the log must change nothing — that is what lets a device pull
  // back its own pushes, and lets a flaky connection be retried.
  it('is idempotent', () => {
    const c = change(3, 'a', 'x')
    const first = mergeChange(c, undefined)
    expect(first).not.toBeNull()
    expect(mergeChange(c, first!)).toBeNull()
  })

  it('applies a tombstone but keeps the body', () => {
    const row = mergeChange(change(2, 'a', 'gone', true), stored(1, 'a', 'here'))
    expect(row?.deleted).toBe(1)
    expect(row?.note).toBe('gone')
  })

  // An edit with a higher lamport genuinely happened after the delete.
  it('resurrects on a later edit', () => {
    const deletedRow: Row = { ...stored(2, 'a', 'gone'), deleted: 1 }
    const row = mergeChange(change(3, 'b', 'back'), deletedRow)
    expect(row?.deleted).toBe(0)
    expect(row?.note).toBe('back')
  })
})
