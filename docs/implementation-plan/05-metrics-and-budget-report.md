# Stage 05 — Metrics & Budget Report

> **Kickoff prompt**
> Implement stage 05 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `05-metrics-and-budget-report.md`, then **all of `docs/07-metrics-and-budgets.md`** and `docs/appendix-excel-parity.md` §A.2–A.3, §A.8–A.10. Stages 01–04 are complete. Build the metrics engine and prove parity with the workbook's budget half. Capital is stage 06 — scope-out is binding.

## Goal

Turn the spreadsheet's expense-and-income formulas into tested code, and prove it by asserting against the workbook's own numbers.

Every metric is a **pure function over loaded data**, which is what makes parity testable at all. Four of the workbook's formulas are wrong; this stage implements the corrected version and asserts both values, so the deviation is documented rather than discovered later.

## MVP demo

Open the budget screen for a month with imported data → per-category actual, planned, **average**, ratio and state → switch months and watch averages hold steady while actuals change → open the new **year view**: 18 categories × 12 months, colour-coded, with `AVG`, `Results`, `Amount`, `Possible Minimum`, `Diff` and `Saved %` — the workbook's grid, in the browser.

Then run `make parity` and watch every workbook figure verified.

## Scope

**In:** the metrics engine (`spend`, `average`, `total`, `spend_total`, `possible_minimum`, `income`, `diff`, `saved_percent`), multi-period report endpoints, the year grid UI, the parity fixture and test suite, `exclude_from_average` on transactions.

**Out:** anything capital — snapshots, allocation, net worth, runway, `delta_real`/`delta_fx` (all stage 06). Settings-backed thresholds (stage 07 — keep config defaults). User-defined metrics (stage 09).

## Contracts published here

```go
// internal/domain/metrics
// Every function is pure given its Loader. No I/O, no clock, no globals.
type Loader interface {
    Spend(ctx, userID int64, p period.Period) (map[int64]money.Money, error) // by category
    RecordedPeriods(ctx, userID int64) ([]period.Period, error)
    Income(ctx, userID int64, p period.Period) (money.Money, error)
    Budgets(ctx, userID int64, p period.Period) (map[int64]money.Money, error)
    Categories(ctx, userID int64) ([]category.Category, error)
}

// nil return means NOT RECORDED. Never conflate with zero.
func Spend(d Data, categoryID int64, p period.Period) *money.Money
func Average(d Data, categoryID int64) *money.Money          // workbook col C
func Total(d Data, categoryID int64) *money.Money            // workbook col T, sentinel fixed
func SpendTotal(d Data, p period.Period) *money.Money        // row 27
func PossibleMinimum(d Data, p period.Period) money.Money    // row 28
func Diff(d Data, p period.Period) *money.Money              // row 32
func SavedPercent(d Data, p period.Period) *float64           // row 33, unclamped
func State(actual, planned *money.Money, th Thresholds) BudgetState
```

Two refinements the code forced, both recorded here rather than left to drift:

- `State` takes `planned` as a **pointer** and the thresholds as the `Thresholds`
  struct stage 03 already published. "No plan recorded" and "planned zero" are
  different facts, and the two knobs are always read together. There is one
  implementation of the precedence, in `internal/domain/budget`; `metrics.State`
  aliases it rather than copying it.
- `Loader` is `SpendByPeriod` / `RecordedPeriods` / `PlannedByPeriod`: one batched
  query per shape across the whole range, rather than one call per period. The
  guarantee is unchanged — no method may COALESCE an absent period into zero.

`Data` is a snapshot loaded once per request, so a report is one round of queries rather than N. Keeping metrics pure over it is what lets the parity suite run without a server.

## The formulas, restated

Full derivations in [07-metrics-and-budgets.md](../07-metrics-and-budgets.md). The operative rules:

| Metric | Rule | Workbook |
| --- | --- | --- |
| `Spend` | Σ expense in period, `nil` if period unrecorded | `E9:P26` |
| `Average` | mean over periods **with a recorded value**; recorded zeros included | `AVERAGEIF(D9:P9,"<>-1")` |
| `Total` | Σ over recorded periods — **excludes the sentinel** | `SUM(E9:P9)` ⚠️ D1 |
| `SpendTotal` | Σ across expense categories, **one definition** | rows 27 ⚠️ D2 |
| `PossibleMinimum` | Σ planned where `is_essential` | `B28`, computed ⚠️ D3 |
| `Diff` | `income − spend_total` | row 32 |
| `SavedPercent` | `1 − spend÷income`; `nil` when income is 0; **not clamped** | row 33 |

## Tasks

**1. `Data` loader.** One batched load per request. Repository methods return `nil` for an unrecorded period — never `COALESCE(...,0)`. This is the single most important detail in the stage.

**2. `Spend`.** Per category per period. A period is *recorded* if any transaction exists in it for that user.

**3. `Average`.** Mean over recorded periods. Verify against `C9`: `House` sums 8,124 over 11 recorded months → **738.5454545…**

**4. `Total`.** Σ over recorded periods. **Deviation D1:** the workbook's `SUM` includes the `-1` sentinel, so `T9` reads 8,123 against a true 8,124, and `T19` (`Hobby`, all zeros) reads **−1**. Assert the corrected value and record the workbook's in the fixture.

**5. `SpendTotal`.** **Deviation D2:** the workbook used `SUM` in `E27` and `SUMIF(...,"<>-1")` in `G27`. One definition. Verify `C27`: 42,091.7108 ÷ 11 → **3826.519164**.

