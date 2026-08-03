# ADR 0006 — Hand-written SQL over an ORM

**Status:** Accepted · **Date:** 2026-07-29

## Context

The workload is unusual for a CRUD app: most queries are **aggregates over periods** — sum by category by month, average excluding absent periods, sum where liquid, allocation as a share of net worth, net worth over time. The spreadsheet being replaced is essentially one large aggregate expression.

`golite` uses `database/sql` with goose. Options considered: an ORM (GORM, ent), a query builder, `sqlc` codegen, or plain `database/sql`.

## Decision

**`database/sql` with hand-written SQL**, organised in `internal/store`, one file per aggregate. No ORM, no query builder.

## Rationale

- The hard queries are aggregates with `GROUP BY`, conditional sums, window functions over periods, and `LEFT JOIN`s that must preserve "no data" as `NULL` rather than collapsing it to `0`. ORMs obstruct exactly this and their escape hatch is raw SQL anyway.
- The distinction between **absent** and **zero** is central ([03](../03-data-model.md) §3.1). Getting it right requires seeing the join and the `COALESCE` — or its deliberate absence.
- Matches `golite`, so the codebase stays familiar.
- No hidden N+1 queries, no surprise lazy loads, no reflection cost.

## Consequences

- More code for simple CRUD. Accepted; it is mechanical and reviewable.
- SQL is SQLite-flavoured. Postgres would require rewriting the aggregate queries — acceptable, since Postgres is not planned ([0001](0001-single-binary-sqlite.md)).
- Column and struct mapping is manual, so schema changes must be followed through by hand. Caught by tests rather than the compiler.
- **`sqlc` was the close call.** It gives compile-time-checked SQL and generated types with no runtime reflection, and would fit well. Rejected only to keep the toolchain identical to `golite` — one fewer code-generation step to explain and keep current. Worth revisiting if manual mapping becomes a source of bugs.

## Rules

- Every query in `internal/store` filters on `user_id`, always as the first predicate. A test scans the package and fails on any statement touching a user-scoped table without it.
- No SQL outside `internal/store`. Domain code receives typed results.
- Every aggregate has a test asserting behaviour on **absent** periods, not just populated ones.
- No string concatenation of user input. Parameters only.
