import Dexie, { type EntityTable } from 'dexie'

import type { Entity, Op, Row } from '@/sync/types'

/**
 * The local replica. This — not the API — is what every screen reads, which is
 * why recording a spend in a tunnel works exactly as well as at home.
 *
 * Indexes exist for the queries the dashboard makes: a month of transactions,
 * one category's transactions, live rows only. `deleted` is part of the compound
 * indexes because tombstones stay in the table forever and would otherwise be
 * scanned on every read.
 */
class MoneyflyDB extends Dexie {
  account!: EntityTable<Row, 'id'>
  category!: EntityTable<Row, 'id'>
  txn!: EntityTable<Row, 'id'>
  budget!: EntityTable<Row, 'id'>
  recurring_rule!: EntityTable<Row, 'id'>
  user_setting!: EntityTable<Row, 'id'>

  /** Operations written locally and not yet acknowledged by the server. */
  outbox!: EntityTable<OutboxOp, 'key'>
  /** Cursor, Lamport clock and bootstrap flag. */
  meta!: EntityTable<MetaRow, 'key'>

  constructor(name = 'moneyfly', indexedDB?: IDBFactory) {
    // The injectable factory is what lets a test stand up two independent
    // replicas in one process, which is the only honest way to test convergence.
    super(name, indexedDB ? { indexedDB, IDBKeyRange } : undefined)
    this.version(1).stores({
      account: 'id, deleted, [deleted+archived]',
      category: 'id, deleted, kind, [deleted+kind]',
      txn: 'id, deleted, occurredOn, [deleted+occurredOn], categoryId, accountId',
      budget: 'id, deleted, period',
      recurring_rule: 'id, deleted, nextOn',
      user_setting: 'id, deleted',
      outbox: 'key, entity',
      meta: 'key',
    })
  }

  /** The table holding an entity's rows. Named `rows` because Dexie already has `table`. */
  rows(entity: Entity): EntityTable<Row, 'id'> {
    return this[entity]
  }

  async getMeta<T>(key: string, fallback: T): Promise<T> {
    const row = await this.meta.get(key)
    return row === undefined ? fallback : (row.value as T)
  }

  async setMeta(key: string, value: unknown): Promise<void> {
    await this.meta.put({ key, value })
  }

  /**
   * Empty the replica.
   *
   * Used when a different account signs in on this browser: the previous user's
   * rows must not linger, and the cursor certainly must not — it indexes someone
   * else's change log.
   */
  async reset(): Promise<void> {
    await this.transaction('rw', this.tables, async () => {
      await Promise.all(this.tables.map((t) => t.clear()))
    })
  }
}

export { MoneyflyDB }

/**
 * A pending operation.
 *
 * Keyed on `entity/id` rather than an auto-increment, so editing the same row
 * twice before a flush leaves one op instead of two. Coalescing is safe because
 * ops are last-write-wins: only the latest version of a row matters, and the
 * intermediate one carries no information the server needs.
 */
export interface OutboxOp extends Op {
  key: string
}

export interface MetaRow {
  key: string
  value: unknown
}

export const db = new MoneyflyDB()

export const outboxKey = (entity: Entity, id: string) => `${entity}/${id}`

// --- meta ------------------------------------------------------------------------

export const META_CURSOR = 'cursor'
export const META_LAMPORT = 'lamport'
export const META_BOOTSTRAPPED = 'bootstrapped'
export const META_USER = 'userId'
/**
 * The signed-in account, cached locally.
 *
 * Not a security boundary — it grants nothing the server would refuse, and the
 * replica it unlocks is already on this device. It exists so that opening the
 * app with no network shows your ledger instead of a sign-in form you cannot
 * complete.
 */
export const META_PROFILE = 'userProfile'

/** Clear the default replica. */
export const resetReplica = () => db.reset()
