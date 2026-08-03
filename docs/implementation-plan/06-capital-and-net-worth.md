# Stage 06 — Capital & Net Worth

> **Kickoff prompt**
> Implement stage 06 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `06-capital-and-net-worth.md`, then `docs/07-metrics-and-budgets.md` §7.5, `docs/appendix-excel-parity.md` §A.4–A.5, and `docs/adr/0003-snapshot-reconcile-balances.md`. Stages 01–05 are complete. **At the end of this stage the workbook is fully replaced.**

## Goal

The capital half: monthly snapshots, computed parent rollups, allocation, liquid assets, net worth, runway and reconciliation drift.

Two workbook bugs get fixed here, and one genuinely new capability lands: splitting net-worth change into **real saving** versus **currency movement**, which the workbook could never do because its rates carry no date.

## MVP demo

Open **Capital** for a month → net worth, month-on-month change **split into real and FX**, runway in months → allocation doughnut over leaf accounts, **summing to exactly 100%** → drill into `Cash` and see USD/EUR/HUF children → `Cash HUF` shows **≈0.116%**, not the workbook's 41.4%.

Then snapshot entry: every account listed with last month's figure pre-filled → adjust two → save. Roughly 30 workbook cells become a confirm-and-adjust screen.

Then reconciliation: a drift badge where transactions imply a different balance — informational, never auto-corrected.

## Scope

**In:** migration 0009 (`balance_snapshot`), snapshot CRUD + bulk, computed parent rollups, `ready_for_usage`, `general`, allocation over leaves, runway with four burn modes, `delta_total`/`delta_real`/`delta_fx`, reconciliation, `general_in(currency)`, capital UI, parity for the capital half.

**Out:** automatic FX refresh and the settings table (stage 07 — rates stay the manually-seeded ones from stage 03). Live `XAU`/`USDT` pricing (stage 07, and blocked on `TODO(anatol)` gold quantity).

## Contracts published here

```go
// internal/domain/capital
type Snapshot struct {
    AccountID int64
    Period    period.Period
    Amount    *money.Money  // XOR with QuantityNano
    QuantityNano *int64
    BaseAmount *money.Money
    FxRateID  *int64
}

func Value(d Data, accountID int64, p period.Period) *money.Money      // leaf: snapshot; parent: Σ children
func ReadyForUsage(d Data, p period.Period) money.Money                 // row 54
func General(d Data, p period.Period) money.Money                       // row 55
func Allocation(d Data, p period.Period) []Share                        // col C — LEAF accounts only
func Runway(d Data, p period.Period, mode BurnMode) *float64            // row 57
func GeneralIn(d Data, currency string, p period.Period) *money.Money   // rows 71–72

type BurnMode string
const (
    BurnEssentialPlanned BurnMode = "essential_planned"
    BurnTrailing3        BurnMode = "actual_trailing_3"   // default
    BurnTrailing12       BurnMode = "actual_trailing_12"
    BurnLegacyBlend      BurnMode = "legacy_blend"        // parity only
)

// CapitalChange splits what the workbook conflated.
type CapitalChange struct{ Total, Real, FX money.Money }
func Change(d Data, p period.Period) CapitalChange
```

`BurnLegacyBlend` exists **only** so parity can reproduce row 57 before the definition improves. It is never the default and is labelled as legacy in the UI.

## The fixes

**Deviation D6 — allocation, two bugs in one column.**

The workbook computed `C40 = 11000 ÷ 26581.66 = 41.4%` for `Cash HUF` — dividing **raw HUF** by **EUR** net worth. At 0.0028 HUF/EUR those forint are 30.80 EUR, so the real share is ≈**0.116%** — the appendix said 1.2% and was itself an arithmetic slip, corrected there. `C38` (`Cash USD`) has the same fault. **Convert to base currency before dividing.**

The doughnut plotted `A36:A50 / C36:C50`, which contains parents *and* their children — `Cash` alongside `Cash USD/EUR/HUF`, `Banks` alongside its four. Slices totalled ≈**162%**. **Allocate over leaf accounts only.**

**Deviation D7 — runway.** `B57`'s denominator used absolute refs `$C$28,$C$27,$B$28`, so every historical month divided by *today's* burn rate — the whole runway history rewrote itself whenever an average changed. **Each period uses its own burn rate.**

**Deviation D8 — capital change.** `E59 = E55 − D55` with undated rates mixes real saving and currency movement:

```
delta_total = general(t) − general(t−1)
delta_real  = Σ (quantity(a,t) − quantity(a,t−1)) × rate(a,t)
delta_fx    = delta_total − delta_real
```

## Tasks

**1. Migration — already applied.** `balance_snapshot` was created by migration **0006**, where §3.7 groups it, including `CHECK ((amount_minor IS NULL) <> (quantity_nano IS NULL))` and the period-format check. Both are asserted by `TestDDL_SnapshotRequiresExactlyOneOfValueOrQuantity` and `TestDDL_PeriodMonthShapeEnforced` from stage 03. No new migration is needed.

**2. `Value` with rollups.** Leaf → its snapshot converted to base at the period's rate. Parent → Σ children, recursively. **Parents are never stored** — this is what makes `Banks FOP` correct automatically, where the workbook omitted it from `Banks` (row 41) yet included it in `General` (row 55).

**3. `ReadyForUsage`.** Σ over `is_liquid = 1`. Verify `B54` = **18319.6584** (`6674.8 + 11644.8584`).

**4. `General`.** Σ over `counts_toward_net_worth = 1`. Verify `B55` = **26581.6584**. Note it counts "(Invests)" current value and excludes "(Invested)" cost basis, which lives on the account as `cost_basis_minor`.

