import { ref, shallowRef } from 'vue'

import { ApiError, syncPull, syncPush, syncSnapshot } from '@/api/http'
import {
  db as defaultDB,
  META_BOOTSTRAPPED,
  META_CURSOR,
  META_LAMPORT,
  META_USER,
  outboxKey,
  type MoneyflyDB,
  type OutboxOp,
} from '@/db'
import { deviceId } from '@/lib/device'
import { plain } from '@/lib/plain'
import { uuidv7 } from '@/lib/uuid'
import { mergeChange } from './lww'
import type {
  Change,
  Entity,
  Op,
  PullResponse,
  PushResponse,
  Row,
  SnapshotResponse,
} from './types'

export type SyncState = 'idle' | 'syncing' | 'offline' | 'error'

/** How often to sync when nothing else prompts it. */
const POLL_INTERVAL_MS = 30_000
/** How many pending ops go in one push. */
const PUSH_BATCH = 500

/** What the engine needs from the server. Injectable so tests can stand one up. */
export interface Transport {
  push(deviceId: string, ops: Op[]): Promise<PushResponse>
  pull(since: number, deviceId: string): Promise<PullResponse>
  snapshot(): Promise<SnapshotResponse>
}

const httpTransport: Transport = {
  push: syncPush,
  pull: syncPull,
  snapshot: syncSnapshot,
}

/**
 * The sync engine.
 *
 * The contract every screen depends on: **a write never waits on the network.**
 * `write()` and `remove()` touch IndexedDB and return; the outbox and this
 * engine are what eventually talk to the server. Sync state is something the UI
 * displays, never something it blocks on.
 *
 * See docs/SYNC.md for the protocol. The conflict rule lives in ./lww.ts and has
 * a twin in Go that must stay identical.
 */
export class SyncEngine {
  readonly state = ref<SyncState>('idle')
  readonly pending = ref(0)
  readonly lastSyncAt = ref<number | null>(null)
  readonly error = shallowRef<string | null>(null)
  /**
   * Whether the event stream is up. Reported separately from `state` because the
   * two answer different questions: this one is "will another device's change
   * reach me promptly", `state` is "did my last exchange with the server work".
   * With the stream down, sync still happens — just on the 30-second poll.
   */
  readonly streamConnected = ref(false)
  /** Bumped after every local or remote change, for views to re-query on. */
  readonly revision = ref(0)

  private lamport = 0
  private cursor = 0
  private running = false
  private inflight: Promise<void> | null = null
  private stream: EventSource | null = null
  private timer: ReturnType<typeof setInterval> | null = null

  private resolvedDevice: string | undefined

  constructor(
    private readonly db: MoneyflyDB = defaultDB,
    device?: string,
    private readonly transport: Transport = httpTransport,
  ) {
    this.resolvedDevice = device
  }

  /**
   * This replica's device id.
   *
   * Resolved lazily, not in the constructor: the app-wide engine is created at
   * module load, and reading localStorage there would make the module
   * un-importable anywhere without a DOM — including in its own tests.
   */
  private get device(): string {
    return (this.resolvedDevice ??= deviceId())
  }

  get deviceID(): string {
    return this.device
  }

  /** The local replica this engine drives. */
  get replica(): MoneyflyDB {
    return this.db
  }

  /**
   * Prepare the replica for a user and start syncing.
   *
   * A different account on the same browser gets a clean replica: their rows
   * must not linger, and the cursor certainly must not — it indexes someone
   * else's change log.
   */
  async start(userId: string): Promise<void> {
    if (this.running) return
    this.running = true

    const previous = await this.db.getMeta<string | null>(META_USER, null)
    if (previous !== userId) {
      await this.db.reset()
      await this.db.setMeta(META_USER, userId)
    }

    this.lamport = await this.db.getMeta(META_LAMPORT, 0)
    this.cursor = await this.db.getMeta(META_CURSOR, 0)
    await this.refreshPending()

    if (!(await this.db.getMeta(META_BOOTSTRAPPED, false))) {
      await this.bootstrap()
    }

    this.listen()
    await this.sync()
  }

  stop(): void {
    this.running = false
    this.stream?.close()
    this.stream = null
    if (this.timer) clearInterval(this.timer)
    this.timer = null
    if (typeof window !== 'undefined') {
      window.removeEventListener('online', this.onOnline)
      document.removeEventListener('visibilitychange', this.onVisible)
    }
  }

  // --- local writes ---------------------------------------------------------------

