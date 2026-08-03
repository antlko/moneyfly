# Appendix — Excel Parity

The acceptance criteria. Every populated region of `[2025-2026] Budget_ Capital Grow.xlsx` mapped to its app equivalent, with deviations stated explicitly.

Sheet `Budget` is the only sheet with cell data; `Visualisation` holds charts only.

## A.1 Settings — rows 1–5

| Cell | Label | Value | App equivalent | Note |
| --- | --- | --- | --- | --- |
| `C2` | Percent for yellow | 10 | `setting['threshold.warn_percent']` | Used by conditional formatting |
| `E2` | EUR/USD | 1.14 | — | **Referenced by no formula.** Dropped; single-direction storage |
| `E3` | USD/EUR | 0.88 | `fx_rate(EUR→USD)` inverted | `1÷1.14 = 0.8772` — already inconsistent |
| `E4` | HUF/EUR | 0.0028 | `fx_rate(EUR→HUF)` inverted | |
| `E5` | UAH/EUR | 0.02 | `fx_rate(EUR→UAH)` inverted | |
| `G2` | Month | 12 | — | **Referenced by no formula.** Dropped |
| — | over multiplier | *hardcoded 2×* | `setting['threshold.over_multiplier']` | Was not configurable |

## A.2 Expense grid — rows 8–26

Columns: `A` name, `B` `Planned (Month)`, `C` `AVG`, **`D` blank**, `E`–`P` = August→July, `Q`–`S` unused, `T` `Results`.

| Row | Category | Planned | Essential | Canonical name |
| --- | --- | --- | --- | --- |
| 9 | House | 750 | ✅ | House |
| 10 | Applience | 150 | ✗ | **Appliances** |
| 11 | Hotel/Trip | 250 | ✗ | Hotel/Trip |
| 12 | Food | 300 | ✅ | Food |
| 13 | Eating out | 100 | ✅ | Eating out |
| 14 | Family | 60 | ✗ | Family |
| 15 | Gifts | 80 | ✗ | Gifts |
| 16 | Entertainment | 30 | ✗ | Entertainment |
| 17 | Toilery | 15 | ✅ | **Toiletry** |
| 18 | Studying | 15 | ✅ | Studying |
| 19 | Hobby | 20 | ✅ | Hobby |
| 20 | Clothes | 30 | ✅ | Clothes |
| 21 | Transport | 150 | ✅ | Transport |
| 22 | Health | 220 | ✅ | Health |
| 23 | Communications | 20 | ✅ | Communications |
| 24 | Sport | 30 | ✅ | Sport |
| 25 | Bills | 30 | ✅ | Bills |
| 26 | Services | 100 | ✅ | Services |
| — | — | — | ✗ | **Utilities** (new — 119 orphaned rows) |
| — | — | — | ✗ | **Taxi** (new — 1 orphaned row) |

Essential = membership of `B28`'s formula. Corrected canonical spellings keep the originals as aliases.

| Cell range | Formula | App equivalent |
| --- | --- | --- |
| `B9:B26` | literal | `budget.planned_minor` |
| `C9:C26` | `IFERROR(AVERAGEIF(D9:P9,"<>-1"),0)` | `average(category)` |
| `E9:P26` | literal | `spend(category, period)` |
| `T9:T26` | `SUM(E9:P9)` | `total(category)` — **deviation D1** |
| `D` column | blank, inside `AVERAGEIF` range | dropped |

## A.3 Roll-ups — rows 27–33

| Cell | Label | Formula | App equivalent |
| --- | --- | --- | --- |
| `B27` | Amount planned | `SUM(B9:B26)` = 2350 | `Σ planned` |
| `C27` | Amount AVG | `IFERROR(AVERAGEIF(D27:P27,"<>0"),0)` | `mean(spend_total)` |
| `E27` | Amount Aug | `SUM(E9:E26)` | `spend_total` — **deviation D2** |
| `G27` | Amount Oct | `SUMIF(G9:G26,"<>-1")` | `spend_total` — **deviation D2** |
| `B28` | Possible Minimum | 13-term sum = 1780 | `Σ planned WHERE is_essential` |
| `C28` | Possible Min AVG | **literal 2094.948491** | computed — **deviation D3** |
| `B31` | Salary planned | 2800 | `budget` on income category |
| `E31:P31` | Salary actual | literals, some inline sums | `income(period)` from line items |
| `B32`/`E32:P32` | Diff | `B31-B27` | `income − spend_total` |
| `B33`/`E33:P33` | Saved % | `(100-(100*B27/B31))/100` | `1 − spend_total÷income` |
| *named* | `SAVED_PERCENT` | `LAMBDA` hardwired to `$E$27/$E$31` | dropped — **deviation D4** |

## A.4 Capital — rows 35–55

