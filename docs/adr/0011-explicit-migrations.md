# ADR 0011 — Migrations run explicitly, not at boot

**Status:** Accepted · **Date:** 2026-07-29

## Context

Running goose automatically at startup is convenient and common. The database here is a single SQLite file in one volume holding the only copy of years of financial history, and the container has `restart: unless-stopped`.

## Decision

Migrations run **explicitly**: `moneyapp migrate up`. The application does **not** migrate at boot. `/readyz` reports unhealthy while the schema version is behind, so an unmigrated container refuses traffic rather than serving against a schema it does not understand.

## Rationale

- **Crash-loop risk.** With auto-restart, a migration that fails partway can be retried repeatedly against a progressively stranger schema. SQLite has transactional DDL, but a multi-statement migration with a data backfill can still leave a half-state — and the restart loop makes it worse, quickly.
- **Backups need a moment.** The upgrade sequence takes a pre-migration backup. Auto-migration at boot leaves no such moment.
- Explicit migration makes upgrade a decision with a known before and after.
- `/readyz` failing closed is safer than an app writing against an unexpected schema.

## Consequences

- Upgrade is a documented four-step sequence rather than `docker compose pull && up -d`:

  ```bash
  docker compose pull
  docker compose down
  docker compose run --rm moneyapp migrate up   # backup taken automatically
  docker compose up -d
  ```

- **Auto-update tooling must not be pointed at this container.** Watchtower or Portainer auto-pull would restart into an unmigrated state and sit unready. Documented in [10](../10-deployment-ci.md) §10.8, and the reason deploys stay manual.
- Forgetting to migrate produces a clearly unready service rather than corruption — the failure is loud and harmless.
- CI runs `up`, `down`, `up` on a scratch database so both directions are exercised before release.

## Alternatives rejected

| Option | Why not |
| --- | --- |
| Auto-migrate at boot | Crash-loop risk against the only copy of the data; no backup window |
| Auto-migrate with an advisory lock | SQLite is single-writer; the lock solves a problem that does not exist here, and not the crash-loop one |
| Migrate in an init container | More moving parts than a documented command for a single-host deploy |
