import type { Change, Row, Version } from './types'

/**
 * Does an incoming version supersede a stored one?
 *
 * Higher Lamport wins; on a tie, the lexicographically greater device id. This
 * function must behave **identically** to `Wins` in backend/internal/sync/op.go.
 * If the two ever disagree, devices silently diverge: each keeps its own winner
 * and neither has any way to notice.
 *
 * The comparison is strict, so an op equal to what is stored does not win. That
 * is what makes applying the log idempotent, and why a device can pull back its
 * own pushes without anything happening.
 */
export function wins(incoming: Version, stored: Version | undefined): boolean {
  if (!stored) return true
  if (incoming.lamport !== stored.lamport) return incoming.lamport > stored.lamport
  return incoming.deviceId > stored.deviceId
}

/**
 * Turn an incoming change into the row to store, or null to keep what we have.
 *
 * A tombstone keeps its body: it costs nothing, and an undo or a debugging
 * session is much easier when the deleted row still says what it was.
 */
export function mergeChange(incoming: Change, stored: Row | undefined): Row | null {
  if (!wins(incoming, stored)) return null
  return {
    ...incoming.data,
    id: incoming.id,
    lamport: incoming.lamport,
    deviceId: incoming.deviceId,
    updatedAt: Date.now(),
    deleted: incoming.deleted ? 1 : 0,
  }
}

/** Strip the sync envelope, leaving the entity's own fields. */
export function bodyOf(row: Row): Record<string, unknown> {
  const { id: _id, lamport: _l, deviceId: _d, updatedAt: _u, deleted: _del, ...body } = row
  return body
}
