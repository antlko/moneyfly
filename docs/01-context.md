# 01 — Context, Goals and Current State

## 1.1 Why this project exists

Personal finances are currently tracked through a four-stage chain:

1. **Monefy** (iOS, paid) records individual transactions on the phone.
2. Once a month the Monefy CSV export is **shared to a Telegram bot** — chosen deliberately, because it avoids transferring a file to a computer first.
3. The bot (`monefy-budget-parser`, Go) parses the CSV, aggregates spend per category for one month, and **writes 18 numbers into a Google Sheet**.
4. A large, hand-maintained **Excel/Sheets workbook** (`[2025-2026] Budget_ Capital Grow`) turns those numbers into budgets, multi-currency net worth, savings rate, runway and charts.

The chain works but has hard limits:

- **The spreadsheet is the application.** All logic lives in cell formulas — fragile, unversioned, untestable, and only editable by its author.
- **Positional coupling.** The bot writes into fixed cells (`E9:P26`). Inserting or reordering a category silently corrupts every later import.
- **Silent data loss.** ~14% of transactions are discarded by a category-name mismatch (§1.3). Nobody was told.
- **Manual multi-currency.** FX rates are four numbers typed by hand, with no date, so history is silently revalued every time they change.
- **Manual net worth.** Roughly 30 balance figures are retyped each month, some as arithmetic in the formula bar (`=4000000+500000+35000+27000`).
- **Single user, single device.** No sharing, no phone-friendly entry, no way to add a transaction outside Monefy.
- **Vendor risk.** Monefy is a paid iOS app. On Android it is a recurring subscription. Losing access to it currently means losing the data-entry front end entirely.

## 1.2 Intended outcome

A self-hosted web application that **fully replaces the spreadsheet** — not a simplified version of it — deployable as a single container the same way `upmonitor` is, usable from a phone as an installed PWA, and able to keep the Telegram ingestion path that already works well.

The spreadsheet is the specification. Anything it computes today, the app must compute. See [appendix-excel-parity.md](appendix-excel-parity.md), which is the acceptance criteria.

## 1.3 Current-state teardown

Understanding the existing system matters because the new one must not inherit its defects. Everything below was verified by reading the code and a real 1,683-row export, not from description.

### The parser

`internal/parser/csv.go` maps a CSV into 18 category totals via `GetPreparedForExcelArray`, which appends values in a **hardcoded order** matching spreadsheet rows 9–26. `internal/sheet/service.go` then writes that slice into column `E`–`P`, rows `9`–`26`.

### The category-name defect

The parser looks up literal category keys. The real export uses different names, so these resolve to zero:

| CSV actually contains | Parser looks up | Rows silently dropped |
| --- | --- | --- |
| `Utilities` | *(no key exists)* | 119 |
| `HotelTrip` | `Hotel/Trip` | 58 |
| `Communication` | `Communications` | 25 |
| `Clouth` | `Clothes` | 19 |
| `Studing` | `Studying ` (trailing space) | 12 |
| `Sport` | `Sports` | 3 |
| `Taxi` | *(no key exists)* | 1 |

**≈237 of 1,683 rows — 14%.** The codebase already shows the scars: `getNotZero(c["Family"], c["Family "])` exists only to paper over a trailing space, and "Toiletry" is spelled three different ways across the CSV, the Go code, and the spreadsheet (`Toilery`).

This almost certainly explains two spreadsheet anomalies: `Communications` averages **0.45** across the year, and `Hobby` is **all zeros**.

### Other latent faults

| Fault | Location | Consequence |
| --- | --- | --- |
| `amount[1:]` strips the first character to remove `-` | `csv.go` | Works only because Monefy exports no income. A positive `450` would become `50`. |
| `time.Parse("1/2/2006", …)` expects `M/D/YYYY` slashes | `csv.go` | Real files are `DD.MM.YYYY` dots. Mismatch calls `log.Fatal`. |
| One month per invocation, month from `os.Args` | `main.go` | A 29-month export requires 29 runs. |
| Rates fetched once at boot from `D2:E6` | `bot/handler.go` | Editing a rate needs a process restart. |
| Any chat may upload | `bot/handler.go` | Anyone who finds the bot can write into the spreadsheet. |
| Live secrets as source constants | `main.go:18-21` | Telegram token and Trading212 key are in git history. **Rotate the token; revoke the key.** |
| `trading212` package unused | `internal/trading212/` | Never wired in. **Dropped entirely** — see [06](06-fx-and-providers.md) §6.7. |

### Spreadsheet defects to fix, not replicate

| # | Defect | Evidence |
| --- | --- | --- |
| 1 | `-1` "not recorded" sentinel is excluded by `AVERAGEIF` but **not** by `SUM` | `T9` = 8123, true total 8124. `T19` (Hobby) = **−1**. |
| 2 | Row 27 uses two different formulas | `E27 =SUM(E9:E26)` vs `G27 =SUMIF(G9:G26,"<>-1")`. |
| 3 | Sub-row percentages divide **unconverted** foreign amounts by EUR net worth | `C40` shows `Cash HUF` as **41.4%**; 11,000 HUF is really ≈1.2%. |
| 4 | Allocation doughnut plots parents **and** children | `A36:A50 / C36:C50` includes `Cash` *and* `Cash USD/EUR/HUF`; slices total ≈**162%**. |
| 5 | Several `AVG` cells are stale hardcoded values, not formulas | `C28` = 2094.948491; `C10`, `C37`–`C41` likewise. |
| 6 | `Num of Months` denominator uses absolute refs | Every historical month is recomputed with *today's* burn rate. |
| 7 | Named `LAMBDA SAVED_PERCENT` is hardwired to August and takes no arguments | `LAMBDA(((100-(100*Budget!$E$27/Budget!$E$31))/100))` |
| 8 | Row 59 conditional format compares a number to the text `"General"` | Rule `B55>A55` applied to `B59`. |

