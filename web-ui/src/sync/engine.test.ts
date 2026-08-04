import 'fake-indexeddb/auto'

import { IDBFactory } from 'fake-indexeddb'
import { beforeEach, describe, expect, it } from 'vitest'

import { ApiError } from '@/api/http'
import { MoneyflyDB } from '@/db'
import { SyncEngine, type Transport } from './engine'
import { wins } from './lww'
import type { Change, Op, Row } from './types'

/**
 * An in-memory stand-in for the server, implementing the same protocol.
 *
 * It resolves conflicts with the same `wins` the client uses, which makes this a
 * test of the *engine* — the outbox, the cursor, bootstrap, offline behaviour —
 * rather than of the rule itself. The rule's agreement with Go is covered by the
 * mirrored case tables in lww.test.ts and backend/internal/sync/op_test.go.
 */
class FakeServer implements Transport {
  rows = new Map<string, Change>()
  log: Change[] = []
  lamport = 0
  online = true
  trimmedBefore = 0

  private assertOnline() {
    // A transport failure, not an API error — the engine must treat these
    // differently, and only one of them means "the server said no".
    if (!this.online) throw new TypeError('Failed to fetch')
  }

  /** Lets a test make push fail in a specific way. */
  pushImpl: (() => never) | null = null

  async push(_deviceId: string, ops: Op[]) {
    if (this.pushImpl) this.pushImpl()
    this.assertOnline()
    let accepted = 0
    for (const op of ops) {
      const key = `${op.entity}/${op.id}`
      if (!wins(op, this.rows.get(key))) continue
      this.rows.set(key, { ...op })
      this.log.push({ ...op, seq: this.log.length + 1 })
      this.lamport = Math.max(this.lamport, op.lamport)
      accepted++
    }
    return { accepted, rejected: [], serverSeq: this.log.length, lamport: this.lamport }
  }

  async pull(since: number) {
    this.assertOnline()
    if (since > 0 && since < this.trimmedBefore) throw new ApiError(409, 'resync required')
    return {
      changes: this.log.filter((c) => (c.seq ?? 0) > since),
      serverSeq: this.log.length,
      hasMore: false,
    }
  }

  async snapshot() {
    this.assertOnline()
    return {
      rows: [...this.rows.values()].filter((r) => !r.deleted),
      serverSeq: this.log.length,
      lamport: this.lamport,
    }
  }

  /** Drop the journal, as retention does, forcing a re-bootstrap. */
  trim() {
    this.trimmedBefore = this.log.length + 1
    this.log = []
  }
}

let dbCounter = 0

function newDevice(server: FakeServer, name: string) {
  // A fresh IDBFactory per device is what makes these genuinely independent
  // replicas rather than one database with two labels.
  const db = new MoneyflyDB(`test-${name}-${dbCounter++}`, new IDBFactory())
  return new SyncEngine(db, name, server)
}

/** Run sync to a fixed point — a second round guarantees the first's writes went. */
async function settle(...engines: SyncEngine[]) {
  for (let round = 0; round < 3; round++) {
    for (const e of engines) await e.sync()
  }
}

async function live(engine: SyncEngine): Promise<Row[]> {
  const rows = await engine.replica.txn.where('deleted').equals(0).toArray()
  return rows.sort((a, b) => a.id.localeCompare(b.id))
}

const expense = (note: string) => ({
  kind: 'expense',
  occurredOn: '2026-08-03',
  amountMinor: -1440,
  currency: 'EUR',
  note,
})

