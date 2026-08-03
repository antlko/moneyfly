# Stage 10 — Migration & Cutover

> **Kickoff prompt**
> Implement stage 10 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `10-migration-and-cutover.md`, then `docs/10-deployment-ci.md` §10.6 and §10.12, and `docs/03-data-model.md` §3.7. Stages 01–07 are required. Load the historical workbook, make backups real, and retire the spreadsheet.

## Goal

Get every historical figure in, prove it matches, make the data recoverable, and stop using the workbook.

## MVP demo

```bash
docker compose run --rm moneyapp migrate-excel --file "[2025-2026] Budget_ Capital Grow.xlsx" --dry-run
docker compose run --rm moneyapp migrate-excel --file "..." --commit
```

Then open the **parity page** in the app: every workbook metric beside the app's, all matching, with the ten deviations listed and explained. Then trigger a backup, restore it into a fresh container, and confirm the data is intact.

## Scope

**In:** `migrate-excel`, the parity report page, `GET /export` and full restore, nightly backups with integrity checks and the rehearsed restore drill, the cutover checklist.

**Out:** two-way Google Sheets sync (never — [01-context.md](../01-context.md) §1.5). Any new domain feature.

## Tasks

### 1. `migrate-excel`

Reads the workbook through the extract in `testdata/parity/`, produced from the
`.xlsx` by `generate.py` — **not** the `.xlsx` directly. Two reasons: it keeps an
XLSX parser out of a binary that would use it exactly once, and it means the file
the migration loads is the same file the parity suite asserts against, so the two
cannot disagree. The workbook itself is not committed; it holds real finances. `--dry-run` prints what it would do and writes nothing; `--commit` applies inside one transaction.

Loads, per [03-data-model.md](../03-data-model.md) §3.7:

- **Budgets** from column `B` — one `budget` row per category per month across the fiscal year.
- **Capital snapshots** from rows 36–69 — one `balance_snapshot` per account per month.
- **FX settings** from `E3:E5`, seeded as `fx_rate` with `source='excel-import'` so historical valuations stay reproducible. Store `EUR→X` only; `E2` (`EUR/USD`) is referenced by no formula and is not imported, and `G2` (`Month`) is dead.
- **Opening balance** — `D55 = 30723` as the snapshot for the period before the first recorded month, so `Diff in real capital` has its seed and the first period does not report a phantom change.
- **Expense history** — the workbook holds only monthly *aggregates*. Insert one aggregate transaction per category per month, `origin='cli'`, clearly marked.

Rules:

- **`-1` becomes absent.** Do not create a row. This is the sentinel that corrupted the workbook's totals; it must not enter the database.
- **The Monefy CSV is authoritative for transaction detail.** Where stage 04's imported months overlap the workbook, **skip the workbook aggregate** — otherwise every overlapping month doubles. Report the skipped range explicitly.
- Floats round half-up at the currency exponent, with the original value kept in the row's note so any discrepancy is traceable.
- Re-running is idempotent.

### 2. Parity report page

The [appendix](../appendix-excel-parity.md) as a live page: workbook value, app value, match or deviation, per metric per period. The ten deviations D1–D10 listed with their explanation.

This is the **cutover criterion made visible** — the workbook is retired when this page is clean, not when someone feels ready.

### 3. Export and restore

`GET /api/v1/export/transactions.csv` streams the history, honouring the same
filter the screen uses. A **full** JSON dump with a matching `POST /import/full`
restore is **not built**: the gzipped database backup already round-trips
everything losslessly and is exercised on every build by `TestBackup_Restorable`,
which is a stronger guarantee than a hand-written serialiser of thirteen tables
that nobody restores from. The CSV covers the lock-in answer
([01-context.md](../01-context.md) §1.4 G9) — the data can leave in a format
anything can read.

Revisit if the database file itself ever stops being portable.

### 4. Backups

Per [10-deployment-ci.md](../10-deployment-ci.md) §10.6:

- Nightly via SQLite's **online backup API** — consistent without stopping writes
- gzipped to `/config/backups/`, 14 retained, oldest pruned
- **integrity-checked after write**; a failed check keeps the previous backup and alerts
- one taken automatically **before every `migrate up`**

### 5. Restore drill

Documented in [cutover.md](../cutover.md) §7 **and performed on every build** by
`TestBackup_Restorable`: it takes a real backup, gunzips it into a fresh file, and
checks both the data and the schema version. An untested backup is not a backup,
and a drill nobody re-runs is a document.

```bash
docker compose down
gunzip -c backups/moneyapp-<ts>.db.gz > moneyapp.db
docker compose up -d
docker compose run --rm moneyapp migrate status
```

### 6. Cutover checklist

1. Deploy; bootstrap admin; change the password.
2. `migrate up`.
3. `migrate-excel --dry-run`, review, then `--commit`.
4. Import the Monefy CSV through the normal pipeline; resolve any unmapped names.
5. **Parity page clean** except the ten documented deviations.
6. Link Telegram; send one export end to end.
7. Enable auto FX; confirm rates land and match the workbook's manual values.
8. Run both systems in parallel for one month.
9. Retire the workbook.
10. Stop the old bot, **rotate its Telegram token**, and **revoke the Trading212 key** — both are literals in the old `main.go:18-21` and are in git history.

## Tests

| Test | Asserts |
| --- | --- |
| `TestMigrateExcel_DryRunWritesNothing` | database unchanged |
| `TestMigrateExcel_Idempotent` | twice → same row counts |
| `TestMigrateExcel_SentinelBecomesAbsent` | `-1` creates no row |
| `TestMigrateExcel_SkipsMonthsCoveredByCSV` | **no double-counting on overlap** |
| `TestMigrateExcel_OpeningBalance` | `D55` seeds the prior period |
| `TestMigrateExcel_SeedsFxAsExcelImport` | `source='excel-import'`, `EUR→X` only |
| `TestMigrateExcel_SkipsDeadSettings` | `E2` and `G2` not imported |
| `TestMigrateExcel_RoundingTraceable` | original float in the note |
| `TestParityPage_ListsAllDeviations` | D1–D11 present with explanations |
| `TestBackup_Restorable` | the drill: back up → gunzip → open → data and schema intact |
| `TestExport_TransactionsCSV` | the history leaves as valid CSV |
| `TestBackup_IntegrityChecked` | corrupt backup detected, previous kept |
| `TestBackup_RetentionPrunes` | 14 kept |
| — | `migrate up` takes a backup first; verified by hand in the cutover run |

`TestMigrateExcel_SkipsMonthsCoveredByCSV` is the one that prevents a silent doubling of every overlapping month — the most likely way this stage could corrupt real data.

## Verification

```bash
make verify
make parity

docker compose run --rm moneyapp migrate-excel --file "[2025-2026] Budget_ Capital Grow.xlsx" --dry-run
docker compose run --rm moneyapp migrate-excel --file "..." --commit

curl -s -b j localhost:8080/api/v1/reports/parity | jq '{matched,deviations:(.deviations|length)}'
# {"matched":true,"deviations":10}

curl -s -b j 'localhost:8080/api/v1/export?format=json' > /tmp/export.json
jq 'keys' /tmp/export.json

docker compose run --rm moneyapp backup now
ls -la /config/backups/ | tail -3
# then perform the restore drill into a fresh container
```

## Done checklist

- [ ] `make verify` and `make parity` green; every earlier demo still works
- [ ] Workbook history loaded; `--dry-run` writes nothing; re-running is idempotent
- [ ] `-1` never becomes a row
- [ ] Overlapping months not double-counted
- [ ] Opening balance seeded from `D55`
- [ ] Parity page clean except D1–D11, each explained
- [ ] The database round-trips losslessly through a backup; history exports as CSV
- [ ] Backups run nightly, integrity-checked, pruned to 14
- [ ] A backup is taken before every migration
- [ ] **Restore drill actually performed**, not just documented
- [ ] Old bot stopped, Telegram token rotated, Trading212 key revoked
- [ ] Workbook retired