**5. `Allocation`.** Leaf accounts only, converted first. **Assert shares sum to 1.0 ± 1e-9** — the workbook gives ≈1.62. Group by asset class with drill-down.

**6. `Runway`.** All four burn modes, each period using its own rate.

The sheet's `B57` = 7.136169061 divides by 2567.155885 = `(C28 + C27 + B28) / 3`,
and `C28` there is the **stale literal 2094.948491** that deviation D3 exists to
correct. Computing `C28` honestly gives 1780, a denominator of 2462.17 and a
runway of 7.44. `legacy_blend` therefore reproduces the sheet's *formula*, not its
*stale inputs*; the fixture records 7.136169061 alongside, citing D3 and D7.

**7. `Change`.** The three-way split. `delta_real` needs each account's *quantity* across two periods, so a snapshot recorded as a value in a foreign currency contributes its native amount as the quantity.

**8. `GeneralIn`.** Verify `B71` = **1329082.92** (`÷ 0.02`) and `B72` = **9493449.429** (`÷ 0.0028`), at each period's rate rather than today's.

**9. Reconciliation.** `GET /snapshots/{period}/reconciliation` — snapshot vs transaction-implied, drift per account. **The snapshot always wins**; drift is informational and never auto-corrects ([adr/0003](../adr/0003-snapshot-reconcile-balances.md)).

**10. Snapshot endpoints.** `GET /snapshots?period=` with `previous_amount` pre-filled, `PUT /snapshots/{account_id}/{period}`, `POST /snapshots/bulk`.

**11. Capital endpoints.** `GET /reports/capital?period=&burn_mode=`, `GET /reports/capital/series?from=&to=`, `GET /reports/net-worth-in/{currency}?period=`.

**12. Capital UI.** Per [08-ux.md](../08-ux.md) §8.5: hero net worth with the real/FX split, runway with its burn mode named and switchable, drill-down allocation doughnut, account list grouped by asset class showing native and base, snapshot entry pre-filled from last month, drift badges.

**13. Extend the parity fixture** with §A.4–A.5 values and deviations D6–D8.

## Tests

### Parity gate

| Test | Asserts |
| --- | --- |
| `TestParity_ReadyForUsage` | row 54; `B54` = 18319.6584 |
| `TestParity_General` | row 55; `B55` = 26581.6584 |
| `TestParity_Runway_LegacyBlend` | row 57; `B57` = 7.136169061 |
| `TestParity_GeneralInUAH` | `B71` = 1329082.92 |
| `TestParity_GeneralInHUF` | `B72` = 9493449.429 |
| `TestParity_AllocationSumsToOne` | **1.0 ± 1e-9; workbook's 1.62 recorded as D6** |
| `TestParity_CashHUFShare` | ≈0.116%, **not 41.4%** |

### Behaviour

| Test | Asserts |
| --- | --- |
| `TestDDL_SnapshotXOR_BothSet` | CHECK violation |
| `TestDDL_SnapshotXOR_NeitherSet` | CHECK violation |
| `TestDDL_SnapshotPeriodFormat` | `2026-7` rejected |
| `TestValue_ParentSumsChildren` | parent = Σ children, converted |
| `TestValue_ParentNotStored` | direct write to a parent → 422 |
| `TestValue_BanksFOPCountedOnce` | in `Banks` **and** `General`, not double |
| `TestValue_NestedParents` | two levels roll up |
| `TestAllocation_LeavesOnly` | no parent appears |
| `TestAllocation_ConvertsBeforeDividing` | HUF share correct |
| `TestRunway_PerPeriodBurnRate` | changing a later period's spend does **not** alter an earlier runway |
| `TestRunway_ZeroBurn_ReturnsNil` | `nil`, not `Inf` |
| `TestChange_SplitsRealAndFX` | rate moves with quantities fixed → `real` = 0, `fx` = total |
| `TestChange_QuantitiesOnly` | rate fixed, quantities move → `fx` = 0 |
| `TestChange_FirstPeriod_UsesOpeningSnapshot` | `D55` seed; no phantom −26,581 as in `P59` |
| `TestSnapshot_SkippedMonthIsGap` | absent, not interpolated |
| `TestSnapshot_BackdatedRipples` | later runway and change recompute |
| `TestReconciliation_DriftReported` | difference surfaced, snapshot unchanged |
| `TestUserIsolation` | extended to every new endpoint |

`TestRunway_PerPeriodBurnRate` is the direct regression test for D7 — under the workbook's absolute references it would fail.

## Verification

```bash
make verify
make parity          # now covers both halves of the workbook

curl -s -b j 'localhost:8080/api/v1/reports/capital?period=2026-06' \
  | jq '{general,ready_for_usage,runway_months,delta_real,delta_fx}'
curl -s -b j 'localhost:8080/api/v1/reports/capital?period=2026-06' \
  | jq '[.allocation[].share]|add'        # 1 (± float epsilon)
curl -s -b j 'localhost:8080/api/v1/reports/capital?period=2026-06' \
  | jq '.allocation[]|select(.name=="Cash HUF")|.share'   # ≈0.012, NOT 0.414
```

## Done checklist

- [ ] `make verify` and `make parity` green; stages 01–05 demos still work
- [ ] **The workbook is fully reproduced — both halves**
- [ ] Allocation sums to exactly 1.0
- [ ] `Cash HUF` ≈0.116%, not 41.4%
- [ ] Parents computed, never stored; `Banks FOP` counted once, correctly
- [ ] Runway uses each period's own burn rate
- [ ] Net-worth change splits into real and FX
- [ ] Both snapshot XOR constraints enforced at the DDL level
- [ ] Drift reported, never auto-corrected
- [ ] Snapshot entry pre-fills from the previous month
- [ ] `openapi.yaml` updated and valid
