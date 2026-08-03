# ADR 0007 — Unrecognised names block the import

**Status:** Accepted · **Date:** 2026-07-29

## Context

The existing parser looks up 18 hardcoded category keys. The real export uses different names, so **≈237 of 1,683 rows — 14%** — resolve to zero with no warning:

| CSV has | Parser wants | Lost |
| --- | --- | --- |
| `Utilities` | *nothing* | 119 |
| `HotelTrip` | `Hotel/Trip` | 58 |
| `Communication` | `Communications` | 25 |
| `Clouth` | `Clothes` | 19 |
| `Studing` | `Studying ` | 12 |
| `Sport` | `Sports` | 3 |
| `Taxi` | *nothing* | 1 |

This is almost certainly why `Communications` averages **0.45** and `Hobby` is **all zeros** in the spreadsheet. The code already carries workarounds — `getNotZero(c["Family"], c["Family "])` exists solely to paper over a trailing space — and "Toiletry" is spelled three ways across the CSV, the Go code and the sheet.

The failure mode matters more than the count: **it was silent**. The numbers looked plausible and were wrong for years.

Compounding it, Monefy exports in the **device locale** — older files use `Наличные` and `Счета` — and accounts and categories were renamed mid-history.

## Decision

A persistent **alias table** maps every source name to a canonical category or account. An unrecognised name puts the batch into `needs_mapping` and **commits nothing** until it is resolved. Fuzzy matching only ever *proposes*; it never applies.

`POST /imports/{id}/commit` returns `409` when `rows_unmapped > 0`.

## Rationale

- The disease is silence, so the cure must be noisy. A blocking prompt cannot be missed.
- Auto-creating a category is what let `Utilities` disappear into a category with no spreadsheet row. Auto-coercing is what let `Communication` become zero.
- An alias table handles locale changes and renames as data, without code changes.
- The decision is made once per name, then never asked again.

## Consequences

- The first import of a new export may need a few decisions. Mitigated by seeding every alias observed in the real data, so that import needs none.
- A blocked batch is a visible pending state. Correct: nothing is stored until it is right.
- Fuzzy suggestions could propose wrongly, which is why they are pre-selected but require confirmation. `Clouth` → `Clothes` is edit distance 2 and suggested; the user confirms.
- Canonical names are corrected (`Appliances`, `Toiletry`) with misspellings retained as aliases, so both the CSV and the old spreadsheet resolve.
- `Utilities` and `Taxi` become **real categories** — they never had a spreadsheet row, which is why 120 rows had nowhere to go. Folding them into `Bills` and `Transport` is a one-line alias change.

## Verification

CI asserts the real 1,683-row export yields **exactly 1,683** stored rows. A run producing 1,446 fails the build. This is the single most important test in the repository.
