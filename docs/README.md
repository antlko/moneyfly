# MoneyApp — Project Documentation

Self-hosted budget and net-worth tracker. Replaces a *Monefy → Telegram bot → Google Sheets → hand-maintained Excel* pipeline with one deployable application, plus an installable PWA, keeping the Telegram ingestion path that already works.

**Status:** stages 01–03 of the [implementation plan](implementation-plan/) are built.
A usable expense tracker: log in, the 20 seeded categories and the chart of accounts,
three-tap entry, budgets, plan-versus-actual, dated FX. Import (stage 04), the metrics
engine (05), capital (06), FX automation (07), the Telegram bot (08) and the PWA (09)
are not started.

Where the code had to decide something the specification left open, the decision is
recorded in the relevant doc under an **"as built"** note rather than left to be
discovered — see [03-data-model.md](03-data-model.md) §3.7,
[07-metrics-and-budgets.md](07-metrics-and-budgets.md) §7.6 and
[implementation-plan/03](implementation-plan/03-transactions-and-budgets.md).

## Reading order

| Doc | Contents |
| --- | --- |
| [01 — Context](01-context.md) | Why, goals, non-goals, glossary, and a teardown of the current system including the defects being fixed |
| [02 — Architecture](02-architecture.md) | C4 context and container diagrams, internal structure, stack, deployment topology |
| [03 — Data Model](03-data-model.md) | ER diagrams, full SQLite DDL, migration and backfill plan |
| [04 — Monefy Import](04-import-monefy.md) | Verified CSV format, alias resolution, dedup, reconcile, batch state machine |
| [05 — Telegram Bot](05-telegram-bot.md) | Ingestion, linking, and what is deliberately excluded — optional, four commands |
| [06 — FX and Providers](06-fx-and-providers.md) | Rate history, provider evaluation, manual/auto settings, valuation modes |
| [07 — Metrics and Budgets](07-metrics-and-budgets.md) | Every spreadsheet formula restated unambiguously |
| [08 — UX and PWA](08-ux.md) | Screens, quick entry, install and offline behaviour, accessibility |
| [09 — API](09-api.md) | REST surface, conventions, validation — see [openapi.yaml](openapi.yaml) |
| [10 — Deployment and CI](10-deployment-ci.md) | Docker, config, migrations, backups, pipelines, cutover |
| [11 — Roadmap](11-roadmap.md) | Phased delivery with completion criteria |
| [Appendix — Excel Parity](appendix-excel-parity.md) | **The acceptance criteria.** Every cell mapped, every deviation justified |

Decisions live in [adr/](adr/), one file each.

## Decisions

| ADR | Decision |
| --- | --- |
| [0001](adr/0001-single-binary-sqlite.md) | Single static binary, embedded Vue UI, pure-Go SQLite |
| [0002](adr/0002-no-encryption.md) | **No encryption at rest** |
| [0003](adr/0003-snapshot-reconcile-balances.md) | Balances are snapshots, reconciled against transactions |
| [0004](adr/0004-integer-money.md) | Money as integer minor units |
| [0005](adr/0005-pwa-vue-no-react-native.md) | PWA only, Vue 3, no React Native |
| [0006](adr/0006-sql-not-orm.md) | Hand-written SQL, no ORM |
| [0007](adr/0007-blocking-alias-mapping.md) | Unrecognised names block the import |
| [0008](adr/0008-natural-key-dedup.md) | Natural-key dedup with occurrence counter |
| [0009](adr/0009-dated-fx-provider-chain.md) | Dated FX history with a provider chain |
| [0010](adr/0010-invite-only-auth.md) | Invite-only registration, server-side sessions |
| [0011](adr/0011-explicit-migrations.md) | Migrations run explicitly, not at boot |
| [0012](adr/0012-defer-offline-entry.md) | Offline entry deferred |
| [0013](adr/0013-telegram-in-process.md) | Telegram bot: minimal, in-process, opt-in |

## What the investigation found

The design is grounded in three real artefacts — the workbook, a 1,683-row Monefy export, and the existing parser — rather than in description. Four findings shaped it:

**The current pipeline silently loses ~14% of transactions.** The parser looks up 18 hardcoded category names; the export uses different ones. `Utilities` (119 rows), `HotelTrip` (58), `Communication` (25), `Clouth` (19), `Studing` (12), `Sport` (3) and `Taxi` (1) all resolve to zero with no warning. This is very likely why `Communications` averages **0.45** and `Hobby` is **all zeros** in the spreadsheet. Hence [adr/0007](adr/0007-blocking-alias-mapping.md), and a CI gate that fails if the export does not yield exactly 1,683 rows.

**The spreadsheet has eight further defects**, each documented with evidence in [01](01-context.md) §1.3 and mapped to a correction in the [parity appendix](appendix-excel-parity.md). Two worth naming: `Cash HUF` is shown as **41.4% of net worth** because raw HUF is divided by EUR net worth — truly ≈1.2%; and the allocation doughnut plots parents *and* their children, so its slices total **≈162%**.

**Encrypting amounts and computing reports server-side are mutually exclusive.** The earlier research report specified both. Encryption was dropped by decision, which resolved the contradiction and several others with it — see [adr/0002](adr/0002-no-encryption.md).

**Undated FX rates rewrite history.** Because the sheet's four rates carry no date, every past balance is revalued whenever one changes, so real saving and currency movement are indistinguishable. Dated rates make the split possible — the single largest analytical gain available.

## Provider evaluation

Tested live, 2026-07-29:

| Provider | Key | UAH | Verdict |
| --- | --- | --- | --- |
| `open.er-api.com` | none | ✅ | **Primary** — 161 currencies |
| `fawazahmed0/currency-api` | none | ✅ | **Fallback** — 338 tickers, plus `XAU` and `USDT` |
| Frankfurter / ECB | none | ❌ | Rejected — ECB covers 30 currencies, UAH not among them |
| DuckDuckGo Instant Answer | none | ❌ | Rejected — returns `production_state: offline`, all answer fields empty |

## Open items

Two facts could not be derived and were deliberately not invented:

- **`TODO(anatol)`** — the gold quantity behind `Gold = 3000` EUR, needed to price it from `XAU`. See [06](06-fx-and-providers.md) §6.9.
- **`TODO(anatol)`** — what the inline income addends were (`=3186+183+200+86`), needed to type income line items. See [07](07-metrics-and-budgets.md) §7.3.

## Action required now

**Rotate the Telegram bot token and revoke the Trading212 API key.** Both are literal constants in `monefy-budget-parser/main.go:18-21` under a `// TODO: HIDE IT!!!!!!` comment, and are in git history. The Trading212 key grants read access to real account and portfolio data; since no broker integration is being built, revoke it rather than rotate. History rewriting is not a reliable fix.

## Operating it

[cutover.md](cutover.md) is the runbook: deploy, load the workbook's history,
import the export, check the parity page, connect Telegram, turn on rates, and the
backup and restore drill. It ends with the credential rotation the old bot needs.