| Row | Label | Formula | `asset_class` | liquid | net worth |
| --- | --- | --- | --- | --- | --- |
| 36 | Gold | literal 3000 | `metal` | ✗ | ✅ |
| 37 | Cash | `(B38*E3)+(B40*E4)+B39` | *computed parent* | ✅ | ✅ |
| 38 | Cash USD | literal | `cash` | ✅ | ✅ |
| 39 | Cash EUR | inline sums | `cash` | ✅ | ✅ |
| 40 | Cash HUF | inline sums | `cash` | ✅ | ✅ |
| 41 | Banks | `(B42*E3)+B43+(B44*E4)+(B45*E5)` | *computed parent* | ✅ | ✅ |
| 42 | Banks USD | inline sums | `bank` | ✅ | ✅ |
| 43 | Banks EUR | inline sums | `bank` | ✅ | ✅ |
| 44 | Banks HUF | inline sums | `bank` | ✅ | ✅ |
| 45 | Banks UAH | inline sums | `bank` | ✅ | ✅ |
| 46 | Banks FOP | literal 0 | `bank` | ✅ | ✅ |
| 47 | Deposits | `G48*E3+G49` in `G` only | `deposit` | ✗ | ✅ |
| 48–49 | Deposits USD/EUR | **labels only, no data** | `deposit` | ✗ | ✅ |
| 50 | Invests | `3187+498` | `investment` | ✗ | ✅ |
| 51 | Invested | `4173-2003` | → `cost_basis_minor` | — | ✗ |
| 54 | Ready for usage | `B37+B41` | `Σ WHERE is_liquid` | | |
| 55 | General | `B36+B37+B41+B46+B47+B50+B66+B68+B69` | `Σ WHERE counts_toward_net_worth` | | |
| `D55` | *seed* | literal 30723 | opening snapshot | | |
| `C36:C55` | Percents | mixed formulas and literals | `allocation` — **deviations D5, D6** | | |

**`Banks FOP` is omitted from `Banks` (41) but included in `General` (55).** As an ordinary child of `Banks` it is now in both automatically — a bug class that disappears with computed parents.

## A.5 Runway and risk capital — rows 57–72

| Row | Label | Formula | App equivalent |
| --- | --- | --- | --- |
| 57 | Num of Months | `B54/((C28+C27+B28)/3)` | `runway`, `burn_mode=legacy_blend` — **deviation D7** |
| 59 | Diff in real capital | `E55-D55` | `delta_total` split into `delta_real` + `delta_fx` — **deviation D8** |
| 65 | CSGO Skins (Invested) | literal | `cost_basis_minor` |
| 66 | CSGO Skins (Invests) | literal | `account`, `other`, counts |
| 67 | Ton (Invested) | all zero | `cost_basis_minor` |
| 68 | Ton (invests) | all zero | `account`, `crypto`, counts |
| 69 | USDT (EUR) | literal 1577 | `account`, `crypto`, ticker `USDT` |
| 71 | General UAH | `B55/E5` | `general_in('UAH')` |
| 72 | General HUF | `B55/E4` | `general_in('HUF')` |

## A.6 Conditional formatting — `E9:P27`

| Priority | Rule | Colour | App state |
| --- | --- | --- | --- |
| 1 | `> B9:B27 × 2` | `#F19189` | `severely_over` |
| 2 | `= 0` | `#E8FAF2` | `zero` |
| 3 | between `B−(B×C2/100)` and `B` | `#FCE8B2` | `approaching` |
| 4 | `> B9:B27` | `#F4C7C3` | `over` |
| 5 | colour scale `0 → B9:B26` | `#DAF1E6`→`#57BB8A` | gradient |
| 6 | `≤ B9:B27` | `#B7E1CD` | `within` |

Rules mix `$B$9:$B$27` and `$B$9:$B$26` while applying to a range that includes row 27 — ambiguous as written. Restated by explicit precedence in [07](07-metrics-and-budgets.md) §7.6.

Row 59 rules compare `B55>A55`, where `A55` is the text `"General"` — **deviation D9**.

## A.7 Charts

| Chart | Source | App equivalent |
| --- | --- | --- |
| Saved % — line | `E33:P33` | savings-rate series |
| Spend Amount — line | `E27:P27` | spend series |
| Months — line | `E57:P57` | runway series |
| Percents — doughnut | `A36:A50` / `C36:C50` | allocation — **deviation D6** |

None bind a category axis, so all three lines plot against a bare index rather than month names. All three include unfilled July and therefore **dive to zero at the right edge** — **deviation D10**.

## A.8 Deviations

Eleven, each deliberate. Parity tests assert the **corrected** value and record the sheet's value alongside.

