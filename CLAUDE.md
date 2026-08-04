# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**moneyfly** — "Self-hosted expense tracking that syncs across your devices." A full-stack app: a
**Go backend** (`backend/`) serves a **Vue 3 PWA** (`web-ui/`) plus a JSON API and the device sync
endpoints. In production the built SPA is embedded into a single static Go binary (one Docker
container). The phone UI is a deliberate clone of Monefy's interaction model — donut/list dashboard,
month carousel, two round record buttons, calculator keypad then category grid — because entering a
spend has to cost three taps. The same SPA widens into an analytics dashboard on desktop.

## Read this first

This file is the **index**: commands, invariants and gotchas. The deep detail lives in `docs/` —
follow the pointer instead of re-deriving behaviour from the code.

| Doc | Read it when you need |
| --- | --- |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Data ownership, request flow, background workers, where to make a change |
| **[docs/SYNC.md](docs/SYNC.md)** | **The op-log protocol, LWW resolution, bootstrap, conflict scenarios** — read before touching anything under `internal/sync` or `src/sync` |
| **[docs/MONEFY-PARITY.md](docs/MONEFY-PARITY.md)** | **The exact screen spec the mobile UI is measured against**, plus the real Monefy CSV format |
| [docs/API.md](docs/API.md) | Every endpoint + object shape |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | Every `config.yaml` field |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | Running both dev servers, building the single binary, testing the PWA on a phone |

**When you change behaviour, update the matching doc in the same change** — these files are the
contract future sessions rely on.

## Working directories

Two toolchains: run `npm` from `web-ui/`, run `go` from `backend/`. In dev they run side by side —
Vite proxies `/api` to the Go server on `:8080`, per `web-ui/vite.config.ts`.

## Commands

Everything has a `make` target; `make help` lists them. **Frontend** (from `web-ui/`): `npm run dev` ·
`npm run type-check` · `npm run lint` (oxlint→eslint, both `--fix`) · `npm run test` (Vitest) ·
`npm run build`. **Backend** (from `backend/`): `go run ./cmd/moneyfly --config-dir ../config` ·
`go vet ./...` · `go test ./...`. **Single binary**: `make build` (SPA build → copy into
`backend/internal/web/dist` → `CGO_ENABLED=0 go build`); the `Dockerfile` automates the same steps.

**Tests**: backend uses stdlib `testing` — table-driven, `httptest` for outbound senders, temp-file
SQLite per test; put new ones beside the package. On the frontend Vitest covers **`src/sync`,
`src/lib` and `src/db` only** (see `vitest.config.ts`) — the sync engine and money arithmetic are too
expensive to debug by hand. There is deliberately **no component test setup**: verify UI changes by
running the dev servers.

## Architecture

- **Source of truth split:** `config.yaml` holds instance infrastructure only (listen address, public
  URL, OIDC providers, FX chain, retention); **SQLite** (`moneyfly.db`) holds users, sessions,
  devices, every domain row, the `change_log` and integration secrets; **IndexedDB** on each device
  holds a full replica of that user's domain rows and is what the UI actually reads. Both server
  stores live in the config dir (`/config` in Docker; `MONEYFLY_CONFIG_DIR` / `--config-dir`,
  default `./config`).
- **Backend packages** (`backend/internal/`): `config` (load-only YAML + env overlay), `logger`
  (slog JSON), `api` (Fiber v3 app, central `ErrorHandler` rendering `{"error": ...}`, SPA
  catch-all over the embedded FS), `web` (`//go:embed all:dist`, exposes `web.FS()`). Entry:
  `cmd/moneyfly/main.go`.
- **Frontend:** Vue 3 `<script setup>`, Pinia stores, Dexie for IndexedDB, typed API client in
  `src/api/`. One route tree for both form factors — the shell component picks the mobile Monefy
  frame or the desktop dashboard, so resizing never changes the URL.

## Invariants (breaking these causes real bugs)

