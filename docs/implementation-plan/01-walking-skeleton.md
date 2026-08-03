# Stage 01 — Walking Skeleton

> **Kickoff prompt**
> Implement stage 01 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `docs/implementation-plan/01-walking-skeleton.md`, then `docs/02-architecture.md`. The repo is empty apart from `docs/`. Build the deployable skeleton described in the stage file. Do not implement any domain feature — scope-out is binding. Finish with `make verify` passing and the MVP demo working from `docker compose up`.

## Goal

Every layer connected end to end, with nothing in them yet. A container that boots, serves an embedded Vue page, answers health checks, and has migrations, config, logging, tests and CI wired up.

This exists so that from stage 02 onward, **every feature is added to a working, deployable system** — never integrated at the end.

## MVP demo

```bash
docker compose up -d
docker compose run --rm moneyapp migrate up
curl -s localhost:8080/healthz          # {"status":"ok"}
curl -s localhost:8080/readyz           # {"status":"ready","schema_version":1}
curl -s localhost:8080/version          # {"version":"dev","commit":"..."}
open http://localhost:8080              # Vue page: "MoneyApp" + backend version from /version
```

The page must fetch `/version` and render it — proving the SPA, the embedded assets and the API are genuinely connected, not three things that merely exist.

## Scope

**In:** repo layout, config, logging, SQLite, goose, Fiber, middleware, `Money`, `Period`, errors, embedded Vue shell, Dockerfile, compose, Makefile, CI, the store-scoping lint test.

**Out:** auth, users, categories, accounts, transactions, import, FX, bot, any real UI. `migrations/` contains only `0001` (currency, user, session) — tables exist, nothing uses them yet.

## Contracts published here

```go
// internal/platform/money   — full definition in 00-conventions.md §2
// internal/platform/apperr  — sentinels + ValidationError, §5
// internal/platform/clock
type Clock interface{ Now() time.Time }   // real + fixed test impl

// internal/config
type Config struct {
    Server    ServerConfig
    Database  DatabaseConfig
    Auth      AuthConfig
    Telegram  TelegramConfig
    Providers ProvidersConfig
    Backup    BackupConfig
    Log       LogConfig
    Reports   ReportsConfig // added in stage 03 for the budget thresholds
}
func Load(path string) (Config, error)   // yaml + env override + validate
func (c Config) Validate() error         // returns every problem, not just the first
```

`Clock` is injected everywhere from the start. Retrofitting it later to make time-dependent tests deterministic is painful, and stages 04, 07 and 08 all need it.

## Tasks

**1. Repo skeleton.** `go mod init`, directory tree from [02-architecture.md](../02-architecture.md) §2.5. Every package gets a `doc.go` stating its responsibility — cheap, and it keeps the layering honest.

**2. `internal/platform/money`.** Full implementation per §2. Table-driven tests covering: EUR 2-decimal, **HUF 0-decimal**, negative parse, `Add`/`Sub` currency mismatch, round-trip `Parse`→`String`, and overflow rejection. This package is the foundation of every money bug that will not happen.

**3. `internal/platform/apperr`.** Sentinels and `ValidationError`.

**4. `internal/platform/clock`.** Real and `Fixed(t)`.

**5. `internal/domain/period`.** `Period`, `ParsePeriod`, `Prev`, `Next`, `Range`. Tests for year boundaries — `2026-01`.`Prev()` is `2025-12`.

**6. `internal/config`.** YAML load, env override, `Validate()`. Validation must reject: unwritable DB path, `bcrypt_cost < 10`, non-absolute `base_url`, `telegram.enabled` with empty token. `config.yaml.dist` committed; `config.yaml` gitignored.

**7. Logging.** `log/slog` JSON to stdout. Request-ID middleware. **PII-masking handler** that redacts `password`, `token`, `api_key` and anything matching a Telegram bot-token pattern — port the idea from `golite`. Test that a logged struct containing a password emits `***`.

