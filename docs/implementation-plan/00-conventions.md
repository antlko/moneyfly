# 00 — Conventions

The shared contract. Every stage assumes this file. Read it once; do not restate it in stage work.

## 1. Layering

```
cmd/moneyapp          wiring only — no logic
internal/transport    HTTP handlers, Telegram handlers, DTOs, validation
internal/domain       business logic, pure where possible
internal/store        repositories, SQL
internal/provider     outbound HTTP clients
internal/platform     Money, errors, logging, clock
```

Four rules, enforced by review and by the import-lint test in stage 01:

1. **`internal/transport` never imports `internal/store`.** Handlers call domain services.
2. **`internal/domain` never imports `internal/transport`.** No `fiber.Ctx` below transport.
3. **SQL lives only in `internal/store`.** Domain receives typed values.
4. **`internal/platform` imports nothing from the other four.**

## 2. Money

`internal/platform/money`. Integer minor units, never floats. See [adr/0004](../adr/0004-integer-money.md).

```go
package money

// Money is an exact amount in one currency, stored in minor units.
// HUF has exponent 0, so Money{1000,"HUF"} is 1000 forint, not 10.
type Money struct {
    Minor    int64
    Currency string // ISO-4217, uppercase
}

func New(minor int64, currency string) Money

// Add and Sub return ErrCurrencyMismatch when currencies differ.
// There is no implicit conversion anywhere in this package.
func (m Money) Add(o Money) (Money, error)
func (m Money) Sub(o Money) (Money, error)
func (m Money) Neg() Money
func (m Money) IsZero() bool

// Parse reads a decimal string ("-65", "397.30") into minor units for the
// given currency's exponent. Rejects anything it cannot represent exactly.
func Parse(s, currency string, exponent int) (Money, error)

// String renders with the currency's exponent. Money{1000,"HUF"} -> "1000".
func (m Money) String(exponent int) string
```

Rules:

- **No `float64` in any domain type.** Ratios (percentages, runway, saved %) are `float64` and are explicitly *not* money.
- **Exponents come from the `currency` table**, never hardcoded. Look up once per request and pass down.
- **Cross-currency arithmetic is refused**, not silently converted. Conversion is `fx.Convert`, which returns the amount *and* the `fx_rate_id` used.
- Division produces a ratio. Rounding is half-up at the currency exponent, applied once, at the end.

## 3. Nullability

`NULL` means **not recorded**. `0` means **recorded zero**. They are never interchangeable — this is the spreadsheet's `-1` sentinel bug, and it must not return.

- Domain: `*Money` for an optionally-recorded amount. `nil` is absent.
- JSON: `null` vs `0`. Never coerce.
- SQL: aggregates must not `COALESCE(...,0)` a missing period into zero. Every aggregate has a test for the absent case.

## 4. Periods

```go
// Period is a calendar month, "YYYY-MM".
type Period string

func ParsePeriod(s string) (Period, error)
func (p Period) Prev() Period
func (p Period) Next() Period
func (p Period) Range() (from, to time.Time) // [first day, last day] inclusive
```