- **Balances are never stored, only derived.** An account balance is its opening balance plus the sum
  of its transactions. This is what lets sync get away with per-row last-write-wins instead of CRDT
  counters — concurrent edits conflict only when they touch the same row, and every aggregate is
  recomputed. Adding a stored running balance would silently break multi-device correctness.
- **Every sync operation is scoped by `user_id` taken from the session**, never from the request
  payload. An op naming a row that belongs to another user is rejected, not applied. Sync cursors are
  per `(user_id, device_id)` — never global.
- **Conflict resolution is `(lamport, device_id)`, compared identically on client and server.** Higher
  lamport wins; equal lamports break the tie on lexicographically greater `device_id`. Both sides must
  use the same rule or devices diverge. Re-applying an op must be a no-op.
- **Nothing in the sync write path may fail on data.** No foreign keys between synced tables, no
  unique constraints on anything a client sends. Ops arrive in any order (a transaction routinely
  lands before its category) and the same logical row can be pushed by two devices — a constraint
  failure would wedge that device into retrying forever. De-duplication is a query against an index
  (`idx_txn_natural_key`), never a constraint.
- **Deletes are tombstones (`deleted = 1`), never `DELETE`.** A hard delete cannot propagate.
- **A new device bootstraps from `/api/sync/snapshot`, never from `pull?since=0`.** The `change_log`
  is trimmed by retention, so `since=0` stops being complete the first time retention runs.
- **The UI never waits on the network.** A write lands in IndexedDB and re-renders immediately; the
  push queue is what talks to the server. Sync state is a status indicator, never a blocking spinner.
- **A 401 and an unreachable server are different answers.** 401 means the server said "not signed
  in" — clear the session. A transport failure means nothing about the session, so the app falls back
  to the profile cached in `meta.userProfile` and carries on. Conflating them puts a sign-in form in
  front of a ledger that is already on the device, with no way to get past it: the app becomes
  useless exactly when offline-first is supposed to earn its keep.
- **Transfers are one row** (`kind='transfer'` with `to_account_id`), not a linked pair — a pair could
  sync half-applied.
- **Money is integer minor units with a per-currency exponent.** HUF has 0 decimals and a real export
  contains it; never assume 2. The exponent table is `backend/internal/money/currency.go`, mirrored in
  `web-ui/src/lib/money.ts`.
- **Exchange rates are stored EUR-based only, and a lookup takes the nearest *earlier* date.** The
  inverse is computed and a cross rate goes through EUR, which makes it impossible to hold
  EUR→USD 1.14 and USD→EUR 0.88 at once. Never a later rate: a total computed for last March must not
  change because a rate arrived in April.
- **Conversion rounds half-away-from-zero exactly once, at the target exponent.** `fx.ConvertMinor`
  in Go and `convertMinor` in `web-ui/src/lib/fx.ts` are the same function written twice and must
  stay identical — the case tables in `fx_test.go` and `fx.test.ts` are mirrored on purpose. Rounding
  twice (convert, then re-scale) is how a column of figures stops adding up to its own total.
- **`fx_rate` is the one table on both sides that is not synced.** A rate is a fact about the world,
  not about a user: no `user_id`, no `data` JSON, no tombstone, and it never enters the op-log. It is
  fetched by the server from providers and pulled by clients over plain REST into their own Dexie
  table. That is also why it may carry real constraints — the "the sync write path must never fail on
  data" rule does not apply to a table no client op can reach.
- **Everything written to IndexedDB must be structured-cloneable.** A Pinia store hands out reactive
  Proxies, and `structuredClone` refuses them — this is what broke registration once. Normalisation
  happens at the write boundary (`db.setMeta`, `SyncEngine.record`) via `lib/plain.ts`, not at the
  call sites; `toRaw` is not enough, because it unwraps only the outer proxy.

## Conventions that bite

- **Icons: `@lucide/vue`**, NOT `lucide-vue-next` (deprecated). Some names changed in v1 (e.g.
  `Home`→`House`); verify via type-check.