**6. `PossibleMinimum`.** Σ planned where essential. **Deviation D3:** `C28` was the stale literal `2094.948491`; always compute. Verify `B28` = **1780**.

**7. `Income`, `Diff`, `SavedPercent`.** Verify `B33` = `1 − 2350÷2800` = **0.1607142857**, and **`J33` = −3.751067461 unclamped** — January spent 4.75× income, and that is the most informative month in the dataset. Clamping it would hide the point.

**7b. Income categories.** The seed had none: every one of stage 02's 20
categories is an expense, so there was nothing to file a salary against and
`Diff` and `Saved %` could not be computed at all for a real account. `Salary`
(the workbook's row 31, planned 2800) and `Other income` are seeded here, taking
the count to **22**. Migration-free — the seed is code.

**8. `exclude_from_average`.** Nullable boolean on `transaction_entry` (migration 0008). Excluded rows still count in `Spend` and `Total` but not in `Average`. Motivating case: `Appliances` January is 10,248.28 against a 150 plan, which makes the mean meaningless. The workbook had no way to express this.

**9. Report endpoints.** `GET /reports/summary?from=&to=` (per period: spend, income, diff, saved %), `GET /reports/categories?from=&to=` (totals, averages, per-period series). Extend `GET /reports/budget?period=` with `average` per category.

**10. Year grid UI.** 18 categories × 12 months plus the roll-up rows, colour-coded by `State`, month-labelled. Unrecorded cells blank — **never `0`, never `-1`**. Horizontally scrollable inside its own container; the page must not scroll sideways.

**11. Parity fixture.** `testdata/parity/workbook-2025-2026.json`, generated by
`testdata/parity/generate.py` from the workbook — every value from
[appendix-excel-parity.md](../appendix-excel-parity.md), each cell carrying
`expected` (corrected) and, where they differ, `workbook` plus `deviations`. The
workbook itself is **not** committed: it holds real finances, and every figure the
tests need is in the fixture.

The generator recomputes each expected value from the rounded cell inputs by the
formula in [07-metrics-and-budgets.md](../07-metrics-and-budgets.md); it never
copies a derived figure out of the sheet, which would make the test circular.
Rounding sub-cent cells to integer minor units is **deviation D11**, added to the
appendix in this stage.

**12. `make parity`.** A dedicated target so the gate can be run alone. Wired into CI.

## Tests

### Parity gate

| Test | Asserts |
| --- | --- |
| `TestParity_SpendGrid` | all 18 × 12 cells match `E9:P26` |
| `TestParity_Averages` | col C, incl. `House` = 738.5454545 |
| `TestParity_Totals_SentinelFixed` | corrected totals; `House` 8124 not 8123; `Hobby` 0 not −1 |
| `TestParity_SpendTotal` | row 27; `C27` = 3826.519164 |
| `TestParity_PossibleMinimum` | 1780 |
| `TestParity_SavedPercent` | `B33` = 0.1607142857; **`J33` = −3.751067461** |
| `TestParity_DeviationsDocumented` | every fixture entry with a `workbook` value has a `deviation` id in D1–D10 |

`TestParity_DeviationsDocumented` is the guard against quietly "fixing" a number without recording why it differs from the workbook.

### Behaviour

| Test | Asserts |
| --- | --- |
| `TestAverage_ExcludesUnrecorded` | `nil` months not counted |
| `TestAverage_IncludesRecordedZero` | zero months drag the mean down, as in the workbook |
| `TestAverage_HonoursExcludeFlag` | flagged outlier out of the mean, in the total |
| `TestAverage_AllUnrecorded_ReturnsNil` | no data → `nil`, not 0 |
| `TestSavedPercent_ZeroIncome_ReturnsNil` | `nil`, not 0, not `Inf` |
| `TestSavedPercent_NotClamped` | negative preserved |
| `TestTotal_SingleRecordedPeriod` | equals that period |
| `TestState_ExactlyPlanned` | `within`, not `over` |
| `TestState_ExactlyTwicePlanned` | `severely_over` |
| `TestState_NilActual` | `not_recorded` |
| `TestMetrics_PureNoIO` | metrics package imports neither `store` nor `net/http` |
| `TestReportSummary_MonthOrdering` | fiscal-year start honoured |

## Verification

```bash
make verify
make parity          # must be green — the gate

curl -s -b j 'localhost:8080/api/v1/reports/budget?period=2026-06' \
  | jq '.categories[]|select(.name=="Transport")|{actual,planned,average,ratio,state}'
curl -s -b j 'localhost:8080/api/v1/reports/summary?from=2025-08&to=2026-07' \
  | jq '.[]|{period,saved_percent}'
# the January-equivalent period reports a large negative saved_percent, unclamped
```

## Done checklist

- [ ] `make verify` and `make parity` both green; stages 01–04 demos still work
- [ ] Every budget-half workbook figure reproduced
- [ ] `House` average = 738.5454545, `Amount` average = 3826.519164 (to 1e-6; 3826.517273 exactly, D11), `Possible Minimum` = 1780
- [ ] Sentinel fix applied: `Hobby` total is 0, not −1
- [ ] `Saved %` unclamped; −3.75 preserved
- [ ] Zero income → `nil`
- [ ] Unrecorded ≠ zero, asserted in both directions
- [ ] `exclude_from_average` affects the mean only
- [ ] Metrics package has no I/O imports
- [ ] All deviations carry an id in D1–D10
- [ ] Year grid never renders `0` for an unrecorded cell
