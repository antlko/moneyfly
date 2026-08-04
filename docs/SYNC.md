# Sync

How a spend recorded on a phone with no signal ends up on a laptop, and what happens when two
devices edit the same row. Read this before touching `backend/internal/sync` or `web-ui/src/sync`.

> Implemented. Server: `backend/internal/sync` (protocol + validation) and
> `backend/internal/db/sync.go` (storage). Client: `web-ui/src/sync`. If the code and this document
> ever disagree, one of the two is a bug — decide which, and fix it in the same change.

## 1. The shape of it

IndexedDB on each device is a **full replica** of that user's domain rows, and it is what the UI
reads. Writing a transaction is a local write plus an entry on a push queue. Nothing in the render
path waits on the network.

```
  UI  ──write──▶ IndexedDB ──▶ push queue ──POST /api/sync/push──▶ SQLite + change_log
                     ▲                                                     │
                     └──────apply──── GET /api/sync/pull ◀───SSE "seq=N"───┘
```

## 2. Versioning: a Lamport clock per row

Every domain row carries `lamport`, `device_id`, `updated_at` and `deleted`.

- A local write sets `lamport = ++localClock`.
- After a pull, `localClock = max(localClock, max(incoming lamport))`.
- The server keeps a per-user high-water mark and returns it, so a freshly bootstrapped device does
  not start at 0 and lose every comparison.

**Resolution rule, applied identically on both sides:**

```
incoming wins  ⟺  (incoming.lamport, incoming.device_id) > (stored.lamport, stored.device_id)
```

Lamport first; on a tie, the lexicographically greater `device_id`. The tiebreak exists purely to
make the outcome deterministic — without it two devices can each keep their own version forever and
never notice they disagree.

Consequences worth stating:

- **Re-applying an op is a no-op**, because it is not *greater* than what it already produced. That
  is what makes the protocol safe to retry, and why a device pulling back its own push is harmless.
- **The result does not depend on delivery order.** Any interleaving of the same op set converges to
  the same state. The phase-2 test suite asserts exactly this over shuffled op sequences.
- **Last write wins per row, not per field.** Two devices editing different fields of one
  transaction will lose one of the edits. That is accepted: transactions are small, edits are rare,
  and field-level merge would mean CRDTs.

### Why last-write-wins is enough here

Because **balances are never stored**. An account balance is its opening balance plus the sum of its
transactions, recomputed on read. There is no counter to merge, so the only possible conflict is two
devices editing the same row — the case LWW handles. Introducing a stored running balance would
break this argument and force a CRDT counter; don't.

### Deletes

Deleting sets `deleted = 1` and bumps the lamport. Rows are never removed by user action — a hard
`DELETE` cannot propagate, because there is nothing left to send. Tombstones are trimmed only by
retention, long after every device has seen them.

An edit that arrives after a delete, with a higher lamport, **resurrects the row**. This is correct:
the edit genuinely happened later.

## 3. Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/sync/push` | Apply a batch of ops; append to `change_log` |
| `GET` | `/api/sync/pull?since=N&limit=500` | Deltas after cursor `N` |
| `GET` | `/api/sync/snapshot` | Every current row + the current `serverSeq` |
| `GET` | `/api/sync/events` | SSE: "something changed, seq=N" |

### push

```jsonc
// request
{
  "deviceId": "01JD...",
  "ops": [
    {
      "entity": "txn",
      "id": "01JDX...",              // UUIDv7, minted on the client
      "lamport": 412,
      "deviceId": "01JD...",
      "deleted": false,
      "data": { "kind": "expense", "occurredOn": "2026-08-03", "amountMinor": -1440, ... }
    }
  ]
}

// response
{ "serverSeq": 9182, "lamport": 415, "rejected": [] }
```

The server ignores any `userId` in the payload and takes it from the session. An op naming a row
owned by someone else is **rejected**, never applied — it lands in `rejected` with a reason.

### pull

```jsonc
{ "changes": [ /* same op shape, plus "seq" */ ], "serverSeq": 9182, "hasMore": false }
```

The client persists `serverSeq` as its cursor per `(user, server)`. Its own ops come back in this
stream; applying them is a no-op by the resolution rule, so they are not filtered out. Filtering by
`deviceId` would be a fragile optimisation hiding a correctness requirement.

### How rows are stored

Every synced table has the same shape: `(user_id, id)` as the primary key, the `(lamport, device_id)`
version pair, a `deleted` flag, and the row body as **JSON** in a `data` column. The columns the
server actually queries — a transaction's date, category, amount — are SQLite **virtual generated
columns** over that JSON, so they are indexable, always consistent with the body, and cost no
storage.

