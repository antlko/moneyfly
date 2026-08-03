# ADR 0002 — No encryption at rest

**Status:** Accepted · **Date:** 2026-07-29 · **Supersedes** the original brief

## Context

The original brief asked that all data be "ready to be encrypted", with per-user keys, so friends could later store data on the same server — while noting it should not be implemented yet. The research report went further and specified encrypting **every** sensitive column including `amount`, calling the database a "zero-knowledge store of ciphertext".

That specification is self-defeating. The same report promises server-computed category breakdowns, averages, balance-over-time series and budget rollups. **`SUM`, `AVG` and `GROUP BY` cannot operate on ciphertext.** It also proposes deriving the key from the user's password and holding it in memory for the session — while the Telegram bot ingests files when no session exists and no key is available.

Asked to choose, the owner's answer was explicit: *"don't do encryption at all — I planned to encrypt everything, but now don't need."*

## Decision

**No encryption at rest.** No field-level encryption, no SQLCipher, no per-user keys, no KDF, no envelope encryption. Report §3 and §7 are void.

Retained: `user_id` scoping on every table, bcrypt for password *hashing*, TLS in transit.

## Rationale

- Aggregation happens in SQL, where it belongs. The metrics engine is straightforward and fast.
- The Telegram bot ingests without a user session — the contradiction disappears rather than being worked around.
- No passphrase means no catastrophic data loss on a forgotten passphrase, and no recovery mechanism that would have undermined the guarantee anyway.
- Pure-Go SQLite stays viable, preserving [0001](0001-single-binary-sqlite.md).
- Encrypted search would have needed blind indexes; encrypted amounts would have needed client-side aggregation and a much heavier frontend. Both avoided.

## Consequences

- **The database file is the sensitive artefact.** It belongs on a volume not shared with any public-facing service, with host-level disk encryption as the appropriate layer.
- Backups are plaintext. They should not be copied to untrusted third-party storage.
- Raw uploaded CSVs are stored plaintext under `/config/uploads`.
- Anyone with host or volume access can read everything. Accepted: this is a personally administered server.
- Multi-user isolation is **logical**, not cryptographic — enforced by `user_id` scoping and reviewed by a test that fails on any unscoped query in `internal/store`.
- Hosting data for others later would need this revisited as a real decision, not a retrofit.

## Why `user_id` scoping was kept anyway

It is not encryption; it is tenancy. It prevents one user's query returning another's rows, and adding it later would touch every table, every query and every handler. The cost now is one column and one argument per repository method.
