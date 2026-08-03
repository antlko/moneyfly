# Stage 04 — Monefy Import

> **Kickoff prompt**
> Implement stage 04 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `04-monefy-import.md`, then **all of `docs/04-import-monefy.md`** plus `docs/adr/0007-blocking-alias-mapping.md` and `docs/adr/0008-natural-key-dedup.md`. Stages 01–03 are complete. Copy the real export to `testdata/monefy-real-1683.csv` first. **The hard gate: that file must import to exactly 1,683 rows.**

## Goal

Get years of real history into the system **without losing a single row**. This is the stage the whole project exists for, and the one with the strictest acceptance test.

The existing pipeline silently discards ~237 of 1,683 transactions — 14% — because it looks up hardcoded category names that do not match what Monefy emits. Stage 02 seeded the aliases; this stage makes an unmapped name **block the batch** so the failure can never be silent again.

## MVP demo

Upload the real export in the browser → preview shows **1,683 new, 0 duplicate, 0 unmapped**, date range 2021-07-19 → 2023-12-03, **28 months touched** → commit → the budget screen for any month in range now shows real numbers → upload a **fresh export of the same history** → preview shows **0 new, 1,683 duplicate** → revert the first batch → all 1,683 rows vanish from reports → un-revert by re-importing.

Two corrections the real file forced, both now fixed in the spec docs: the export touches **28** distinct months, not 29 (2022-08 and 2022-09 are empty), and re-uploading the *byte-identical* file is short-circuited by its sha256 — it reports the original batch rather than producing a second preview. Use a fresh export, or any byte-different copy, to see the 0/1,683 duplicate case.

Then the negative case: delete the `HotelTrip` alias, upload again → batch enters `needs_mapping` with 58 rows against one unknown name, **commit is refused with 409**, nothing is stored.

## Scope

**In:** migration 0007 (`import_batch`, `import_row`, and `import_batch_id` on transactions), CSV parser, alias resolution with fuzzy *suggestions*, dedup and occurrence assignment, full-dataset reconcile with vanished detection, batch state machine, preview/mapping/commit/revert endpoints, raw-file retention, import UI, and the full `testdata/` corpus.

**Out:** Telegram ingestion (stage 08 — it reuses this pipeline unchanged), the metrics engine (stage 05), generic column-mapping for non-Monefy sources (deferred past v1).

## Contracts published here

```go
// internal/domain/importer
type Source interface {
    Key() string                                   // "monefy"
    Parse(r io.Reader) ([]RawRow, []ParseError, error)
}

type RawRow struct {
    LineNo      int
    Raw         string
    Date        time.Time
    AccountName string
    CategoryName string
    AmountMinor int64   // absolute value; sign moved to Kind
    Currency    string
    Kind        transaction.Kind
    Description string  // UNTRIMMED — feeds NaturalKey
}

type Service interface {
    // Receive stores the file, parses, resolves, dedups. Never commits.
    Receive(ctx, userID int64, origin Origin, filename string, r io.Reader) (*Batch, error)
    Preview(ctx, userID, batchID int64) (*Preview, error)
    ApplyMappings(ctx, userID, batchID int64, m Mappings) (*Preview, error)
    // Commit returns apperr.ErrConflict when RowsUnmapped > 0 or state != previewed.
    Commit(ctx, userID, batchID int64) (*Batch, error)
    Revert(ctx, userID, batchID int64) (*Batch, error)
}

type Origin string // "web" | "telegram" | "cli"
```

`Service` is the seam stage 08 plugs into. The bot must call `Receive` and relay the result — it gets **no import logic of its own**.

## The CSV, as verified

From the real file. Do not re-derive; this is measured, not assumed ([04-import-monefy.md](../04-import-monefy.md) §4.1).

```
date,account,category,amount,currency,converted amount,currency,description
19.07.2021,UAH,Utilities,-65,UAH,-65,UAH,Коммисия
03.12.2023,HUF,Communication,-1000,HUF,-100,UAH,
```

