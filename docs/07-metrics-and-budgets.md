# 07 — Metrics, Budgets and Thresholds

The spreadsheet, restated as unambiguous definitions. Every metric names its source cells so [appendix-excel-parity.md](appendix-excel-parity.md) can assert equality against real data.

## 7.1 Conventions

- **Period** — a calendar month, `YYYY-MM`. The sheet's columns `E`–`P` are Aug→Jul; the fiscal year start is a setting, default 8.
- **Recorded vs absent.** The sheet used `-1` for "not recorded" and `0` for "recorded, spent nothing". These are different facts and are now `NULL` and `0`.
- **Aggregates skip absent periods.** They never coerce absent to zero.
- All money is base currency unless stated. Conversion uses the period's rate ([06](06-fx-and-providers.md) §6.6).

## 7.2 Expense metrics

### `spend(category, period)` — grid `E9:P26`

```
spend = Σ base_amount_minor
        WHERE kind = 'expense'
          AND category_id = c
          AND occurred_on within period
          AND deleted_at IS NULL
```

`NULL` when the period has no recorded data at all — distinguishing "no data" from "zero spend", which the sheet could not.

**"Recorded" is defined per period, not per category**: a period is recorded when it
holds at least one non-deleted transaction. In a recorded period a category with no
rows is a genuine `0`; in an unrecorded period every category is `NULL`. Without
that rule the two facts collapse back together for any category that simply had a
quiet month. Asserted by `TestBudgetReport_UnrecordedIsNull` and
`TestBudgetReport_RecordedZeroIsZero`.

A transaction whose `base_amount_minor` is `NULL` — no rate covered its date — is
excluded from the sums and counted in the report's `unconverted` field. It is never
treated as zero, and the client shows the count.

### `planned(category, period)` — column `B`

One `budget` row per category per period. The sheet had a single annual figure; here it may vary by month, seeded from one value so setup is not 216 entries.

### `average(category)` — column `C`

```
average = mean( spend(category, p) for p in periods where spend IS NOT NULL )
```

Matches `AVERAGEIF(D9:P9, "<>-1")`. Verified: `House` sums to 8,124 over 11 recorded months → **738.5454545**, exactly `C9`.

Recorded zeros **are** included, as in the sheet. An explicit `exclude_from_average` flag on a transaction handles genuine one-offs — `Appliances` January is 10,248.28 against a 150 plan, which distorts the mean beyond usefulness. The sheet had no way to express this.

### `total(category)` — column `T`, `Results`

```
total = Σ spend(category, p) over recorded periods
```

**Fixes defect 1.** `T9 =SUM(E9:P9)` included the `-1` sentinel, so every total was understated by exactly 1: `T9` = 8,123 against a true 8,124, and `T19` (`Hobby`, all zeros) = **−1**. Excluding absent periods gives the correct total.

### `spend_total(period)` — row 27, `Amount`

```
spend_total = Σ spend(c, period) for all expense categories
```

**Fixes defect 2.** Row 27 used two different formulas: `E27 =SUM(E9:E26)` but `G27 =SUMIF(G9:G26,"<>-1")`. One definition now.

`C27` used `AVERAGEIF(D27:P27,"<>0")` — `<>0` rather than `<>-1`, so a month of genuinely zero spend would be silently dropped from the average. Absence, not zero, is the exclusion criterion.

Verified: 42,091.7108 over 11 months → **3826.519164**, exactly `C27`.

### `possible_minimum(period)` — row 28

```
possible_minimum = Σ planned(c, period) WHERE c.is_essential = 1
```

The sheet hardcoded 13 of 18 cells: `B28 =B9+B12+B13+B19+B20+B22+B21+B24+B25+B26+B17+B23+B18` = 1,780, excluding `Appliances`, `Hotel/Trip`, `Family`, `Gifts`, `Entertainment`. That set is now the `is_essential` flag, so reclassifying is a toggle rather than a formula edit.

**Fixes defect 5.** `C28` was the stale literal `2094.948491`. Now always computed.

## 7.3 Income

```
income(period) = Σ base_amount_minor WHERE kind = 'income' AND period
```

The sheet had one `Salary` row with line items crammed into formulas — `H31 =3186+183+200+86`, `L31 =2806+800+1100`, `O31 =3015+1658+200`. Those become real income transactions with a source, date and category, so the composition is inspectable instead of archaeology.

Monefy exports no income ([04](04-import-monefy.md) §4.8), so income entry is a v1 requirement.

**`TODO(anatol)`** — what those addends were. Bonuses, refunds, side income and gifts each want their own income category; until known, they import as a single `Salary` category with the composite value and a note.

## 7.4 Derived period metrics

