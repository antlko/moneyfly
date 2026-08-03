# ADR 0008 — Natural-key dedup with occurrence counter, and full-dataset reconcile

**Status:** Accepted · **Date:** 2026-07-29

## Context

Monefy exports have **no transaction ID**, **no time** (date only), and contain **all history every time**. Verified: the sample file spans 19.07.2021 → 03.12.2023 in a single export. So every import overlaps every previous one — overlap is the normal case, not an edge case.

The research report suggested hashing rows to detect duplicates. That is wrong here. Two genuinely distinct transactions can be byte-identical: two coffees on the same day, same price, same category, same empty description. A content hash discards the second — real data loss, the same class of bug as [0007](0007-blocking-alias-mapping.md).

## Decision

```
natural_key = SHA256( occurred_on | account_source_name | category_source_name
                      | amount_minor | currency | raw_description )
occurrence  = 1-based index within (natural_key) for that date
```

Enforced by `UNIQUE (user_id, natural_key, occurrence) WHERE deleted_at IS NULL`.

Import is a **full-dataset reconcile** over the file's date range: present in file only → insert; present in both → skip; present in database but absent from the file's range → **flag as vanished**, never auto-delete.

## Rationale

Behaviour across the cases that matter:

| Scenario | Result |
| --- | --- |
| Two identical coffees, first import | Both stored, occurrence 1 and 2 |
| Same file re-imported | 0 inserted, 2 duplicates |
| A third identical coffee added | 1 inserted, 2 duplicates |
| Transaction deleted in Monefy | Flagged as vanished, retained |

Neither content hashing nor append-only gets all four right. Uniqueness lives in the database, so idempotency is a guarantee rather than importer logic that could regress.

Reconcile is scoped to `[min(date), max(date)]` of the file, so a partial export can never imply everything outside its range was deleted.

## Consequences

- Editing a transaction's description or amount in the app changes its natural key. Locally-edited rows are therefore marked, and reconcile does not treat them as vanished when the unedited original is absent from a later export.
- Editing a transaction **in Monefy** looks like a delete plus an insert. Acceptable: the old row is flagged vanished, the new one inserted, and the user sees both.
- `raw_description` is part of the key, so trailing whitespace is significant. Raw text is preserved for the key and trimmed only for display — the observed `"Продукты "` would otherwise collide with `"Продукты"`.
- Vanished rows require a human decision. Deliberate: auto-deleting would make a parsing bug destructive against the only copy of years of history.
- Changing the key definition invalidates stored keys, so it needs a migration that recomputes them. The definition is therefore fixed and covered by tests.

## Alternatives rejected

| Option | Why not |
| --- | --- |
| Content hash alone | Discards genuine same-day duplicates |
| Append-only, user deletes duplicates | Every re-import duplicates the entire history |
| Replace the whole date range on import | Destroys transactions entered in the app rather than Monefy |
| Trust file order as identity | Order is not stable across exports |