- **Tailwind v4 resets buttons to `cursor: default`** — the base layer in `src/assets/tailwind.css`
  puts `cursor-pointer` back on `button` and `[role=button]`, along with `touch-action: manipulation`
  so keypad taps never double-tap-zoom.
- **Colours live only in `src/assets/tailwind.css`** as `@theme` tokens (`mf-*` for the shell,
  `cat-*` for the category palette). Never hardcode a hex in a component. Category rows store a
  palette *key* (`"rose"`), never a hex — otherwise a re-theme would have to rewrite synced rows on
  every device.
- **There is no raw config editor endpoint.** upmonitor has one; moneyfly must not, because
  `config.yaml` carries OIDC client secrets. `config` is load-only: no `Save`, no `Clone`, no
  copy-on-write path.
- **Defaults live in one place** — `internal/config` exports them (`DefaultCurrency`,
  `DefaultChangeLogRetentionDays`, …) precisely so API fallbacks cannot drift. Don't re-hardcode a
  default at a use site.
- **Fiber handlers** return `fiber.NewError(code, msg)` for errors (the central `errorHandler` renders
  `{"error": msg}` — the shape the frontend's `ApiError` parses); success via `c.JSON`.
- **Synced rows are JSON plus generated columns.** Every synced table stores the row body in a `data`
  TEXT column; the fields the *server* queries are SQLite VIRTUAL generated columns over it. So
  adding a field is a client-side change, and only a new server-side query needs a migration. Don't
  add typed columns to a synced table — you would then have two sources of truth for one value.
- **A pointer gesture must track which pointer is down and check that a button is held.** A mouse
  emits `pointermove` while merely hovering, so a handler that looks at coordinates alone treats
  crossing the window as a drag — and if it then takes `setPointerCapture`, it steals every click
  from its children. `SwipePager` and `BalancePill` show the shape: record `pointerdown`'s
  `pointerId`, ignore events without it, and bail when a mouse comes back with `buttons === 0`.
- **Overlays animate by wrapping the `v-if` in a `<Transition>` at the call site**, not by a keyframe
  inside the component: a `v-if` alone cannot animate a departure, and the departure is the half
  people notice. The named transitions (`mf-fade`, `mf-sheet`, `mf-drawer-l/r`, `mf-page`) live in
  `src/assets/tailwind.css` with `--mf-ease` / `--mf-duration`.
- **A template handler must be one expression.** `@click="a(); b()"` across two statements — or one
  statement carrying a TypeScript cast — does not compile, and **`vue-tsc` does not catch it**: only
  the dev server does, at request time, and the page renders blank. Put it in a named function.
- **Migrations**: add a new `backend/internal/db/migrations/NNNNN_name.sql` with `-- +goose Up`/`Down`;
  never edit an applied one. Dialect `sqlite3`. They are embedded and run inside `db.Open` on every
  start — there is no separate migrate command and the distroless image has no shell to run one.
- **`backend/internal/web/dist/index.html` is a tracked placeholder** so `//go:embed all:dist`
  compiles in a fresh checkout. `make build` and the Dockerfile replace the whole directory. Don't
  delete the placeholder and don't commit a real build over it.
- **PWA icons are generated**, not hand-drawn: edit the SVG in `web-ui/public/` and run
  `make icons`. The maskable variant is separately centred on the mark's *visual* centre (~300,260),
  not the canvas centre.
- **Logging** is slog JSON to stdout; prefer `slog.InfoContext(c.Context(), …)` with key/value attrs.
  Level via `MONEYFLY_LOG_LEVEL` (debug|info|warn|error).

## Legal boundary

moneyfly clones Monefy's **layout and interaction flow**, which is fair game, and nothing else. Do
not copy Monefy's drawn category icons, its script wordmark or its name into this repo. Category
icons come from Lucide; the wordmark and app icon are our own (`web-ui/public/icon.svg`).