### `diff(period)` — row 32

```
diff = income(period) − spend_total(period)
```

Both a retrospective figure and, for the current month, a live "left to spend" — same definition, different framing.

### `saved_percent(period)` — row 33

```
saved_percent = 1 − (spend_total ÷ income)      when income > 0
              = NULL                            when income = 0
```

Matches `(100-(100*E27/E31))/100` with the sheet's `IFERROR(…, 0)` replaced by `NULL`, because zero savings and unknown savings are different.

**Not clamped.** January is **−3.75** and that is the truth — spending was 4.75× income. Clamping would hide the most important month in the dataset. Charts render negatives; the axis accommodates them.

The named `LAMBDA SAVED_PERCENT` is dropped. It was hardwired to `$E$27/$E$31` — August only — and took no arguments (defect 7).

## 7.5 Capital metrics

### Account rollups — rows 37, 41

Not stored. `SUM` over child accounts, each converted at the period rate:

```
value(parent, period) = Σ base_value(child, period) for children of parent
```

The sheet's `B37 =(B38*$E$3)+(B40*$E$4)+(B39)` enumerated children by hand, which is why `Banks FOP` was omitted from `Banks` (row 41) yet included in `General` (row 55). As an ordinary child it now appears in both automatically.

### `ready_for_usage(period)` — row 54

```
= Σ base_value(a, period) WHERE a.is_liquid = 1
```

Sheet: `Cash + Banks`, excluding gold, investments and USDT.

### `general(period)` — row 55, net worth

```
= Σ base_value(a, period) WHERE a.counts_toward_net_worth = 1
```

Sheet: `B36+B37+B41+B46+B47+B50+B66+B68+B69` — including the "(Invests)" current-value rows and excluding the "(Invested)" cost-basis rows. `cost_basis_minor` on the account captures cost basis without a phantom account, so profit is `value − cost_basis`.

`D55 = 30723` sat outside the month grid as the seed for `E59`. It becomes an explicit opening snapshot for the period before the first recorded month.

### `allocation(period)` — column C, `Percents`

```
allocation(a) = base_value(a, period) ÷ general(period)
```

**Fixes defects 3 and 4.**

Defect 3 — the sheet divided **unconverted** amounts by EUR net worth. `C40` computed `11000 ÷ 26581.66 = 41.4%` for `Cash HUF`, treating 11,000 HUF as 11,000 EUR. At 0.0028 HUF/EUR those 11,000 forint are 30.80 EUR, so the real share is **≈0.116%** — a factor of 357 out, not the "≈1.2%" an earlier draft of this line claimed. Conversion now happens first. `C38` (`Cash USD`) had the same fault.

Defect 4 — the doughnut plotted `A36:A50 / C36:C50`, which contains both parents and children: `Cash` alongside `Cash USD/EUR/HUF`, `Banks` alongside its children. Slices summed to **≈162%**. Allocation is over **leaf accounts only**, with drill-down by asset class.

`C55 = 1` was a hardcoded 100%. It is now a genuine sum, and a mismatch is surfaced rather than asserted away.

### `runway(period)` — row 57, `Num of Months`

```
runway = ready_for_usage(period) ÷ burn_rate(period)
```

The sheet used `B54/(($C$28+$C$27+$B$28)/3)` — the mean of average essential spend, average total spend, and planned essential spend. Three different measures averaged together, with no stated rationale.

**Fixes defect 6.** Those were **absolute** references, so every month in row 57 divided by *today's* burn rate — the whole runway history silently rewrote itself whenever an average changed.

`burn_rate` is now an explicit, configurable choice:

| Mode | Definition |
| --- | --- |
| `essential_planned` | `possible_minimum(period)` |
| `actual_trailing_3` | mean actual spend over the previous 3 recorded periods |
| `actual_trailing_12` | mean over 12 |
| `legacy_blend` | the sheet's three-way mean, per period — for parity testing |

Default `actual_trailing_3`. `legacy_blend` exists so the parity appendix can reproduce the sheet exactly before the definition changes.

### `capital_change(period)` — row 59, `Diff in real capital`

The sheet: `E59 =E55-D55`. Because every balance was revalued at current FX, this mixed real saving with currency movement.

Split into three figures:

```
delta_total = general(t) − general(t−1)
delta_real  = Σ (quantity(a,t) − quantity(a,t−1)) × rate(a, t)
delta_fx    = delta_total − delta_real
```

`delta_real` is money actually saved or spent. `delta_fx` is the currency effect. The sheet reported only their sum, so a month where the hryvnia moved looked identical to a month of genuine saving.

`P59 = −26,581.66` was an artefact of `P55` being 0 for an unfilled July — the same sentinel fault as defect 1, and absent by construction here.