describe('SyncEngine', () => {
  let server: FakeServer

  beforeEach(() => {
    server = new FakeServer()
  })

  it('pushes a local write and delivers it to another device', async () => {
    const a = newDevice(server, 'dev-a')
    const b = newDevice(server, 'dev-b')
    await a.start('user-1')
    await b.start('user-1')

    await a.write('txn', expense('coffee'))
    await settle(a, b)

    expect((await live(b)).map((r) => r.note)).toEqual(['coffee'])
    expect(a.pending.value).toBe(0)
  })

  // The headline promise: recording a spend with no connection works, and the
  // work survives until the connection comes back.
  it('keeps offline writes and delivers them on reconnect', async () => {
    const a = newDevice(server, 'dev-a')
    const b = newDevice(server, 'dev-b')
    await a.start('user-1')
    await b.start('user-1')

    server.online = false
    await a.write('txn', expense('tunnel coffee'))
    await a.write('txn', expense('tunnel bread'))
    await settle(a)

    expect(a.state.value).toBe('offline')
    expect(a.pending.value).toBe(2)
    // The UI sees them immediately regardless.
    expect(await live(a)).toHaveLength(2)
    expect(await live(b)).toHaveLength(0)

    server.online = true
    await settle(a, b)

    expect(a.pending.value).toBe(0)
    expect(a.state.value).toBe('idle')
    expect((await live(b)).map((r) => r.note).sort()).toEqual(['tunnel bread', 'tunnel coffee'])
  })

  it('converges when both devices write offline', async () => {
    const a = newDevice(server, 'dev-a')
    const b = newDevice(server, 'dev-b')
    await a.start('user-1')
    await b.start('user-1')

    server.online = false
    await a.write('txn', expense('from a'))
    await b.write('txn', expense('from b'))
    await settle(a, b)

    server.online = true
    await settle(a, b, a, b)

    const seenByA = (await live(a)).map((r) => r.note).sort()
    const seenByB = (await live(b)).map((r) => r.note).sort()
    expect(seenByA).toEqual(['from a', 'from b'])
    expect(seenByB).toEqual(seenByA)
  })

  // The conflict case: one row, two edits, no connection. Both sides must end up
  // showing the same winner — disagreeing silently is the failure this whole
  // design exists to prevent.
  it('resolves a concurrent edit of one row identically on both devices', async () => {
    const a = newDevice(server, 'dev-a')
    const b = newDevice(server, 'dev-b')
    await a.start('user-1')
    await b.start('user-1')

    const id = await a.write('txn', expense('original'))
    await settle(a, b)

    server.online = false
    await a.write('txn', expense('edited on a'), id)
    await b.write('txn', expense('edited on b'), id)
    await settle(a, b)
    server.online = true
    await settle(a, b, a, b)

    const [rowA] = await live(a)
    const [rowB] = await live(b)
    expect(rowA.note).toBe(rowB.note)
    expect(rowA.lamport).toBe(rowB.lamport)
    expect(rowA.deviceId).toBe(rowB.deviceId)
    // Same lamport on both sides, so the device id broke the tie — and 'dev-b'
    // sorts after 'dev-a'.
    expect(rowA.note).toBe('edited on b')
  })

  it('propagates a delete', async () => {
    const a = newDevice(server, 'dev-a')
    const b = newDevice(server, 'dev-b')
    await a.start('user-1')
    await b.start('user-1')

    const id = await a.write('txn', expense('mistake'))
    await settle(a, b)
    expect(await live(b)).toHaveLength(1)

    await a.remove('txn', id)
    await settle(a, b)
    expect(await live(b)).toHaveLength(0)
  })

  // A device that has been away longer than the journal survives must rebuild,
  // and must not lose the work it made while it was away.
  it('re-bootstraps after the journal is trimmed, keeping unsent work', async () => {
    const a = newDevice(server, 'dev-a')
    const b = newDevice(server, 'dev-b')
    await a.start('user-1')
    await b.start('user-1')

    await a.write('txn', expense('before the gap'))
    await settle(a, b)

    // b goes away; a keeps working; retention trims the journal.
    await a.write('txn', expense('while b was away'))
    await settle(a)
    server.trim()

    // b comes back with an offline write of its own still pending.
    server.online = false
    await b.write('txn', expense('b was offline too'))
    await settle(b)
    server.online = true
    await settle(b, a, b)

    const notes = (await live(b)).map((r) => r.note).sort()
    expect(notes).toEqual(['b was offline too', 'before the gap', 'while b was away'])
    expect(b.pending.value).toBe(0)
    expect((await live(a)).map((r) => r.note).sort()).toEqual(notes)
  })

  // Signing in as someone else must not leave the previous account's ledger in
  // the browser, nor their cursor — it indexes a different change log.
  it('wipes the replica when a different user signs in', async () => {
    const a = newDevice(server, 'dev-a')
    await a.start('user-1')
    await a.write('txn', expense('user one'))
    await settle(a)
    expect(await live(a)).toHaveLength(1)

    const reused = new SyncEngine(a.replica, 'dev-a', new FakeServer())
    await reused.start('user-2')
    expect(await live(reused)).toHaveLength(0)
  })

  /*
   * "Offline" has to mean the network, and nothing else.
   *
   * The check used to be `!(e instanceof ApiError)` — anything that was not an
   * HTTP response was blamed on the connection. That swept up every local
   * failure and reported it as offline while the server was answering 200,
   * which sends you to check a connection that was never the problem.
   */
  it('reports a local failure as an error, not as offline', async () => {
    const a = newDevice(server, 'dev-a')
    await a.start('user-1')

    const broken = new Error('IndexedDB transaction aborted')
    server.pushImpl = () => {
      throw broken
    }
    await a.write('txn', expense('one'))
    await settle(a)

    expect(a.state.value).toBe('error')
    expect(a.error.value).toBe('IndexedDB transaction aborted')
  })

  it('still calls a transport failure offline', async () => {
    const a = newDevice(server, 'dev-a')
    await a.start('user-1')

    server.pushImpl = () => {
      throw new TypeError('Failed to fetch')
    }
    await a.write('txn', expense('one'))
    await settle(a)

    expect(a.state.value).toBe('offline')
  })

  it('reports pending work while offline', async () => {
    const a = newDevice(server, 'dev-a')
    await a.start('user-1')

    server.online = false
    await a.write('txn', expense('one'))
    await settle(a)
    expect(a.pending.value).toBe(1)

    // Editing the same row again coalesces rather than queueing a second op:
    // only the latest version of a row carries information.
    const rows = await live(a)
    await a.write('txn', expense('one, edited'), rows[0].id)
    await settle(a)
    expect(a.pending.value).toBe(1)
  })
})
