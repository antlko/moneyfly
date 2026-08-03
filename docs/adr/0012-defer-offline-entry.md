# ADR 0012 — Defer offline expense entry

**Status:** Accepted · **Date:** 2026-07-29

## Context

With encryption dropped ([0002](0002-no-encryption.md)), offline entry became the largest remaining architectural question on the client. The brief wants the PWA to be usable "like Monefy", and Monefy works offline because it is a local-first native app.

## Decision

**Deferred.** The service worker caches the **app shell only** — HTML, JS, CSS, fonts, icons. API responses are not cached. Offline shows a clear offline state. No write queue, no local mutation log.

## Rationale

- The real cost is not the queue; it is everything the queue implies: conflict resolution when two devices both queued edits, duplicate money and FX logic on the client, a client-side copy of the natural-key dedup rule, and a sync protocol with its own failure modes and tests.
- The actual workflow is **one bulk import a month** from Monefy via Telegram. Offline entry serves a use case that does not yet exist.
- Monefy remains the offline capture tool for now. This app is the analysis and reporting layer. That division is honest about what each is good at.
- Shell-only caching sidesteps iOS's ~7-day storage eviction entirely: eviction costs one reload, never data. A write queue evicted before sync would lose transactions — the worst possible outcome for a finance app.

## Consequences

- **No expense can be recorded without a connection.** The clearest limitation in the product, and it is stated plainly rather than hidden.
- The PWA is not yet a full Monefy replacement. If Monefy access were lost tomorrow, entry would require connectivity — usually fine, sometimes not.
- The UI must handle offline gracefully: shell loads, an unmistakable offline banner, and no save button that appears to work and then does not.
- Quick entry is built as if it will one day be offline-capable — optimistic UI, a draft that survives a failed save, no assumption of a synchronous server round trip in the interaction model. That keeps the later addition a matter of adding a queue rather than reworking the flow.

## Revisit when

- The PWA is genuinely used for daily entry instead of Monefy, **or**
- Monefy access is actually lost, **or**
- A save is attempted offline often enough to be annoying — worth instrumenting, so the decision can be made on evidence rather than intuition.

At that point the design is: an IndexedDB mutation queue, client-generated UUIDs for idempotency (the API already accepts `Idempotency-Key`), last-write-wins on `updated_at` with conflicts surfaced rather than resolved silently, and a visible pending-sync count.
