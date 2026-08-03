# Stage 03 — Transactions & Budgets

> **Kickoff prompt**
> Implement stage 03 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `03-transactions-and-budgets.md`, plus `docs/03-data-model.md` §3.4–3.5, `docs/08-ux.md` §8.3–8.4 and `docs/06-fx-and-providers.md` §6.3 for rate lookup. Stages 01–02 are complete. Build a usable expense tracker. Import is stage 04 — scope-out is binding.

## Goal

**The first stage that is genuinely useful.** Log expenses and income, set budgets, browse and search history, see plan versus actual. From here the app could replace a notes-app habit even before the Monefy import exists.

Also lands the FX foundation — a manual, dated rate table — because `base_amount_minor` is computed on write and every later stage depends on it.

## MVP demo

Log in → tap **+** → pick `Food` → type `12.50` → save. Three taps.

Then: see it in the current month's list → set a budget for `Food` of 300 → the budget screen shows `12.50 / 300`, 4%, green → add a `4000 HUF` expense on a HUF account → confirm it converts to EUR at the seeded rate and lands in the same total → log income → see `Diff` and `Saved %` → swipe to last month, see it empty (**blank, not zero**) → delete a transaction and watch the total drop.

## Scope

**In:** migrations 0004 (minimal `fx_rate`), 0005 (`transaction_entry`), 0006 (`budget`), the `Money`↔SQL boundary, transaction CRUD, transfers, natural-key computation, rate lookup and conversion on write, budget CRUD + bulk seed, budget report for a single period, quick-entry UI, transaction list with search and filter, month navigation.

**Out:** import batches and `import_row` (stage 04), the metrics engine and averages (stage 05), snapshots and net worth (stage 06), providers and the scheduler (stage 07), PWA (stage 09). `import_batch_id` is nullable and stays null here.

## Contracts published here

```go
// internal/domain/fx
type Rate struct { AsOf time.Time; Base, Quote string; Rate *big.Rat; Source string; ID int64 }
type Service interface {
    // RateOn: exact date, else nearest EARLIER, else (nil, nil).
    // Never a later rate — a past figure must not change because of a future rate.
    RateOn(ctx context.Context, base, quote string, on time.Time) (*Rate, error)
    // Convert returns the converted amount and the rate id used, for audit.
    Convert(ctx context.Context, m money.Money, to string, on time.Time) (money.Money, *int64, error)
}

// internal/domain/transaction
type Kind string // "expense" | "income" | "transfer_in" | "transfer_out"

type Transaction struct {
    ID, UserID, AccountID int64
    CategoryID  *int64        // nil iff kind is a transfer
    OccurredOn  time.Time     // date only
    Kind        Kind
    Amount      money.Money   // native, always non-negative
    BaseAmount  *money.Money  // nil when no rate was available
    FxRateID    *int64
    Description, Merchant *string
    NaturalKey  string
    Occurrence  int
    TransferGroupID, ImportBatchID *int64
}

// NaturalKey is THE dedup contract. Stage 04 depends on it byte-for-byte.
// SHA256 of the fields joined by "\x1f", in exactly this order:
//   occurredOn(YYYY-MM-DD) | accountSourceName | categorySourceName |
//   amountMinor(base-10) | currency | rawDescription
// rawDescription is UNTRIMMED — "Продукты " must not collide with "Продукты".
func NaturalKey(occurredOn time.Time, accountName, categoryName string,
                amountMinor int64, currency, rawDescription string) string

// internal/domain/budget
type Budget struct { CategoryID int64; Period period.Period; Planned money.Money }
```

`NaturalKey` is defined here rather than in stage 04 so that manually-entered and imported transactions share one identity rule. Changing it later invalidates stored keys and needs a recompute migration — so it is frozen now, and covered by a golden test.

## As built — decisions taken while implementing

Six things the stage file left open, resolved and recorded here so code and spec do
not drift:

1. **Migrations follow the §3.7 numbering, not the feature boundary.** 0004 creates
   `provider`, `fx_rate` and `setting`; 0005 adds `import_batch`, `import_row` and
   `idempotency_key` alongside `transaction_entry`; 0006 adds `balance_snapshot`
   alongside `budget`. Only `fx_rate`, `transaction_entry`, `budget` and
   `idempotency_key` are *used* here — the rest are empty tables, exactly as stage 01
   left `user` and `session`. The alternative, renumbering later, would break the
   forward-only rule for no gain. `transaction_entry.import_batch_id` needs
   `import_batch` to exist for its foreign key regardless.