## 1.4 Goals

**G1 — Parity.** Reproduce every figure the spreadsheet produces, verified by golden-file tests over the existing 12 months.

**G2 — No silent loss.** Every imported row is either stored or explicitly reported. An unrecognised category stops the batch; it never becomes a zero.

**G3 — Flexibility without code changes.** Categories, budgets, thresholds, currencies, metrics and dashboard layout are data, not schema or formulas.

**G4 — Self-updating where possible.** Anything derivable from a free, keyless API — FX rates, gold, crypto — refreshes on a schedule instead of being retyped. No credentialed third-party integrations.

**G5 — Phone-first entry.** Installable PWA, expense logged in ≤3 taps, so the app can stand in for Monefy if access is lost.

**G6 — Keep Telegram.** Forwarding the export from the phone stays a first-class ingestion path.

**G7 — Deploy like `upmonitor`.** One container, one config file, one volume, `ghcr.io`, amd64 + arm64.

**G8 — Multi-user ready.** Every row scoped to a user from day one, so hosting a friend later is configuration rather than migration.

**G9 — No lock-in.** Full export of everything, at any time.

## 1.5 Non-goals

| Not doing | Why |
| --- | --- |
| **Encryption at rest** | Explicitly dropped by the owner. Removes the KDF, key rotation, envelope-encryption and passphrase-recovery problem entirely, and lets aggregation happen in SQL. See [adr/0002](adr/0002-no-encryption.md). |
| **Offline expense entry** | The largest single cost on the client. Nothing in the current workflow needs it. App shell is cached; data needs a connection. Named deferral, revisit if the PWA genuinely replaces Monefy. See [adr/0012](adr/0012-defer-offline-entry.md). |
| **Native iOS app** | App Store cost. PWA via Add to Home Screen instead. |
| **React Native / Expo** | Not on the roadmap; would force React over Vue and diverge from `golite`/`upmonitor`. |
| **Two-way Google Sheets sync** | Conflict-prone. A one-time historical import is offered instead. |
| **Bank / Open Banking integration** | Out of scope. Monefy plus manual entry is the input. |
| **Multi-currency consolidated tax reporting** | Not a requirement. |

## 1.6 Glossary

| Term | Meaning |
| --- | --- |
| **Base currency** | Currency all reporting is expressed in. **EUR**, configurable. |
| **Native amount** | Amount in the currency the transaction actually occurred in. Authoritative. |
| **Converted amount** | Amount restated in base currency, computed by us at a known rate — never trusted from Monefy's export. |
| **Account** | A place value sits: a bank account, a cash stash, gold, an investment holding. Has an asset class and a currency. |
| **Asset class** | `cash`, `bank`, `deposit`, `investment`, `metal`, `crypto`, `other`. Drives grouping. |
| **Liquid** | Convertible to spendable money quickly. Sum of liquid accounts = spreadsheet's `Ready for usage`. |
| **Snapshot** | A manually recorded balance for one account at one month-end. Source of truth for net worth. |
| **Expected balance** | Balance implied by summing transactions. Compared against the snapshot to surface drift. |
| **Drift** | `snapshot − expected`. Non-zero means transactions are incomplete. Informational, never auto-corrected. |
| **Planned** | Budgeted amount for a category in a period. Spreadsheet column `B`. |
| **Possible Minimum** | Sum of `Planned` across categories flagged essential. Spreadsheet row 28. |
| **Runway** | Liquid assets ÷ monthly burn rate, in months. Spreadsheet row 57, `Num of Months`. |
| **Saved %** | `1 − (spend ÷ income)` for a period. Spreadsheet row 33. |
| **Import batch** | One upload, tracked as a unit so it can be previewed, reported on, and reversed. |
| **Alias** | A source-file category or account name mapped to a canonical one. The fix for §1.3. |
| **Provider** | An external source for a value — FX rate, metal price, crypto price. |
| **Sentinel** | The spreadsheet's `-1` "not recorded yet" marker. Replaced by `NULL`. |

## 1.7 Users

| Role | Who | Needs |
| --- | --- | --- |
| **Owner / admin** | Repo author | Everything. Also administers the server. |
| **Invited user** | Possibly a partner or friends, later | Own isolated data on someone else's server. |
| **Read-only user** | Possibly a partner | View dashboards without editing. |

Registration is **invite-only**; there is no open sign-up. See [adr/0010](adr/0010-invite-only-auth.md).

## 1.8 Constraints

- Single small VPS, alongside other containers, behind a reverse proxy.
- No CGO — must cross-compile to a static binary for amd64 and arm64.
- Free API tiers only; no paid data feeds.
- Outbound calls must be non-blocking and optional; the app works fully with providers disabled.
- Author is a Go backend engineer — idioms follow `antlko/golite`.

## 1.9 Open items

Two facts could not be derived and are deliberately not invented:

- **`TODO(anatol)`** — the quantity of gold behind `Gold = 3000` EUR, needed to switch to live `XAU` pricing. See [06-fx-and-providers.md](06-fx-and-providers.md).
- **`TODO(anatol)`** — what the inline income addends were (`=3186+183+200+86`), needed to seed income line-item types. See [07-metrics-and-budgets.md](07-metrics-and-budgets.md).