Two consequences are load-bearing:

* There are **no foreign keys between synced tables and no unique constraints on anything a client
  sends**. Ops arrive in arbitrary order (a transaction routinely lands before the category it
  names), and the same logical row can be pushed from two devices. A constraint failure in the sync
  write path would wedge that device into retrying forever, so **the write path must not be able to
  fail on data**. De-duplication — the importer's natural key — is a query against an index, never a
  constraint.
* Adding a field is a client-side change. Only a field the *server* needs to query requires a
  migration, and then only to add a generated column.

### snapshot — and why it is not optional

`change_log` is trimmed to `sync.change_log_retention_days`. The moment retention runs,
`pull?since=0` stops being a complete history: it would hand a new device the recent deltas and none
of the older rows, producing a silently partial replica.

**So a device with no cursor always bootstraps from `/api/sync/snapshot`**, then follows deltas from
the `serverSeq` it returned. The same path handles a device that has been offline longer than
retention: `sync_state.trimmed_before` records the sequence below which replay is impossible, and a
pull with an older cursor is answered with **409 `resync required`** — a well-formed request whose
assumption about the world is stale. The client re-bootstraps.

The snapshot reads `serverSeq` **before** the rows, never after. A change committed in between is
then delivered twice, which is harmless because applying an op twice is a no-op; reading the
sequence last would instead let that change fall through the gap entirely.

The snapshot omits tombstones — a device with nothing has nothing to delete — and bootstrap
deliberately **keeps the outbox**: work written offline has not reached the server, so it is not in
the snapshot, and clearing it would destroy the only copy. Pending ops are re-applied on top of the
snapshot so the UI never briefly loses an offline write.

### events (SSE)

`GET /api/sync/events` streams `{"seq": 9182, "deviceId": "..."}` whenever a write lands. The client
reacts by pulling. SSE rather than WebSocket because the need is one-directional, `EventSource`
reconnects on its own, and it survives any reverse proxy. Fallbacks: pull on `visibilitychange`,
`online`, and a 30-second poll.

Notifications are per user and are **dropped rather than queued** when a subscriber's one-slot buffer
is full. That is safe precisely because the event carries no data: a pending wake-up already says
"there is something new", and a blocking send would let one stalled reader hold up a write.

**A dropped stream is not evidence that the app is offline.** A proxy timeout, a closed laptop lid or
a dev-server reload all break the stream while the API stays perfectly reachable, so a stream error
never sets the offline state — it triggers a sync and lets the outcome of a real request decide.
The engine reports the two separately: `state` (did the last exchange work) and `streamConnected`
(will another device's change reach me promptly, or only on the next poll).

## 4. IDs and clocks

- Row ids are **UUIDv7 minted on the client** — sortable by creation time, and no server round-trip
  is needed to create a row offline.
- `device_id` is minted once per browser profile and stored in **localStorage**, not IndexedDB
  (`web-ui/src/lib/device.ts`). Two reasons: it is needed synchronously, at sign-in; and it must
  outlive the replica, so that when Safari evicts IndexedDB the device comes back with the same
  identity and re-bootstraps, instead of appearing as a brand new device after every eviction. A
  private window is a different device, which is exactly what makes it a usable test rig.
- `occurred_on` is a plain `YYYY-MM-DD` string with no timezone, matching both Monefy and its CSV. A
  transaction happens on a date, not at an instant; storing a timestamp would make the same purchase
  land in different months for different devices.

## 5. Storage on the client

IndexedDB via Dexie. Two caveats to respect:

- **Safari evicts IndexedDB after ~7 days without use** unless storage is persisted. The app calls
  `navigator.storage.persist()`; if it is refused, eviction costs a re-bootstrap from the server and
  no data, because the server holds everything.
- **Unpushed local ops are the one thing not on the server.** The push queue must survive reload
  (it lives in IndexedDB, not memory) and must not be cleared until the server has acknowledged.

## 6. What to test

The phase-2 suite is not done until these pass:

1. **Convergence.** Generate op sequences from two devices, apply them in every interleaving, assert
   identical final state.
2. **Tiebreak determinism.** Equal lamports, different device ids — both sides pick the same winner.
3. **Idempotence.** Applying the same op twice changes nothing.
4. **Resurrection.** Edit-after-delete with a higher lamport brings the row back.
5. **Bootstrap after trim.** Run retention, then bootstrap a new device, and assert its replica
   equals the server's state.
6. **Scoping.** An op referencing another user's row id is rejected and does not modify that row.