**8. SQLite.** `modernc.org/sqlite`, `CGO_ENABLED=0`. Open with `_pragma=foreign_keys(1)`, `journal_mode=WAL`, `busy_timeout=5000`. Pool capped at 4 ([10-deployment-ci.md](../10-deployment-ci.md) §10.3).

**9. Migration `0001`.** `currency`, `user`, `session`, transcribed exactly from [03-data-model.md](../03-data-model.md) §3.5. Seed `currency` with EUR, USD, HUF, UAH — **HUF exponent 0**. Working `Down`.

**10. Migration runner.** `moneyapp migrate up|down|status`, and a `schema_version` read used by `/readyz`.

**11. Fiber server.** Middleware chain: recover → request ID → structured log → CORS-off → routes. `/healthz`, `/readyz`, `/version`. `/readyz` returns **503** when the schema version is behind the binary's expectation — that is the guard from [adr/0011](../adr/0011-explicit-migrations.md), so test it by starting against an unmigrated DB.

**12. Vue shell.** Vite + TS + Tailwind v4 + Pinia. One page that fetches `/version` and renders it. Built to `internal/transport/rest/assets/dist`, served from `embed.FS` with SPA fallback to `index.html`.

**13. `TestNoUnscopedQueries`.** Scans `internal/store` for SQL against user-scoped tables lacking a `user_id` predicate. It passes trivially now — that is the point. It exists before there is anything to catch, so stage 02 cannot introduce the first violation.

**14. Dockerfile + compose.** Multi-stage per [10-deployment-ci.md](../10-deployment-ci.md) §10.4. Distroless, non-root, amd64 + arm64. `HEALTHCHECK` invoking `/moneyapp healthcheck` — no `curl` in the image.

**15. Makefile.** Every target in [00-conventions.md](00-conventions.md) §12.

**16. CI.** `lint`, `test`, `migrate-check` (up→down→up), `build` for both architectures. On merge to `main`, push `ghcr.io/antlko/moneyapp:main` and `:sha-<short>`.

## Tests

| Test | Asserts |
| --- | --- |
| `TestMoney_HUFZeroDecimal` | `Parse("1000","HUF",0)` → `Minor==1000` |
| `TestMoney_AddCurrencyMismatch` | returns `ErrCurrencyMismatch` |
| `TestMoney_ParseRoundTrip` | `Parse` → `String` is identity across a table |
| `TestPeriod_PrevAcrossYear` | `2026-01` → `2025-12` |
| `TestConfig_RejectsEnabledBotWithoutToken` | `Validate` errors, names the key |
| `TestConfig_ReportsAllProblems` | multiple bad keys → all reported |
| `TestLog_MasksSecrets` | password never appears in output |
| `TestReadyz_UnmigratedReturns503` | boot on empty DB → 503 |
| `TestSPAFallback` | `/some/route` serves `index.html`; `/api/v1/nope` is 404 JSON |
| `TestNoUnscopedQueries` | passes (no store code yet) |
| `TestMigrations_UpDownUp` | schema identical after cycle |

## Verification

```bash
make verify
make docker-build
docker compose up -d && docker compose run --rm moneyapp migrate up
curl -fs localhost:8080/healthz | grep -q '"ok"'
curl -fs localhost:8080/readyz  | grep -q '"ready"'
curl -fsI localhost:8080/ | grep -q '200 OK'
docker compose down
```

Expected: all green, image builds for both architectures, page renders the backend version.

## Done checklist

- [ ] `make verify` green on a clean checkout
- [ ] MVP demo reproducible from this file alone
- [ ] Image builds `linux/amd64` and `linux/arm64`, `CGO_ENABLED=0`
- [ ] `/readyz` returns 503 before migration, 200 after
- [ ] Vue page displays the backend version
- [ ] `Money` handles HUF exponent 0 correctly
- [ ] Config validation names every offending key
- [ ] Secrets masked in logs
- [ ] `TestNoUnscopedQueries` wired into CI
- [ ] No feature code — scope-out respected
