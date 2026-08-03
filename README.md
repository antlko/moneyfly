# MoneyApp

Self-hosted budget and net-worth tracker. One Go binary with the Vue SPA embedded,
SQLite in a mounted volume, no sidecars, no CGO.

It replaces a *Monefy → Telegram bot → Google Sheets → hand-maintained Excel*
pipeline. The full design lives in [docs/](docs/); start with
[docs/README.md](docs/README.md).

**Built so far: all ten stages of [the plan](docs/implementation-plan/).** Log in, the
seeded chart of accounts and categories, three-tap expense entry, budgets,
plan-versus-actual with dated FX, the Monefy import — 1,683 real rows with an
unrecognised name blocking the batch rather than vanishing from it — the metrics
engine, and capital: net worth, allocation, runway and the month's change split
into real saving and currency movement. **Both halves of the workbook are
reproduced and parity-tested** (`make parity`). Rates now maintain themselves from
two free, keyless providers, behind a manual/auto setting that also covers
thresholds and prices. An opt-in Telegram bot relays an export from the phone into
the same import pipeline. It installs to a phone home screen, runs standalone and
opens offline with saving visibly disabled. The workbook's own history loads with
`migrate-excel`, backups run nightly and restore is drilled on every build — see
[docs/cutover.md](docs/cutover.md).

## Run it

```bash
export MONEYAPP_BOOTSTRAP_PASSWORD='pick-something-long'
docker compose up -d
docker compose run --rm moneyapp migrate up
docker compose restart moneyapp
open http://localhost:8080
```

Log in as `admin@example.com` with that password; the first thing it asks for is a new
one. Migrations are deliberately **not** run at boot — `/readyz` stays unhealthy until
the schema is current, so a container started against an unmigrated database refuses
traffic rather than writing against a schema it does not understand
([adr/0011](docs/adr/0011-explicit-migrations.md)).

Set `MONEYAPP_BASE_URL` to your real `https://` address in production: the session
cookie is marked `Secure` from it.

## Develop

```bash
make tools        # golangci-lint v2 into ./bin (the config uses the v2 schema)
make verify       # lint + test + migrate up/down/up — the CI gate
make run          # server on :8080 against ./tmp/config
make dev          # Vite dev server on :5173, proxying the API to :8080
```

`make help` lists the rest. Every stage's verification uses these targets, which are
specified in [docs/implementation-plan/00-conventions.md](docs/implementation-plan/00-conventions.md) §12.

## Layout

```
cmd/moneyapp        wiring only, no logic
internal/transport  HTTP handlers, DTOs, validation      — never imports store
internal/domain     business logic, pure where possible
internal/store      repositories and all SQL
internal/platform   Money, errors, logging, clock        — imports nothing above it
migrations/         goose, forward-only, embedded
ui/                 Vue 3 + TS + Pinia + Tailwind v4, built into the binary
docs/               the specification this implements
```

Three invariants worth knowing before changing anything:

- **Money is integer minor units**, never a float, and HUF has exponent 0
  ([adr/0004](docs/adr/0004-integer-money.md)). Exponents come from the `currency`
  table, never from a constant.
- **`NULL` means not recorded; `0` means recorded zero.** They are never
  interchangeable. This is the spreadsheet's `-1` sentinel bug, and
  `TestBudgetReport_UnrecordedIsNull` / `TestBudgetReport_RecordedZeroIsZero` exist to
  keep it from returning.
- **Every query against a user-owned table filters on `user_id`.** With no encryption
  at rest ([adr/0002](docs/adr/0002-no-encryption.md)) that predicate is the only
  thing separating two users' finances, so `TestNoUnscopedQueries` fails the build on
  a statement that omits it.

## Before deploying this anywhere real

The previous system left a live Telegram bot token and a Trading212 API key as source
constants in `monefy-budget-parser/main.go`, and they are in git history.
**Rotate the Telegram token and revoke the Trading212 key** — nothing here uses the
latter, so revoking outright is simpler than rotating. Rewriting history is not a
reliable fix.