2. **`idempotency_key` is a new table**, and an addition to the §3.5 DDL. Replaying
   an `Idempotency-Key` must return the original response, and the natural key
   cannot do that job: two identical coffees on one day are deliberately *not*
   unique. See [03-data-model.md](../03-data-model.md) §3.7.
3. **Occurrence uses `MAX(occurrence) + 1`, not `count(*) + 1`.** The unique index is
   partial (`WHERE deleted_at IS NULL`), so after a soft delete a count would
   re-issue a number that a live row still holds. Both are identical until something
   is deleted, and `MAX` stays correct afterwards.
4. **An amount's currency must equal its account's currency** (422 otherwise). The
   native amount is by definition the amount in the account's own currency; a euro
   cash account holding a forint expense is not a fact the model should accept.
5. **A cross-currency transfer is refused** (422), with the message pointing at two
   separate transactions. Which side is authoritative and at what rate is a decision
   the specification does not make, and inventing one would silently produce wrong
   figures.
6. **No income category is seeded.** The seed is exactly the 20 expense categories
   the stage-02 gate counts. Monefy exports no income at all
   ([04-import-monefy.md](../04-import-monefy.md) §4.8), so the income tab in quick
   entry starts empty and offers to create one — a normal `POST /categories` with
   `kind: income`.

Thresholds live in `config.Reports` (`warn_percent`, `over_multiplier`) until the
per-user `setting` table lands in stage 07.

## Tasks

**1. Migration 0004 — minimal `fx_rate`.** Full table per [03-data-model.md](../03-data-model.md) §3.5, including both indexes. Seed the workbook's rates as `source='workbook-seed'`, dated the first of the earliest month: `EUR→USD 1.14`, `EUR→HUF` from `0.0028` inverted (357.142857…), `EUR→UAH` from `0.02` inverted (50). **Store `EUR→X` only** ([adr/0009](../adr/0009-dated-fx-provider-chain.md)) — the workbook's `USD/EUR 0.88` is the inverse and is not stored.

> **As built:** the date is `2021-07-01`, the first of the month containing the
> earliest row in the real export (19.07.2021), so nearest-earlier lookup covers the
> whole history the importer will bring in. `EUR→HUF` is stored as
> `357.142857142857` — the reciprocal of `0.0028` is a repeating decimal, and twelve
> places are far more precision than the source figure carries.

**2. `internal/domain/fx`.** `RateOn` with exact → nearest-earlier → `(nil, nil)`. Cross rates via base: `USD→HUF = (EUR→HUF) ÷ (EUR→USD)`, computed in `big.Rat`, rounded **once** at the end. `Convert` returns the rate ID.

**3. Migration 0005 — `transaction_entry`.** Exactly per §3.5, including the `CHECK` tying transfers to a null category and `UNIQUE (user_id, natural_key, occurrence) WHERE deleted_at IS NULL`. **Verify by test that the constraints reject what they should** — see the constraint tests below.

**4. Money↔SQL boundary.** Store `amount_minor` + `currency`; read back into `money.Money` using the currency's exponent. One helper, used everywhere. No ad-hoc scaling at call sites.

**5. `NaturalKey`.** Exactly as specified. Golden test with a fixed expected hash so a future refactor cannot silently change it.

**6. Occurrence assignment.** On insert, `occurrence = 1 + count(existing rows with same user+natural_key)`. Must be computed **inside** the insert transaction to avoid a race; assert with a concurrent-insert test.

**7. Transaction service.** Create, get, list, update, soft-delete. On create: validate the category matches the kind, compute the natural key and occurrence, convert to base currency and record `fx_rate_id`. A **missing rate must not block the write** — store `base_amount = NULL` and surface it in the response.

**8. Transfers.** `POST /transfers` creates the mirrored pair in one SQL transaction with a shared `transfer_group_id`. Deleting one deletes both. Transfers never carry a category and never appear in category totals.

**9. Transaction endpoints.** Full set from [09-api.md](../09-api.md), cursor-paginated on `(occurred_on, id)`. `Idempotency-Key` honoured on create — replaying returns the original response.

**10. Migration 0006 — `budget`.** Per §3.5.

**11. Budget service.** Get by period, upsert one, `POST /budgets/bulk` to seed a range from one figure per category — the workbook has one annual figure per category, and nobody should type 216 values.

**12. Budget report — single period.** `GET /reports/budget?period=`. Per category: actual, planned, ratio, `state`. Implement the six-level precedence from [07-metrics-and-budgets.md](../07-metrics-and-budgets.md) §7.6 — `severely_over` → `over` → `approaching` → `within` → `zero` → `not_recorded`, with thresholds read from config defaults (10% warn, 2× over) since the settings table is stage 07. **Averages and multi-period aggregates are stage 05.**