  /**
   * Create or update a row. Returns its id.
   *
   * The id is minted here (UUIDv7) rather than by the server — that is what lets
   * a row be created with no connection and still have its final identity.
   */
  async write(entity: Entity, body: Record<string, unknown>, id?: string): Promise<string> {
    const rowId = id ?? uuidv7()
    await this.record(entity, rowId, body, false)
    return rowId
  }

  /**
   * Delete a row.
   *
   * A tombstone, never a real delete: a removed row has to propagate, and there
   * is nothing to send if it is gone.
   */
  async remove(entity: Entity, id: string): Promise<void> {
    const existing = await this.db.rows(entity).get(id)
    await this.record(entity, id, existing ? bodyOf(existing) : {}, true)
  }

  private async record(
    entity: Entity,
    id: string,
    input: Record<string, unknown>,
    deleted: boolean,
  ): Promise<void> {
    // Stores hand over reactive proxies (a row read back through `useLiveQuery`,
    // an array held in a store's state), and structured clone refuses those. The
    // whole body is normalised once, here, so both the row and the outbox entry
    // are storable and identical.
    const body = plain(input)

    // One transaction over the row, the outbox and the clock: a write that made
    // it into the table but not the outbox would never reach the server, and a
    // reload between the two would never notice.
    await this.db.transaction(
      'rw',
      [this.db.rows(entity), this.db.outbox, this.db.meta],
      async () => {
        const lamport = ++this.lamport
        const row: Row = {
          ...body,
          id,
          lamport,
          deviceId: this.device,
          updatedAt: Date.now(),
          deleted: deleted ? 1 : 0,
        }
        const op: OutboxOp = {
          key: outboxKey(entity, id),
          entity,
          id,
          lamport,
          deviceId: this.device,
          deleted,
          data: body,
        }
        await this.db.rows(entity).put(row)
        await this.db.outbox.put(op)
        await this.db.setMeta(META_LAMPORT, lamport)
      },
    )

    this.revision.value++
    await this.refreshPending()
    void this.sync()
  }

  // --- server exchange -------------------------------------------------------------

  /**
   * Push everything pending, then pull everything new.
   *
   * A call made while a sync is already running joins that one rather than being
   * dropped, so awaiting this always means "a sync finished", never "a sync was
   * skipped because one happened to be in flight".
   */
  sync(): Promise<void> {
    if (!this.running) return Promise.resolve()
    if (this.inflight) return this.inflight
    this.inflight = this.runSync().finally(() => {
      this.inflight = null
    })
    return this.inflight
  }

  private async runSync(): Promise<void> {
    this.state.value = 'syncing'
    try {
      await this.flush()
      await this.pull()
      this.state.value = 'idle'
      this.error.value = null
      this.lastSyncAt.value = Date.now()
    } catch (e) {
      // A failed sync is normal (a tunnel, a rebooting server) and must never
      // discard pending work — the outbox is left exactly as it was.
      if (isNetworkError(e)) {
        this.state.value = 'offline'
      } else {
        this.state.value = 'error'
        this.error.value = e instanceof Error ? e.message : String(e)
      }
    } finally {
      await this.refreshPending()
    }
  }

  private async flush(): Promise<void> {
    for (;;) {
      const batch = await this.db.outbox.limit(PUSH_BATCH).toArray()
      if (batch.length === 0) return

      const res = await this.transport.push(this.device, batch.map(toOp))

      await this.db.transaction('rw', this.db.outbox, async () => {
        for (const op of batch) {
          // Only clear what was actually sent. The user may have edited the same
          // row while the request was in flight; that newer op has a higher
          // lamport and must survive.
          const current = await this.db.outbox.get(op.key)
          if (current && current.lamport === op.lamport) {
            await this.db.outbox.delete(op.key)
          }
        }
      })

      if (res.rejected.length > 0) {
        // A rejection is a client bug, not a conflict — conflicts resolve
        // silently. Surface it rather than retrying something the server will
        // never accept.
        console.error('sync: server rejected operations', res.rejected)
      }
      await this.raiseLamport(res.lamport)
      if (batch.length < PUSH_BATCH) return
    }
  }

  private async pull(): Promise<void> {
    for (;;) {
      let res: PullResponse
      try {
        res = await this.transport.pull(this.cursor, this.device)
      } catch (e) {
        // 409 means our cursor predates the surviving change log, so replaying
        // would silently skip rows. Start over from a snapshot.
        if (e instanceof ApiError && e.status === 409) {
          await this.bootstrap()
          return
        }
        throw e
      }

      if (res.changes.length > 0) {
        await this.applyChanges(res.changes)
      }
      this.cursor = res.serverSeq
      await this.db.setMeta(META_CURSOR, this.cursor)
      if (!res.hasMore) return
    }
  }

