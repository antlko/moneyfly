# ADR 0009 — Dated FX history with a provider chain

**Status:** Accepted · **Date:** 2026-07-29

## Context

The spreadsheet holds four hand-typed rates with **no date**: `EUR/USD` 1.14, `USD/EUR` 0.88, `HUF/EUR` 0.0028, `UAH/EUR` 0.02. Three problems:

1. `1 ÷ 1.14 = 0.8772`, not `0.88` — two numbers for one fact, already inconsistent.
2. `EUR/USD` is referenced by **no formula**. Dead data. (`Month = 12` likewise.)
3. Undated rates mean **history is silently revalued** whenever a rate is edited, so `Diff in real capital` blends real saving with currency movement and cannot be decomposed.

The owner asked for daily automatic updates from a free API, and specifically for DuckDuckGo to be investigated.

## Decision

Store **one direction only** (`EUR→X`) in a dated `fx_rate` table, as decimal **strings** parsed into `big.Rat`. Refresh daily from a provider chain: **`open.er-api.com` primary, `fawazahmed0/currency-api` fallback**. Lookup is exact date, else nearest **earlier** date, else `NULL`.

## Provider evaluation — tested live 2026-07-29

| Provider | Key | UAH | HUF | XAU | Crypto | Count | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `open.er-api.com` | none | ✅ | ✅ | ✗ | ✗ | 161 | **Primary** |
| `fawazahmed0` via jsDelivr | none | ✅ | ✅ | ✅ | ✅ | 338 | **Fallback + metals/crypto** |
| Frankfurter / ECB | none | ❌ | ✅ | ✗ | ✗ | 30 | Rejected |
| DuckDuckGo Instant Answer | none | ❌ | ❌ | ✗ | ✗ | — | Rejected |

**Frankfurter rejected** because ECB reference rates cover 30 currencies and **UAH is not among them** — confirmed by fetching `/v1/currencies`. UAH is required, so ECB cannot be primary. It stays usable as a third opinion for EUR, USD and HUF.

**DuckDuckGo rejected** on evidence. `GET api.duckduckgo.com/?q=1+EUR+to+UAH&format=json` returns `Answer`, `AbstractText` and `AnswerType` as empty strings with `"production_state": "offline"`. The conversion Instant Answer is not in production; the endpoint returns metadata about itself. It is not a currency API.

Both chosen providers agree with the hand-maintained values, confirming the `X/EUR = 1 ÷ (EUR→X)` convention.

## Rationale

- Single-direction storage structurally eliminates the `1.14`/`0.88` inconsistency.
- Dated rates make **contemporaneous valuation** possible, which is what allows net-worth change to split into `delta_real` and `delta_fx` — the analysis the spreadsheet could never do.
- No API key on either provider means no secret to manage, no quota to exceed, no account to lapse.
- `XAU` availability means gold can be priced rather than retyped.

## Consequences

- Two "opinions" per day are possible, so `UNIQUE (as_of_date, base, quote, source)` allows both and `provider.priority` resolves. No conflict, and provider disagreement stays visible.
- FX markets close, so weekend and holiday gaps are normal. Nearest-earlier lookup handles them; they are not errors.
- A provider outage must never block rendering. The last known value keeps serving with a staleness badge; the scheduler is the only network caller.
- A **plausibility check** rejects an implausible single-day move (default >15%) and flags it. A silently wrong rate would corrupt every derived figure while looking like a real event.
- Historical rates before the first fetch do not exist. The `migrate-excel` command seeds the spreadsheet's stated rates with `source = 'excel-import'` so old valuations remain reproducible.
- Both providers are third parties that could disappear. Mitigated by two independent sources, plus full manual mode — the app works completely with providers disabled.

## Generalisation

FX is not special-cased. Every numeric setting is `manual` or `auto` with a provider behind `auto`, so rates, metal prices and crypto prices share one mechanism, one settings screen and one audit trail. A manual override always wins and the UI shows both values. See [06](../06-fx-and-providers.md) §6.4.

Providers are restricted to **free, keyless** sources. Anything requiring a credential — a broker API, for instance — is out of scope, which keeps the provider layer free of secret management entirely.
