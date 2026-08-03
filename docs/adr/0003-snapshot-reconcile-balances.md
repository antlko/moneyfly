# ADR 0003 — Balances are snapshots, reconciled against transactions

**Status:** Accepted · **Date:** 2026-07-29

## Context

Two incompatible models were on the table.

The research report asserts the system "automatically maintains a running balance per account" by summing transactions. **The spreadsheet does not work that way.** Rows 36–55 are hand-typed monthly figures with no relationship to the expense grid. Gold, CSGO skins and USDT have no transactions at all. Monefy exports no income and no transfers, so a transaction-derived balance would be wrong by construction — it would see only expenses.

## Decision

**Hybrid.** Monthly `balance_snapshot` rows are the source of truth for net worth. Transactions independently imply an *expected* balance. The difference — **drift** — is surfaced per account. Nothing is auto-corrected.

## Rationale

- Faithful to how the data is actually maintained, so migration is lossless and cutover needs no behaviour change.
- Assets with no transaction history (gold, skins, crypto) are first-class rather than special cases.
- Drift is genuinely useful: it is the signal that an import missed something, income was not recorded, or a transfer is invisible. The spreadsheet had no equivalent — a missed month was silent.
- Purely derived balances would demand logging every income, transfer, investment move and gold purchase — a large increase in data entry for someone who currently does one bulk import a month.

## Consequences

- Roughly 30 figures per month must still be recorded. Mitigated: the snapshot screen pre-fills last month's values, so it is confirm-or-adjust rather than retype.
- Two numbers can disagree. Resolved by rule: **the snapshot always wins**, drift is informational.
- Parent accounts (`Cash`, `Banks`) are computed sums, never stored — which fixes the spreadsheet's bug where `Banks FOP` was omitted from the `Banks` formula but included in `General`.
- A skipped month is visible as a gap, not silently interpolated.

## Alternatives rejected

| Option | Why not |
| --- | --- |
| Snapshots only | Keeps spending and net worth disconnected, as today. No drift detection. |
| Derived only | Wrong given no income and no transfers in the export; huge data-entry increase. |
| Derived with periodic "adjustment" transactions | Fabricates transactions that never happened; corrupts category analysis. |
