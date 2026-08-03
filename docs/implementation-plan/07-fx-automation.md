# Stage 07 — FX Automation & Settings

> **Kickoff prompt**
> Implement stage 07 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `07-fx-automation.md`, then **all of `docs/06-fx-and-providers.md`** and `docs/adr/0009-dated-fx-provider-chain.md`. Stages 01–06 are complete. Rates currently come from a manual seed; make them maintain themselves. Do not add any credentialed provider — Trading212 was dropped.

## Goal

Stop retyping rates. A daily scheduler pulls from free, keyless APIs into the dated `fx_rate` history, behind a generalised **manual/auto setting** mechanism that also covers thresholds, gold and crypto.

The FX table already exists (stage 03) and history is already dated (stage 06). This stage fills it automatically and makes the whole thing configurable without a redeploy.

## MVP demo

Settings → **Currencies**: each rate shows its value, mode, provider, last fetch and freshness → flip `fx.EUR_UAH` to **auto** → hit refresh → the live rate appears (≈51.08) with a "just now" badge → override it manually to 51.00 → the UI shows *"auto suggested 51.08 — you set 51.00"* with a reset → block outbound network and refresh again → the value **holds** with a staleness badge and a recorded error, and no screen breaks.

Then Thresholds: change `warn_percent` from 10 to 15 → the budget screen's amber band widens immediately, with no restart.

## Scope

**In:** migration 0010 (`provider`, `setting`), both provider clients, the fallback chain, the daily scheduler with boot catch-up and jitter, plausibility checks, the settings service and UI, contemporaneous vs constant valuation, thresholds moved out of config into settings, `XAU`/`USDT` price settings.

**Out:** any credentialed provider — **Trading212 is dropped, not deferred** ([06-fx-and-providers.md](../06-fx-and-providers.md) §6.7). User-defined metrics (stage 09).

## Providers — tested, not assumed

Verified live on 2026-07-29. Do not substitute without retesting.

| Provider | Endpoint | Key | UAH | Role |
| --- | --- | --- | --- | --- |
| `open-er-api` | `https://open.er-api.com/v6/latest/EUR` | none | ✅ | **primary**, 161 currencies, publishes `time_next_update_utc` |
| `fawazahmed0` | `https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/eur.json` | none | ✅ | **fallback**, 338 tickers incl. `XAU`, `USDT` |

**Rejected, with reasons — do not revisit without new evidence:**

- **Frankfurter / ECB** — 30 currencies, **UAH absent**. Confirmed by fetching `/v1/currencies`. UAH is required, so it cannot be primary.
- **DuckDuckGo Instant Answer** — returns `"production_state":"offline"` with `Answer`, `AbstractText` and `AnswerType` all empty. It is not a currency API.

Expected shapes:

```jsonc
// open-er-api
{ "base_code":"EUR", "time_next_update_utc":"...", "rates": { "USD":1.138108, "HUF":360.409427, "UAH":51.080258 } }
// fawazahmed0
{ "date":"2026-07-29", "eur": { "usd":1.13964137, "huf":360.87703934, "uah":51.24129442, "xau":0.0002829015 } }
```

## Contracts published here

```go
// internal/provider
type RateProvider interface {
    Key() string
    // Fetch returns EUR->X rates. Partial responses are rejected wholesale.
    Fetch(ctx context.Context, base string) (map[string]*big.Rat, time.Time, error)
}

// internal/domain/setting
type Mode string // "manual" | "auto"
type Setting struct {
    Key string; Mode Mode
    ManualValue *string
    ProviderKey *string
    LastValue, LastError *string
    LastFetchedAt *time.Time
    RefreshInterval time.Duration
}
type Service interface {
    // Effective returns the value in force: manual override wins over auto.
    Effective(ctx context.Context, userID int64, key string) (*string, error)
    Refresh(ctx context.Context, userID int64, key string) (*Setting, error)
    IsStale(s Setting, now time.Time) bool
}

// Valuation mode, threaded through every capital report.
type Valuation string
const (
    Contemporaneous Valuation = "contemporaneous" // default; each period at its own rate
    Constant        Valuation = "constant"        // today's rate; reproduces the workbook
)
```

## Tasks

**1. Migration — mostly already applied.** `provider` and `setting` were created by
migration **0004**, with the two FX providers seeded, because §3.7 groups them
there. Migration **0008** adds only what is new: `audit_log`, and the `metal` and
`crypto` provider rows. `kind IN ('fx','metal','crypto')` — **no `broker`** — is
already enforced from 0004 and is pinned by `TestNoBrokerProviderKind`.

Settings are **seeded in code**, not by migration: they are user-scoped, and at
migration time there is no user to own them (the same reason the taxonomy is code
— [03-data-model.md](../03-data-model.md) §3.7). `seed.Settings()` installs
`threshold.warn_percent` = 10 (`C2`), `threshold.over_multiplier` = 2,
`report.base_currency` = EUR, `report.fiscal_year_start` = 8, the three FX keys and
the two price keys. `Provisioner.EnsureSettings` backfills them at boot for
accounts created before this stage, so an upgrade does not land on an empty
settings screen.

Do **not** seed `EUR/USD` as a stored rate — it is referenced by no workbook formula, and only `EUR→X` is stored. Likewise `Month = 12` (`G2`) is dead and is not migrated.

**2. `open-er-api` client.** Parse, validate `base_code`, convert to `big.Rat` from the decimal text, read `time_next_update_utc` for scheduling. Timeout 10s, one retry.

**3. `fawazahmed0` client.** Same interface. Also exposes `xau` and `usdt`.

**4. Fallback chain.** Primary → fallback → keep last value. **A provider failure is never fatal** and never blocks a render.

