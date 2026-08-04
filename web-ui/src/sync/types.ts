/** Every synced table. Must match `Entities` in backend/internal/sync/op.go. */
export const ENTITIES = [
  'account',
  'category',
  'txn',
  'budget',
  'recurring_rule',
  'user_setting',
] as const

export type Entity = (typeof ENTITIES)[number]

/** The version pair every synced row carries. */
export interface Version {
  lamport: number
  deviceId: string
}

/** A row as it lives in IndexedDB: its body, plus the sync envelope. */
export type Row = Version & {
  id: string
  updatedAt: number
  deleted: 0 | 1
} & Record<string, unknown>

/** One create, update or delete, as sent to the server. */
export interface Op extends Version {
  entity: Entity
  id: string
  deleted: boolean
  data: Record<string, unknown>
}

/** An Op as it comes back from the server, carrying its place in server order. */
export interface Change extends Op {
  seq?: number
}

export interface PushResponse {
  accepted: number
  rejected: { entity: string; id: string; reason: string }[]
  serverSeq: number
  lamport: number
}

export interface PullResponse {
  changes: Change[]
  serverSeq: number
  hasMore: boolean
}

export interface SnapshotResponse {
  rows: Change[]
  serverSeq: number
  lamport: number
}