**13. Quick entry UI.** Per [08-ux.md](../08-ux.md) §8.3: category grid ordered by recent frequency, custom numeric keypad (not the OS keyboard), date defaulting to today, account defaulting to last-used-for-currency. Optimistic save with an Undo toast; a failed save **restores the draft** rather than discarding it.

**14. Budget screen.** Hero month-to-date with planned beneath, `Diff`, `Saved %`, category rows with progress bars coloured by `state` — **with an icon and label, never colour alone**. Swipe between months. Unrecorded months render blank.

**15. History screen.** List with search over description and merchant, filters for category, account, kind and date range, infinite scroll on the cursor.

**16. `openapi.yaml`** updated; still validates.

## Tests

### Constraint tests (assert the DDL, not just the Go)

| Test | Asserts |
| --- | --- |
| `TestDDL_TransferRejectsCategory` | `transfer_out` + category → CHECK violation |
| `TestDDL_ExpenseRequiresCategory` | expense + null category → CHECK violation |
| `TestDDL_NaturalKeyUniquePerOccurrence` | same key + occurrence 1 twice → UNIQUE violation |
| `TestDDL_SameKeyOccurrence2Allowed` | **the two-coffees case: must succeed** |

### Behaviour

| Test | Asserts |
| --- | --- |
| `TestNaturalKey_Golden` | fixed input → fixed hash, frozen |
| `TestNaturalKey_UntrimmedDescription` | `"Продукты "` ≠ `"Продукты"` |
| `TestOccurrence_ConcurrentInsert` | two goroutines, same key → occurrences 1 and 2, no error |
| `TestRateOn_NearestEarlier` | gap → earlier rate; never a later one |
| `TestRateOn_NoRate_ReturnsNilNil` | before any rate → `(nil,nil)` |
| `TestCreate_NoRate_StoresNullBase` | write succeeds, `base_amount` null |
| `TestConvert_HUFToEUR` | 4000 HUF at seeded rate → expected EUR minor units |
| `TestConvert_CrossRate` | `USD→HUF` via EUR, rounded once |
| `TestTransfer_CreatesPairAtomically` | both rows or neither |
| `TestTransfer_ExcludedFromCategoryTotals` | transfer absent from budget report |
| `TestBudgetReport_States` | each of the six states, incl. **2× boundary is `severely_over`** |
| `TestBudgetReport_UnrecordedIsNull` | absent period → `null`, not `0` |
| `TestBudgetReport_RecordedZeroIsZero` | zero-spend → `0`, state `zero` |
| `TestBulkBudget_SeedsRange` | 12 periods × N categories written |
| `TestIdempotency_ReplayReturnsOriginal` | no duplicate created |
| `TestSoftDelete_ExcludedFromReports` | deleted row gone from totals, still in DB |
| `TestUserIsolation` | extended to every new endpoint |

`TestBudgetReport_UnrecordedIsNull` and `TestBudgetReport_RecordedZeroIsZero` are the pair that keeps the spreadsheet's `-1` sentinel bug from returning.

## Verification

```bash
make verify
docker compose up -d && docker compose run --rm moneyapp migrate up
# create an expense, then read the budget report
curl -s -b j -X POST localhost:8080/api/v1/transactions \
  -H 'content-type: application/json' -H 'Idempotency-Key: demo-1' \
  -d '{"account_id":1,"category_id":4,"occurred_on":"2026-07-15","kind":"expense",
       "amount":{"amount_minor":1250,"currency":"EUR","exponent":2}}' | jq '.id'
curl -s -b j 'localhost:8080/api/v1/reports/budget?period=2026-07' \
  | jq '.categories[]|select(.name=="Food")|{actual,planned,state}'
# replay the same Idempotency-Key -> same id, no new row
```

Expected: the HUF expense appears in the EUR total at the seeded rate; an untouched previous month reports `actual: null`, not `0`.

## Done checklist

- [ ] `make verify` green; stages 01–02 demos still work
- [ ] Expense logged in three taps on a phone viewport
- [ ] All four DDL constraint tests pass, including two-coffees at occurrence 2
- [ ] `NaturalKey` golden test frozen
- [ ] HUF converts correctly (exponent 0, seeded rate)
- [ ] Missing rate stores null base without failing the write
- [ ] Unrecorded month is `null`; recorded zero is `0`
- [ ] Budget states match the six-level precedence, 2× boundary included
- [ ] Transfers atomic and excluded from category totals
- [ ] Colour never the sole signal in the UI
- [ ] `openapi.yaml` updated and valid