| Property | Reality | Implication |
| --- | --- | --- |
| BOM | UTF-8 `EF BB BF` present | strip, or the first header is `﻿date` |
| Header | **`currency` twice** | **address columns positionally** — a name map is impossible |
| Date | `DD.MM.YYYY`, dots | not `M/D/YYYY`; the old parser `log.Fatal`s here |
| Amount | always negative in this file | parse the **signed decimal properly** — never `amount[1:]` |
| Income | **zero rows** | positives must still parse correctly when they appear |
| Cols 4–5 | native amount + currency | **authoritative** |
| Cols 6–7 | converted, in Monefy's base at export time (UAH) | **discard** — not stable, not our base |
| Coverage | full history every export | overlap is the normal case |
| Locale | older files are Russian (`Наличные`, `Счета`) | resolve via aliases |

## Tasks

**1. Copy the fixture.** `testdata/monefy-real-1683.csv` from `~/Programming/anatolkozhukhar/monefy-budget-parser/data/monefy-2023-12-03_03-48-39.csv`. Assert in a test that it has 1,684 lines including the header.

**2. Migration — already applied.** `import_batch`, `import_row` and `transaction_entry.import_batch_id` were created by migration **0005**, because `transaction_entry` references `import_batch` and [03-data-model.md](../03-data-model.md) §3.7 groups them there. No new migration is needed; this task is done by stage 03.

**3. Monefy parser.** BOM strip, delimiter sniff (comma or semicolon), **positional** column access, `DD.MM.YYYY`, signed decimal into minor units **using the currency's exponent** (`-1000 HUF` → `1000`, not `100000`), sign → `Kind`, description preserved untrimmed. A malformed row becomes a `ParseError` with its line number; **it does not abort the file**.

**4. Alias resolution.** For each row, resolve account and category via stage 02's `ResolveAlias`. Unknown → row status `unmapped`. Fuzzy matching (case-insensitive, trim-insensitive, then Levenshtein ≤ 2) **only produces a suggestion** — it never auto-applies. `Clouth` → `Clothes` is distance 2 and is suggested, not assumed.

**5. Dedup.** Compute `NaturalKey` from stage 03 — same function, not a copy. Assign `occurrence` per §4.5. Classify each row `new` or `duplicate` against stored rows.

**6. Reconcile.** Over `[min(date), max(date)]` of the file only: stored rows in range absent from the file are **flagged `vanished`**, never auto-deleted. Scoping to the file's range is what stops a partial export implying everything else was deleted.

**7. Batch state machine.** `received → parsed → needs_mapping ⇄ parsed → previewed → committed → reverted`, plus `failed`. Illegal transitions return `ErrConflict`.

**8. Commit.** One SQL transaction: insert rows, set `import_batch_id`, update counts, set `committed_at`. **Refuse with `ErrConflict` when `rows_unmapped > 0`** — this is the guarantee, and it is enforced in the service, not the UI.

**9. Revert.** Soft-delete exactly the rows carrying that `import_batch_id`; set `reverted_at`.

**10. Raw retention.** Store under `/config/uploads/<user>/<sha256>`. On upload, if the sha256 was already committed, report it rather than reprocessing.

**11. Endpoints.** `POST /imports`, `GET /imports`, `GET /imports/{id}`, `GET /imports/{id}/rows?status=`, `POST /imports/{id}/mappings`, `POST /imports/{id}/commit`, `POST /imports/{id}/revert`. Cap upload size; return 413 above it.

**12. Import UI.** Per [08-ux.md](../08-ux.md) §8.6. The **mapping screen is the centrepiece**: one row per unknown name, row count, suggestion pre-selected but requiring confirmation, and no way to proceed past it. Preview shows counts, date range and months touched. Batch history with revert.

**13. Test corpus.** Every fixture in [04-import-monefy.md](../04-import-monefy.md) §4.14.

## Tests

### The gate

| Test | Asserts |
| --- | --- |
| `TestImport_RealExport_All1683Rows` | **exactly 1,683 stored. 1,446 is a failure.** |
| `TestImport_RealExport_NoUnmapped` | 0 unmapped with stage 02's seeded aliases |
| `TestImport_RealExport_CategoryTotals` | per-category counts match the file: Food 431, Eating out 363, Entertainment 275, Transport 121, **Utilities 119**, Gifts 106, **HotelTrip 58**, Toiletry 52, Health 30, House 28, **Communication 25**, Hobby 20, **Clouth 19**, **Studing 12**, Applience 12, Family 7, **Sport 3**, **Taxi 1**, Services 1 |