Fiscal year start is a user setting (default 8, from the workbook's Aug→Jul layout). It affects grouping and labels only — never storage.

## 5. Errors

Domain returns typed sentinels. Transport maps them. **Never match an error by string.**

```go
package apperr

var (
    ErrNotFound     = errors.New("not found")
    ErrConflict     = errors.New("conflict")
    ErrValidation   = errors.New("validation")
    ErrUnauthorized = errors.New("unauthorized")
    ErrForbidden    = errors.New("forbidden")
    // Added in stage 04: an upload over the configured cap. Separate from
    // ErrValidation because 413 is the honest answer — the request was
    // well-formed, it was simply too big.
    ErrTooLarge     = errors.New("payload too large")
)

// Field-level detail for 422 responses.
type ValidationError struct{ Fields []FieldError }
type FieldError struct{ Field, Message string }
```

| Sentinel | HTTP | `type` |
| --- | --- | --- |
| `ErrValidation` | 422 | `/problems/validation` |
| `ErrNotFound` | 404 | `/problems/not-found` |
| `ErrConflict` | 409 | `/problems/conflict` |
| `ErrUnauthorized` | 401 | `/problems/unauthorized` |
| `ErrForbidden` | 403 | `/problems/forbidden` |
| `ErrTooLarge` | 413 | `/problems/too-large` |
| anything else | 500 | `/problems/internal` |

A 500 logs the full error with a request ID and returns **only** the request ID to the client. Internal detail never crosses the wire.

## 6. Repositories

Every repository method scoped to a user takes `userID int64` **as its first argument after `ctx`**:

```go
func (r *TransactionRepo) ListByPeriod(ctx context.Context, userID int64, p Period) ([]Transaction, error)
```

- `userID` comes **only** from the session. Never from a body, query, path or header.
- Every SQL statement touching a user-scoped table filters on `user_id`, as the **first** predicate.
- Stage 01 adds `TestNoUnscopedQueries`, which scans `internal/store` and fails on any statement against a user-scoped table without a `user_id` predicate. It runs in CI from stage 01 onward.

## 7. SQL

- Hand-written, one file per aggregate, in `internal/store`. No ORM ([adr/0006](../adr/0006-sql-not-orm.md)).
- Parameters only. No string concatenation of input, ever.
- `STRICT` tables. The DDL in [03-data-model.md](../03-data-model.md) §3.5 is authoritative — migrations transcribe it, they do not reinterpret it.
- Timestamps are ISO-8601 UTC `TEXT`. Dates are `YYYY-MM-DD`. Periods are `YYYY-MM`.

## 8. Migrations

goose, `migrations/`, forward-only, numbered as in [03-data-model.md](../03-data-model.md) §3.7. Run explicitly, never at boot ([adr/0011](../adr/0011-explicit-migrations.md)).

- Every migration has a working `-- +goose Down`.
- CI runs `up → down → up` on a scratch database.
- Seed data (currencies, canonical categories, aliases) ships as migrations so a fresh install is usable immediately.

## 9. Testing

| Kind | Location | Rule |
| --- | --- | --- |
| Unit | beside the code | Pure functions, table-driven |
| Repository | `internal/store` | Real SQLite in a temp file, migrations applied |
| HTTP | `internal/transport/rest` | `httptest`, real router, real DB |
| Fixture | `testdata/` | Real CSV, real spreadsheet values |

- `go test -race ./...` must pass. Races are build failures.
- **No mocks for the database.** SQLite in a temp file is fast enough and catches real SQL errors.
- Every aggregate is tested for the **absent-period** case, not only the populated one.
- Test names state the behaviour: `TestImport_UnknownCategory_BlocksBatch`, not `TestImport2`.

## 10. Frontend

Vue 3 `<script setup>` + TypeScript, Pinia stores, Tailwind v4, shadcn-vue. Lives in `ui/`, built into `internal/transport/rest/assets/dist`, embedded with `embed.FS`.

- **Money is never a JS number in logic.** The API sends `{amount_minor, currency, exponent}`; a single `formatMoney()` helper renders it. Arithmetic on the client is forbidden — ask the server.
- API types are generated from `openapi.yaml`, never hand-written.
- One Pinia store per domain area.
- Components are dumb; stores hold state; no `fetch` inside a component.

## 11. Configuration

`config.yaml` in `/config`, environment overrides per key, **validated at boot** — the process exits non-zero on unusable config, naming the offending key. Secrets only from the environment; never a source-code default ([10-deployment-ci.md](../10-deployment-ci.md) §10.3).

## 12. Commands

Every stage's verification uses these. Stage 01 creates them.

```bash
make build          # ui + binary
make test           # go test -race ./... + vitest
make lint           # golangci-lint + eslint + prettier
make migrate-up     # goose up
make migrate-status
make run            # local, against ./tmp/config
make docker-build
make verify         # lint + test + migrate up/down/up  — the CI gate
```

## 13. Definition of done

A stage is done when **all** hold:

1. `make verify` passes on a clean checkout.
2. The stage's **MVP demo** can be performed against `docker compose up` by someone following only the stage file.
3. Every prior stage's demo still works.
4. New endpoints exist in `openapi.yaml` and the spec still validates.
5. Anything contradicting the spec docs is either fixed in code, or the spec is updated in the same commit — they never drift.
6. Both gates (import fidelity, Excel parity) are green once their stage has landed.

## 14. Commits

Conventional commits, scoped by stage: `feat(04): monefy csv parser`, `fix(03): huf exponent in quick entry`, `test(05): parity fixtures`.

One logical change per commit. A commit that changes a published contract also updates the stage file that depends on it.

## 15. What to do when the spec is wrong

The spec was written before the code. If a task turns out to be impossible or wrong:

1. **Stop. Do not silently improvise a different design.**
2. Write down what breaks and the smallest correct alternative.
3. If it contradicts an ADR, say which — those were decided with reasons ([adr/](../adr/)).
4. Update the spec doc and the stage file in the same commit as the code.

Silent divergence between docs and code is the failure mode this whole plan exists to avoid.
