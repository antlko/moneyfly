# ADR 0004 — Money as integer minor units

**Status:** Accepted · **Date:** 2026-07-29

## Context

The spreadsheet stores money as floats, and the damage is visible in the raw cell values: `116.33999999999999`, `293.0025`, `78.624`, `1067.230491`. Values are summed, averaged, divided into percentages and multiplied by FX rates, so error compounds through every derived figure. The existing parser uses `float64` throughout and formats with `%.3f`, which hides drift rather than preventing it.

Four currencies are in play with different conventions: EUR and USD have 2 decimals, **HUF effectively has 0**, and amounts reach 4,562,000.

## Decision

Store money as `INTEGER` **minor units** plus a currency code. Per-currency exponent from the `currency` table. A `Money{Amount int64, Currency string}` type in `internal/platform`. FX rates stored as decimal **strings**, parsed into `math/big.Rat`.

Never `REAL`. Never `float64` in a domain type.

## Rationale

- Addition and subtraction become exact, which is most of what the app does.
- HUF is handled correctly: `-1000 HUF` is `amount_minor = 1000`, not `100000`. Getting this wrong scales every Hungarian figure by 100.
- `big.Rat` for rates means a conversion rounds **once**, at the end, rather than accumulating error per operation.
- `int64` minor units span far beyond any plausible balance, including millions of HUF.

## Consequences

- Every boundary must scale explicitly: CSV parsing, API serialisation, UI formatting. Centralised in one type with tests per currency.
- The API never sends a bare number. It sends `{ "amount_minor": 6500, "currency": "EUR", "exponent": 2 }`, and the client formats. Slightly more verbose, unambiguous.
- Division — percentages, averages, runway — produces a ratio, not money. Ratios are `float64`; the rounding rule and direction are specified at each call site.
- Cross-currency arithmetic is refused at runtime. Conversion must be explicit and records the `fx_rate_id` used.
- Migrating the spreadsheet's floats requires a documented rounding rule: half-up at the currency's exponent, with the original value retained in the import note so any discrepancy is traceable.

## Alternatives rejected

| Option | Why not |
| --- | --- |
| `float64` | The defect being fixed |
| SQLite `DECIMAL` | No such type; it is `REAL` affinity in disguise |
| Decimal string in the database | Every aggregate would need parsing; loses SQL `SUM` |
| `big.Rat` for amounts | Overkill for storage; no exact SQL representation |