| # | Sheet behaviour | Corrected | Evidence |
| --- | --- | --- | --- |
| **D1** | `SUM` includes the `-1` sentinel | absent periods excluded | `T9` = 8123, true 8124; `T19` = **−1** |
| **D2** | Row 27 uses `SUM` in one column, `SUMIF` in another | one definition | `E27` vs `G27` |
| **D3** | `C28` is a stale literal | computed | `C28` = 2094.948491 |
| **D4** | `SAVED_PERCENT` LAMBDA hardwired to August | dropped | takes no arguments |
| **D5** | Percents mix formulas and stale literals; `C42:C45` empty | all computed | `C37`–`C41` literal |
| **D6** | Allocation divides **unconverted** amounts, and plots parents + children | convert first; leaf accounts only | `C40` = **41.4%** for 11,000 HUF, truly **≈0.116%**; slices total **≈162%** |
| **D7** | Runway denominator absolute → history rewritten | per-period burn rate | `$C$28,$C$27,$B$28` |
| **D8** | Capital change conflates saving and FX | split `delta_real` / `delta_fx` | undated rates |
| **D9** | Row 59 format compares number to text | sign of `delta_real` | `B55>A55`, `A55` = `"General"` |
| **D10** | Charts include unfilled months and dive to zero | incomplete months excluded; months labelled | `P27`, `P33`, `P57` = 0 |
| **D11** | Cells carry sub-cent values from unconverted FX arithmetic | rounded half-up to integer cents | 66 of the 198 expense cells; `G10` = 74.0272 |

**D11 was found by the code, not by reading the sheet.** Money here is integer
minor units ([adr/0004](adr/0004-integer-money.md)), so a cell like `74.0272`
becomes `7403`. Summing the rounded cells moves five of the twelve monthly totals
by one cent, and `Amount AVG` from the sheet's 3826.519164 to 3826.517273 — well
inside the 1e-6 relative tolerance §A.10 allows on ratios, and the parity fixture
records both figures either way.

## A.9 Verified reference values

Confirmed by recomputation from the raw cells — the anchors for the golden-file tests.

| Metric | Sheet | Verification |
| --- | --- | --- |
| `House` AVG (`C9`) | 738.5454545 | 8124 ÷ 11 recorded months ✅ |
| `House` total (`T9`) | 8123 | true 8124 — off by the sentinel ✅ D1 |
| `Hobby` total (`T19`) | −1 | true 0 — a year of no spending ✅ D1 |
| `Amount` AVG (`C27`) | 3826.519164 | 42091.7108 ÷ 11 ✅ (3826.517273 from cent-rounded cells — D11) |
| `Possible Minimum` (`B28`) | 1780 | 13-term sum ✅ |
| `Saved %` planned (`B33`) | 0.1607142857 | `1 − 2350÷2800` ✅ |
| `Saved %` Jan (`J33`) | −3.751067461 | spend 4.75× income — **not clamped** ✅ |
| `Ready for usage` (`B54`) | 18319.6584 | `6674.8 + 11644.8584` ✅ |
| `General` (`B55`) | 26581.6584 | 9-term sum ✅ |
| `Num of Months` (`B57`) | 7.136169061 | `18319.6584 ÷ 2567.155885` ✅ |
| `General UAH` (`B71`) | 1329082.92 | `26581.6584 ÷ 0.02` ✅ |
| `General HUF` (`B72`) | 9493449.429 | `26581.6584 ÷ 0.0028` ✅ |
| `Cash HUF` percent (`C40`) | 0.4138191769 | `11000 ÷ 26581.6584` — **unconverted** ✅ D6. Converted: `30.80 ÷ 26581.6584` = **0.00115869** |

## A.10 Test obligation

```
TestExcelParity
  for each period in Aug..Jul:
    assert spend(c, p)            == sheet[row(c)][col(p)]     for all 18
    assert average(c)             == sheet[row(c)]["C"]
    assert total(c)               == sheet[row(c)]["T"] + sentinel_correction(c)
    assert spend_total(p)         == sheet[27][col(p)]
    assert possible_minimum(p)    == 1780
    assert income(p), diff(p), saved_percent(p) == rows 31, 32, 33
    assert ready_for_usage(p)     == sheet[54][col(p)]
    assert general(p)             == sheet[55][col(p)]
    assert runway(p, legacy_blend)== sheet[57][col(p)]
    assert general_in("UAH", p)   == sheet[71][col(p)]
    assert general_in("HUF", p)   == sheet[72][col(p)]
    assert sum(allocation(p))     == 1.0   ± 1e-9      // sheet gives ~1.62
```

Tolerance `1e-6` relative on ratios; **exact** on money, since money is integer minor units ([adr/0004](adr/0004-integer-money.md)).

Alongside this, the import fidelity gate from [adr/0007](adr/0007-blocking-alias-mapping.md): the real 1,683-row export must yield **exactly 1,683** stored rows.
