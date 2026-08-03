# ADR 0001 — Single static binary with embedded UI and SQLite

**Status:** Accepted · **Date:** 2026-07-29

## Context

The app runs on one small VPS alongside other containers, is maintained by one person, and must be deployable the same way `antlko/upmonitor` already is. The research report left the stack open — "Node/Go/Python", "React or Vue", "SQLite/Postgres" — which is not a decision.

## Decision

One Go binary. Fiber v2 for HTTP. The Vue 3 SPA compiled and embedded via `embed.FS`. SQLite through **`modernc.org/sqlite`** (pure Go). goose for migrations. `/config` volume holding config, database, uploads and backups. Published to `ghcr.io` for `amd64` and `arm64`.

## Rationale

- **`CGO_ENABLED=0` is the load-bearing property.** It makes arm64 a cross-compile rather than an emulated build, and keeps the image distroless.
- Embedding the UI removes version skew between API and frontend — they ship as one artefact or not at all.
- SQLite on local disk beats a network hop for single-user aggregate queries, and reduces backup to copying one directory.
- The author already operates this exact shape.

## Consequences

- **SQLCipher is unavailable** (needs CGO). Not a loss — see [0002](0002-no-encryption.md).
- Writes serialise. Irrelevant at one to three users; the connection pool is capped at 4 with a busy timeout rather than sized for concurrency.
- Postgres later would mean a repository swap. Dialect-specific SQL is therefore avoided, but no abstraction layer is built for a migration that is not planned.
- Frontend changes require a full rebuild. Acceptable; it is one CI job.

## Alternatives rejected

| Option | Why not |
| --- | --- |
| Separate API + static host | Two deployables, version skew, no benefit at this scale |
| Postgres in compose | Second container, second backup story, unused headroom |
| `mattn/go-sqlite3` | Requires CGO; breaks the cross-compile |
| Node or Python backend | Author is a Go engineer; diverges from both reference repos |