### Alternate-currency views — rows 71, 72

```
general_in(currency, period) = general(period) ÷ rate(currency, period)
```

`General UAH` and `General HUF` generalise to any currency, at the period's rate rather than today's.

## 7.6 Thresholds and colour rules

The sheet applied six overlapping conditional-format rules to `E9:P27`. Restated by explicit precedence — first match wins:

| Precedence | Condition | Sheet colour | Meaning |
| --- | --- | --- | --- |
| 1 | `spend IS NULL` | none | Not recorded |
| 2 | `spend = 0` | `#E8FAF2` pale | Recorded, nothing spent |
| 3 | `spend ≥ planned × over_multiplier` | `#F19189` red | Severely over |
| 4 | `spend > planned` | `#F4C7C3` pink | Over |
| 5 | `spend ≥ planned × (1 − warn_percent/100)` | `#FCE8B2` amber | Approaching |
| 6 | `spend > 0` | `#B7E1CD` green | Within budget |

Three points of order, all decided while implementing `budget.Classify`:

- **`NULL` is tested first**, because it cannot be compared at all.
- **`= 0` is tested before the over-budget rules**, which is where the workbook's own
  conditional formatting put it (appendix §A.6 rule 2). Under the previously written
  ordering, a plan of zero with zero spend classified as *severely over* — nonsense.
  With a positive plan the two orderings agree.
- **Level 3 is `≥`, not `>`**: exactly twice the plan is severely over, as the
  stage-03 test table requires. The threshold is computed as
  `plan × (100 − warn) ÷ 100` in exact rationals rather than `plan × (1 − warn/100)`,
  because the binary value of `0.9` is a shade above nine tenths and would put
  exactly 90% of plan just under the amber line.

**When nothing was planned** (no `budget` row, as for `Utilities` and `Taxi`),
`planned` and `ratio` are `null` and the state is `within` — there is no plan to be
over — and the UI labels the row "no plan set" rather than claiming it is within
budget. A plan of *recorded zero* is different: any spend against it is severely
over, which is literal and correct.

`warn_percent` is the sheet's `Percent for yellow` (`C2` = 10), so 90–100% of plan is amber. `over_multiplier` was hardcoded 2×; it is now a setting. Both are per-user, with optional per-category override.

Two corrections:

- The sheet's rules referenced `$B$9:$B$27` in some and `$B$9:$B$26` in others while applying to `E9:P27`, which includes the `Amount` row — ambiguous by construction. Row 27 is now explicitly compared against total planned.
- Row 59's rules compared `B55 > A55`, where `A55` holds the text `"General"` (defect 8). Row 59 is coloured on the sign of `delta_real`.

Colour is never the only signal: each state carries an icon and an accessible label, and the palette is remapped for dark mode where these pastels fail.

## 7.7 User-defined metrics

`saved_metric` stores built-ins as rows (`is_builtin = 1`) alongside user definitions, so both are listed, charted and reused identically.

A definition is a restricted expression over named, pre-computed quantities:

```
runway_essential = ready_for_usage / possible_minimum
savings_streak   = count(periods where saved_percent > 0.2)
food_share       = spend('Food') / spend_total
```

Deliberately **not** a general formula language:

- Identifiers resolve against a fixed registry of metrics and category or account references.
- Operators are `+ − × ÷`, comparison, and a small function set — `min`, `max`, `abs`, `mean`, `count`.
- No cell references, no loops, no I/O. Parsed to an AST and evaluated against loaded values; nothing is `eval`'d.
- Division by zero yields `NULL`, never an error or infinity.

This covers the spreadsheet's "calculation parameters" flexibility without importing the fragility that made the spreadsheet hard to trust.

## 7.8 Parity obligations

Golden-file tests load the 12 recorded months and assert, per period:

| Metric | Sheet reference |
| --- | --- |
| `spend` per category | `E9:P26` |
| `average` per category | `C9:C26` |
| `total` per category | `T9:T26` **with the sentinel fix applied** |
| `spend_total` | row 27 |
| `possible_minimum` | row 28 |
| `income`, `diff`, `saved_percent` | rows 31, 32, 33 |
| `ready_for_usage`, `general` | rows 54, 55 |
| `allocation` | column C **with conversion fixed** |
| `runway` under `legacy_blend` | row 57 |
| `general_in(UAH)`, `general_in(HUF)` | rows 71, 72 |

Four assertions deliberately differ from the sheet, each asserted against the **corrected** value with the sheet's value recorded alongside as a documented deviation: the sentinel totals, row 27's formula split, unconverted allocation percentages, and the parent-plus-child doughnut.