  /** Replace the whole replica from the server's current state. */
  private async bootstrap(): Promise<void> {
    const snap = await this.transport.snapshot()

    await this.db.transaction('rw', this.db.tables, async () => {
      // Clear the rows but keep the outbox: work written offline has not reached
      // the server yet, so it is not in the snapshot and dropping it would lose
      // it outright.
      for (const t of this.db.tables) {
        if (t.name !== 'outbox' && t.name !== 'meta') await t.clear()
      }
    })

    await this.applyChanges(snap.rows)

    this.cursor = snap.serverSeq
    await this.db.setMeta(META_CURSOR, this.cursor)
    await this.db.setMeta(META_BOOTSTRAPPED, true)
    await this.raiseLamport(snap.lamport)

    // Re-apply anything still pending on top of the snapshot, so the UI does not
    // briefly lose an offline write.
    const stillPending = await this.db.outbox.toArray()
    for (const op of stillPending) {
      const row = mergeChange(op, await this.db.rows(op.entity).get(op.id))
      if (row) await this.db.rows(op.entity).put(row)
    }
    this.revision.value++
  }

  private async applyChanges(changes: Change[]): Promise<void> {
    let maxLamport = 0
    await this.db.transaction('rw', this.db.tables, async () => {
      for (const change of changes) {
        maxLamport = Math.max(maxLamport, change.lamport)
        const row = mergeChange(change, await this.db.rows(change.entity).get(change.id))
        // null means the stored version is newer or identical — including when
        // this is our own op coming back, which is why echoes need no filtering.
        if (row) await this.db.rows(change.entity).put(row)
      }
    })

    await this.raiseLamport(maxLamport)
    this.revision.value++
  }

  /** The clock only ever moves forward, on this device and across the account. */
  private async raiseLamport(seen: number): Promise<void> {
    if (seen <= this.lamport) return
    this.lamport = seen
    await this.db.setMeta(META_LAMPORT, this.lamport)
  }

  private async refreshPending(): Promise<void> {
    this.pending.value = await this.db.outbox.count()
  }

  // --- wake-ups ---------------------------------------------------------------------

  private listen(): void {
    if (typeof window === 'undefined') return

    this.openStream()
    // The poll is the safety net, not the mechanism: it covers a dropped event
    // stream and a proxy that eats SSE entirely.
    this.timer = setInterval(() => void this.sync(), POLL_INTERVAL_MS)
    window.addEventListener('online', this.onOnline)
    document.addEventListener('visibilitychange', this.onVisible)
  }

  private onOnline = () => void this.sync()
  private onVisible = () => {
    if (document.visibilityState === 'visible') void this.sync()
  }

  private openStream(): void {
    if (typeof EventSource === 'undefined') return
    this.stream?.close()

    const stream = new EventSource('/api/sync/events')
    stream.addEventListener('ready', () => {
      this.streamConnected.value = true
    })
    stream.addEventListener('changed', (ev) => {
      try {
        const data = JSON.parse((ev as MessageEvent).data) as { deviceId?: string }
        // Our own push already updated the replica; pulling it back would be a
        // no-op, so skip the round trip.
        if (data.deviceId === this.device) return
      } catch {
        /* malformed event — sync anyway, it costs one request */
      }
      void this.sync()
    })
    stream.onerror = () => {
      this.streamConnected.value = false
      // A dropped stream is NOT evidence that the app is offline — a proxy
      // timeout, a laptop lid, or a dev-server reload all land here while the
      // API is perfectly reachable. Only a failed sync request can decide that,
      // so ask for one and let it set the state. EventSource reconnects on its
      // own using the server's `retry` hint.
      void this.sync()
    }
    this.stream = stream
  }
}

function toOp(op: OutboxOp): Op {
  const { key: _key, ...rest } = op
  return rest
}

function bodyOf(row: Row): Record<string, unknown> {
  const { id: _i, lamport: _l, deviceId: _d, updatedAt: _u, deleted: _del, ...body } = row
  return body
}

/** A transport failure, as opposed to the server saying no. */
function isNetworkError(e: unknown): boolean {
  return !(e instanceof ApiError)
}

/** The engine this app uses. Tests build their own. */
export const sync = new SyncEngine()