**5. Plausibility check.** Reject a single-day move beyond `providers.fx.plausibility_max_change` (default 0.15) and flag it. A silently wrong rate corrupts every derived figure while looking like a real event, so this is a correctness control, not politeness.

**6. Persist rates.** Insert into `fx_rate` with `source` = provider key. `UNIQUE (as_of_date, base, quote, source)` makes re-running idempotent and lets two providers disagree without conflict; `provider.priority` resolves at read time.

**7. Scheduler.** Daily, jittered. On boot, refresh anything older than its interval — a restart must not skip a day. Weekend and holiday gaps are **normal** for FX and are not errors; the nearest-earlier lookup from stage 03 covers them.

**8. Settings service.** `Effective` with manual-over-auto precedence. `Refresh` forces a fetch. `IsStale` drives the badge.

**9. Move thresholds out of config.** Stage 03 read `warn_percent` and `over_multiplier` from config defaults; they now come from `setting`, per user, changeable without restart. Optional per-category override.

**10. Valuation modes.** `?valuation=` on every **capital** endpoint. Default
`contemporaneous`. `constant` reproduces the workbook's behaviour — both derive
from the same rows, so this is a read-time choice, not stored data.

Scoped to capital, deliberately. A transaction's `base_amount_minor` is computed
at its own date when it is written, with the `fx_rate_id` recorded
([adr/0009](../adr/0009-dated-fx-provider-chain.md)); revaluing spending history
at today's rate would mean re-converting every row on every read and would
contradict that audit trail. The workbook's revaluation problem was in the
capital block — deviation D8 — and that is where the switch belongs. The budget
report still accepts `?valuation=` for compatibility and is always
contemporaneous.

**11. Price settings.** `price.XAU` and `price.USDT`, backed by `fawazahmed0`. **`price.XAU` stays `manual`** until the gold quantity is supplied — `Gold = 3000` is a value, not a holding, and inferring ~0.85 troy oz from a possibly stale valuation would be fabrication. Leave `TODO(anatol)` in the seed comment.

**12. Settings endpoints.** `GET /settings`, `PATCH /settings/{key}`, `POST /settings/{key}/refresh`, `GET /providers`, `GET /fx/rates`, `GET /fx/rates/latest`.

**13. Settings UI.** Per [08-ux.md](../08-ux.md) §8.7, grouped Currencies / Prices / Thresholds / Periods / Integrations / Account. Every auto setting shows provider, last fetch, staleness and last error. Overrides show both values with a reset.

**14. Audit.** Log setting changes to `audit_log` — who changed `warn_percent`, when.

## Tests

Provider tests use **recorded fixtures**, not live calls. One opt-in `-tags=live` test hits the real endpoints so drift is detectable without making CI depend on the internet.

| Test | Asserts |
| --- | --- |
| `TestProvider_ParseOpenErApi` | fixture → correct `big.Rat` for USD, HUF, UAH |
| `TestProvider_ParseFawazahmed` | same, plus `xau` |
| `TestProvider_PartialResponseRejected` | missing required currency → whole response rejected |
| `TestProvider_MalformedRejected` | no partial application |
| `TestChain_PrimaryFails_UsesFallback` | fallback value stored |
| `TestChain_BothFail_KeepsLastValue` | value held, `last_error` set, marked stale |
| `TestPlausibility_RejectsImplausibleMove` | 10× jump rejected and flagged |
| `TestPlausibility_AcceptsNormalMove` | 2% accepted |
| `TestScheduler_BootCatchUp` | stale on boot → refreshed |
| `TestScheduler_Idempotent` | twice in a day → one row per source |
| `TestScheduler_WeekendGapNotAnError` | gap tolerated, no error logged |
| `TestSetting_ManualOverridesAuto` | manual wins; auto value still visible |
| `TestSetting_ResetRestoresAuto` | reverts to provider value |
| `TestThreshold_ChangeAffectsStateNoRestart` | 10→15 widens amber immediately |
| `TestValuation_ConstantMatchesWorkbook` | `constant` reproduces the workbook's revalued history, and the two modes genuinely differ where rates moved |
| `TestNoBrokerProviderKind` | inserting `kind='broker'` → CHECK violation |
| `TestParity_StillGreen` | stages 05–06 parity unaffected |

`TestNoBrokerProviderKind` pins the Trading212 removal at the schema level so it cannot creep back.

## Verification

```bash
make verify
make parity

curl -s -b j localhost:8080/api/v1/settings | jq '.[]|{key,mode,effective_value,stale}'
curl -s -b j -X POST localhost:8080/api/v1/settings/fx.EUR_UAH/refresh | jq '{last_value,last_fetched_at,stale}'
curl -s -b j 'localhost:8080/api/v1/fx/rates?quote=UAH&from=2026-07-01' | jq 'length'

# offline behaviour
docker network disconnect bridge moneyapp
curl -s -b j -X POST localhost:8080/api/v1/settings/fx.EUR_UAH/refresh | jq '{last_value,last_error,stale}'
curl -fs 'localhost:8080/api/v1/reports/capital?period=2026-06' >/dev/null && echo "renders offline OK"
docker network connect bridge moneyapp
```

## Done checklist

- [ ] `make verify` and `make parity` green; stages 01–06 demos still work
- [ ] Rates refresh daily without intervention
- [ ] Provider outage keeps the last value, badges it stale, breaks nothing
- [ ] Implausible rate moves rejected and flagged
- [ ] Manual override wins and shows both values
- [ ] Thresholds live in settings; changes apply without restart
- [ ] Both valuation modes work; `constant` reproduces the workbook
- [ ] No credentialed provider; `kind='broker'` rejected by the schema
- [ ] `price.XAU` left manual pending `TODO(anatol)` gold quantity
- [ ] Capital reports render with all providers disabled