The bolded categories are the ones the old pipeline discarded. Asserting their counts individually is what makes the regression impossible to reintroduce quietly.

### Behaviour

| Test | Asserts |
| --- | --- |
| `TestParse_StripsBOM` | first header is `date` |
| `TestParse_DuplicateCurrencyHeader` | both currency columns read positionally |
| `TestParse_DottedDate` | `19.07.2021` parsed; `07/19/2021` rejected loudly |
| `TestParse_HUFZeroDecimal` | `-1000 HUF` → `1000` minor |
| `TestParse_PositiveAmount_Income` | `450` → income, minor `45000`, **not `50`** |
| `TestParse_MalformedRowDoesNotAbort` | one bad line → 1 rejected, rest parsed |
| `TestParse_UntrimmedDescription` | `"Продукты "` preserved for the key |
| `TestImport_Reimport_ZeroNew` | second run: 0 new, 1,683 duplicate |
| `TestImport_ThreeIdenticalRows` | occurrences 1, 2, 3 all stored |
| `TestImport_ThirdCoffeeAdded` | 1 new, 2 duplicate |
| `TestImport_UnknownCategory_BlocksBatch` | state `needs_mapping`, **0 rows stored** |
| `TestImport_CommitWithUnmapped_409` | `ErrConflict`, nothing written |
| `TestImport_FuzzySuggestsNeverApplies` | suggestion returned; no alias created |
| `TestImport_MalformedRow_RejectedNotFatal` | a rejected line is counted and explained, and does not block |
| `TestImport_ApplyMappings_ThenCommits` | mapping → previewed → committed |
| `TestImport_Revert_RemovesExactlyBatch` | other batches untouched |
| `TestImport_VanishedFlaggedNotDeleted` | row absent from range → flagged, still present |
| `TestImport_VanishedScopedToFileRange` | rows outside the range never flagged |
| `TestImport_RussianLocale` | `Наличные`/`Счета` resolve |
| `TestImport_SameSha256_ReportsAlreadyImported` | no reprocessing |
| `TestImport_AtomicCommit` | failure mid-commit → zero rows |
| `TestUserIsolation` | user A cannot see or commit user B's batch |

## Verification

```bash
make verify
docker compose up -d && docker compose run --rm moneyapp migrate up

BATCH=$(curl -s -b j -F file=@testdata/monefy-real-1683.csv \
  localhost:8080/api/v1/imports | jq -r '.id')
curl -s -b j localhost:8080/api/v1/imports/$BATCH \
  | jq '{rows_total,rows_new,rows_unmapped,status,months_touched}'
# {"rows_total":1683,"rows_new":1683,"rows_unmapped":0,"status":"previewed","months_touched":28}

curl -s -b j -X POST localhost:8080/api/v1/imports/$BATCH/commit | jq '.status'   # "committed"
curl -s -b j 'localhost:8080/api/v1/transactions?limit=1' | jq '.has_more'        # true

# idempotency: the identical file is short-circuited on its digest
curl -s -b j -F file=@testdata/monefy-real-1683.csv localhost:8080/api/v1/imports \
  | jq '{id,already_imported}'                      # the original batch, already_imported: true

# a fresh export of the same history is analysed in full
cp testdata/monefy-real-1683.csv /tmp/again.csv && echo >> /tmp/again.csv
B2=$(curl -s -b j -F file=@/tmp/again.csv localhost:8080/api/v1/imports | jq -r '.id')
curl -s -b j localhost:8080/api/v1/imports/$B2 | jq '{rows_new,rows_duplicate}'
# {"rows_new":0,"rows_duplicate":1683}
```

## Done checklist

- [ ] `make verify` green; stages 01–03 demos still work
- [ ] **1,683 rows imported from the real file — the gate**
- [ ] Per-category counts asserted, including all seven previously-lost categories
- [ ] Re-import inserts 0
- [ ] Three identical rows → three stored
- [ ] Unknown category blocks the batch; commit returns 409; nothing stored
- [ ] Fuzzy matching only ever suggests
- [ ] Vanished rows flagged, never deleted, scoped to the file's range
- [ ] Positive amounts parse correctly (guards the `amount[1:]` bug)
- [ ] Revert removes exactly one batch
- [ ] Mapping screen cannot be skipped in the UI
- [ ] `openapi.yaml` updated and valid
