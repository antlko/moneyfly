# 11 — Roadmap

Ordered so each phase is independently useful. Nothing depends on a later phase to be worth running.

> **Progress.** Phase 0 and phase 1 are complete, and the parts of phases 3 and 6 that
> stage 03 of the [implementation plan](implementation-plan/) covers: budgets with bulk
> seeding, the single-period budget report, quick entry, the budget and history
> screens, dark mode. Phase 2 (import) is the next piece of work and is where the
> 1,683-row gate lives. The metrics engine, capital, the FX scheduler, the bot and the
> PWA manifest remain untouched.
>
> The phases here are grouped by theme; the implementation plan slices the same work so
> that every stage ends deployable. Where the two disagree, follow the plan.

## Phase 0 — Skeleton

Repo along `golite` lines; config load and validation; `log/slog` with PII masking; goose wiring; SQLite via `modernc.org/sqlite`; Fiber with middleware; the `Money` type and `currency` seed; Vue app embedded via `embed.FS`; Dockerfile for both architectures; CI running lint, test and build.

**Done when** the container starts, serves an empty SPA, and `/healthz` and `/readyz` answer correctly.

## Phase 1 — Data foundation

Migrations 0001–0008. Repositories with mandatory `user_id` scoping and the review test that fails on an unscoped query. Auth: bootstrap admin, login, sessions, password change. Categories and accounts CRUD with aliases.

**Done when** an admin can log in and manage categories and accounts, and no repository method can reach a row it does not own.

## Phase 2 — Import

The whole point of the system.

Positional CSV parser — BOM, delimiter sniffing, `DD.MM.YYYY`, proper signed-decimal parsing with per-currency exponents. Alias resolution with fuzzy *suggestions only*. Natural-key plus occurrence dedup. Full-dataset reconcile with vanished-row flagging. Batch state machine, preview, commit, revert. The full `testdata/` corpus.

**Done when** the real 1,683-row export imports to exactly **1,683** rows, re-importing inserts **0**, and an unknown category blocks the batch. This is the gate that makes the 14% loss impossible.

## Phase 3 — Metrics and parity

Every [07](07-metrics-and-budgets.md) metric as a pure function. Budgets with bulk seeding. Reports endpoints. `legacy_blend` burn mode. Golden-file parity suite.

**Done when** the parity suite passes against the 12 recorded months, with the four corrections asserted as documented deviations. From here the spreadsheet is reproducible, which is the real cutover criterion.

## Phase 4 — Capital

Snapshots with previous-month pre-fill. Computed parent rollups. `is_liquid` and `counts_toward_net_worth`. Leaf-only allocation. Runway with switchable burn. Reconciliation drift. `delta_real` / `delta_fx` split.

**Done when** net worth, allocation and runway match the sheet, allocation sums to 100%, and net-worth change is split into real and currency components — the analysis the spreadsheet could not do.

## Phase 5 — FX and providers

`fx_rate` history. `open.er-api` primary, `fawazahmed0` fallback. The generalised `setting` mechanism with manual/auto per key. Daily scheduler with boot catch-up, jitter and plausibility checks. Contemporaneous vs constant valuation. Staleness surfacing.

**Done when** rates refresh unattended, a provider outage degrades to the last known value with a visible badge, and everything still works with providers disabled.

## Phase 6 — Frontend

Quick entry in ≤3 taps. Budget screen with the gradient pace chart. Capital screen. History with search and filter. Import flow including the blocking mapping screen. Settings. Category and account management. Dark mode. PWA manifest and shell-only service worker, with the iOS install hint.

**Done when** a month can be run entirely from a phone: import, review, record snapshots, log an ad-hoc expense.

## Phase 7 — Telegram

Linking with single-use codes. Document ingestion through the same pipeline as web upload. `/link`, `/unlink`, `/help` — and nothing else. Supervised worker with persisted offset and backoff. Off by default.

Small phase by design: the bot relays bytes and one reply. See [05](05-telegram-bot.md) §5.3 for what is deliberately excluded.

**Done when** forwarding an export from the phone imports it, an unmapped name replies with a link instead of committing, and killing the container mid-poll loses nothing.

## Phase 8 — Migration and cutover

`migrate-excel` for the workbook. Parity report as a user-visible page. `GET /export` and full restore. Backups with the rehearsed restore drill.

**Done when** history is loaded, the parity page is clean, and a restore has actually been performed once.

## Phase 9 — Polish

User-defined metrics with the restricted expression parser. Drag-and-drop dashboard widgets. Charts export. Accessibility audit against WCAG 2.1 AA. i18n scaffolding.

## Deferred, with reasons

| Item | Why deferred | Revisit when |
| --- | --- | --- |
| **Offline expense entry** | Client mutation queue, cross-device conflict resolution, duplicated money and FX logic on the client. Largest single client cost. | The PWA genuinely replaces Monefy for daily entry |
| **Encryption at rest** | Explicitly dropped | Data is hosted for people who ask for it |
| **Shared/household budgets** | No current need; trivial to add without encryption — a membership table | Someone actually shares |
| **Broker integration (Trading212)** | **Dropped, not deferred.** Would reintroduce a credential and an outbound dependency to avoid typing two numbers a month | Not planned |
| **Bot commands beyond ingestion** | The bot does one job. Queries and notifications belong in the app | Only if the app proves inconvenient |
| **Generic CSV mapping UI** | Only Monefy matters today | A second source appears |
| **TWA / Play Store** | PWA suffices on Android | Android becomes the primary device |
| **Native iOS** | App Store cost | Never, most likely |
| **Postgres** | SQLite is ample at this scale | Multi-tenant with real concurrency |
| **Prometheus** | Unjustified for one user | Others are hosted |
| **Two-way Sheets sync** | Conflict-prone, no benefit once the app is authoritative | Never |

## Immediate action, independent of all phases

**Rotate the Telegram bot token and revoke the Trading212 API key.** Both are literals in `main.go:18-21` of the existing parser and are in git history. The Trading212 key grants read access to real account and portfolio data — and since no broker integration is being built, revoking it outright is simpler than rotating. History rewriting is not a reliable fix for either.
