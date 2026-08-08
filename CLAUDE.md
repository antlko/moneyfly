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
Vite proxies `/api` to the Go server on `:5007`, per `web-ui/vite.config.ts`.

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
- **Backend packages** (`backend/internal/`): `config` (YAML + env overlay; writes a fully-defaulted
  file on first boot, and `UpdateSettings` persists the `app`/`sync`/`fx` subset — never
  `server`/`oidc`), `logger`
  (slog JSON), `api` (Fiber v3 app, central `ErrorHandler` rendering `{"error": ...}`, SPA
  catch-all over the embedded FS), `web` (`//go:embed all:dist`, exposes `web.FS()`). Entry:
  `cmd/moneyfly/main.go`.
- **Frontend:** Vue 3 `<script setup>`, Pinia stores, Dexie for IndexedDB, typed API client in
  `src/api/`. One route tree for both form factors — `useIsDesktop()` (`src/lib/breakpoint.ts`,
  640px, mirrored in a `tailwind.css` media query) is read in exactly two places: `App.vue` wraps
  every route in `DesktopShell.vue` (sidebar nav, persistent) at that width, and `DashboardView.vue`
  separately swaps its own content for `DesktopDashboardContent.vue` — the only screen genuinely
  redesigned for desktop, since it is the only one the mobile layout (donut, swipe paging) does not
  already suit. Every other screen renders its existing mobile component unmodified inside
  `DesktopShell`'s content area; resizing never changes the URL either way.

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
- **There is no raw config editor endpoint, and there still must never be one.** upmonitor has
  one; moneyfly must not, because `config.yaml` can carry OIDC client secrets. What exists instead
  is a narrower admin-only settings API (`GET`/`PUT /api/admin/settings`, `handlers_settings.go`)
  scoped to exactly `config.Settings` — `app`, `sync`, `fx` — and nothing else. `server.*` and
  `oidc.*` stay config.yaml/env-only, by construction: `config.UpdateSettings` re-reads them fresh
  from disk before every write rather than round-tripping whatever the in-memory `Config` holds, so
  an OIDC secret pulled in from the environment can never end up back in the file. A fresh instance
  also gets a fully-populated `config.yaml` written on first boot (`config.Load`), not just an
  in-memory default, so there is something on disk to hand-edit for the fields that stay
  hand-edit-only.
- **Defaults live in one place** — `internal/config` exports them (`DefaultCurrency`,
  `DefaultChangeLogRetentionDays`, …) precisely so API fallbacks cannot drift. Don't re-hardcode a
  default at a use site.
- **A config field that defaults to *on* must be a `*bool`.** A plain bool cannot tell "absent" from
  "false", and its zero value is the wrong answer: `fx.enabled` was one, so an instance with no
  `config.yaml` — the documented bare `docker run` — silently never fetched a rate, and every
  foreign-currency record sat outside every total under a "no exchange rate yet" caption. `normalize()`
  fills the pointer, and callers ask `cfg.FX.On()`.
- **Fiber handlers** return `fiber.NewError(code, msg)` for errors (the central `errorHandler` renders
  `{"error": msg}` — the shape the frontend's `ApiError` parses); success via `c.JSON`.
- **Synced rows are JSON plus generated columns.** Every synced table stores the row body in a `data`
  TEXT column; the fields the *server* queries are SQLite VIRTUAL generated columns over it. So
  adding a field is a client-side change, and only a new server-side query needs a migration. Don't
  add typed columns to a synced table — you would then have two sources of truth for one value.
- **A background worker that writes synced rows on someone's behalf uses `deviceId = "server"`**, a
  constant with no matching row in `devices` — never a real device's id. It only has to out-rank
  nothing: a brand-new row starts at lamport 1, and an existing row's own advance is
  `stored lamport + 1`, so a person's own concurrent edit — at their device's already-higher clock —
  wins outright on lamport alone before `device_id` ever breaks a tie. `internal/api/recurring.go` is
  the one caller today; the next background writer (an importer, a webhook processor) should reuse the
  same constant rather than invent another sentinel.
- **Adding calendar months or years to a date must clamp the day, not let it overflow.**
  `time.AddDate(0, 1, 0)` on 31 January lands on 3 March, because Go normalises an out-of-range day
  forward instead of stopping at the month's end — `new Date(y, m + 1, d)` sets the identical trap in
  JavaScript. `addMonthsClamped` in `backend/internal/api/recurring.go` and `nextOccurrence` in
  `web-ui/src/lib/period.ts` both clamp to the target month's last real day instead, independently, the
  same way the FX arithmetic is mirrored rather than shared. Anything that recurs monthly or yearly
  needs this, not only recurring records.
- **Any outbound request to a user-supplied URL must check the resolved address, not the hostname,
  and must check it at request time, not only when the URL was saved.** Webhooks
  (`internal/api/webhooks.go`) are the one caller today: the dial function itself refuses a private,
  loopback or link-local address, checked against what DNS resolves to for *that* request. Checking
  the hostname once, at creation, is exactly what a DNS-rebinding attack defeats — a name that
  resolves to a public address during validation and a private one once it is trusted — and on a
  multi-user instance the person entering the URL is not necessarily the operator, so this is not a
  theoretical caller. Anything else that ever fetches a user-supplied URL should dial through the
  same checked client rather than a bare `http.Client`.
- **A pointer gesture must track which pointer is down and check that a button is held.** A mouse
  emits `pointermove` while merely hovering, so a handler that looks at coordinates alone treats
  crossing the window as a drag — and if it then takes `setPointerCapture`, it steals every click
  from its children. `SwipePager` and `BalancePill` show the shape: record `pointerdown`'s
  `pointerId`, ignore events without it, and bail when a mouse comes back with `buttons === 0`.
- **The mobile shell is `position: fixed; inset: 0`, not `height: 100%`.** On iOS `100%` resolves
  against the *large* viewport, so the document is taller than the screen and the whole app scrolls —
  header under the clock, record buttons off the bottom, every vertical gesture fighting a page
  scroll. See docs/MONEFY-PARITY.md §"The app frame" for the rest of the PWA rules that go with it
  (`black-translucent` status bar, no pinch-zoom, no text selection under `pointer: coarse`, and
  putting the shell back after the keyboard closes).
- **At the desktop breakpoint, `#app` gets an explicit `height: 100vh`, not `min-height`.** Every
  mobile screen reused as-is on desktop (everything except the dashboard, per `DesktopShell`) still
  has `h-full` on its root, and a percentage height only resolves against an ancestor whose own
  height is *definite* — `min-height` alone does not make one, so a child asking for `height: 100%`
  inside a `min-height`-only ancestor can silently collapse. `html`/`body` stay `height: auto` so the
  document can still grow past one screen for the dashboard's own longer content; only `#app` needs
  the fixed number, and its `overflow` is left alone so taller content is not clipped, just pushes
  the document taller.
- **`showPicker()` on a hidden `<input type="date">` must be called from a click on that same input,
  not from a wrapping `<button>` around it.** A real iPhone enforces same-element activation for this
  and refuses it otherwise — no error, nothing opens, indistinguishable from the control being dead.
  Desktop Chrome/Firefox don't enforce this (any click works), which is exactly how the wrapping-button
  shape passed every check that wasn't on real iOS hardware. `DateRow` and `FilterDrawer`'s "Choose
  date" put the real `<input>` on top (`absolute inset-0`, real `pointer-events`), covering the whole
  row, with the click handler on the input itself; the icon/label/chevron underneath are
  `pointer-events-none` decoration. Also listen on both `input` and `change` — Android's full-screen
  calendar dialog doesn't reliably raise `input` when its OK button commits.
- **`inset-0` does not size a replaced element** (`<input>`, `<img>`, `<video>`) **the way it sizes a
  `<div>`.** An absolutely positioned `<div>` with `inset-0` fills its positioned ancestor; an
  `<input>` can keep its own intrinsic width regardless. Add `h-full w-full` explicitly wherever a
  replaced element needs to actually cover its container — the two date inputs above both need it.
- **`:active` is dead on iOS until the document has a touch listener.** Safari applies it only if one
  is registered somewhere, so every pressed state in the app — keypad, record buttons, account rows —
  silently did nothing on an iPhone, which reads as taps not registering. `main.ts` registers an empty
  passive `touchstart` listener for exactly this and nothing else; don't remove it.
- **Nothing may still be moving where a finger is about to land.** The browser hit-tests where an
  element *is*, not where the frame you reacted to drew it, so content that animates into place eats
  the first tap aimed at it. `SwipePager` carries the old period out but brings the new one up
  *at rest*, fading rather than sliding, for this reason — a sliding arrival made every button need
  two taps for the length of the animation.
- **Never give `html` or `body` a `touch-action` value, not even to block pinch-zoom.** Declaring any
  panning value on the document root puts WebKit's gesture recognizer in a state where the tap right
  after a swipe — anywhere in the app — is consumed as "stop the pan" instead of delivered as a click,
  so every button needed two taps immediately after paging the month carousel. Zoom is disabled at the
  viewport instead (`maximum-scale=1, user-scalable=no` in index.html), which never touches touch
  gesture recognition. `touch-action` stays fine on individual elements that actually drag
  (`SwipePager`, sheet handles) — the trap is specifically the document root.
- **…and it must not believe every `lostpointercapture` it sees.** A *touch* pointer is implicitly
  captured by whatever element it lands on, so calling `setPointerCapture` on an ancestor revokes
  that — and the descendant's `lostpointercapture` **bubbles** straight into the ancestor's own
  handler, one event after the drag begins. Treating it as a cancellation made every swipe cancel
  itself on the frame it started, on touch only: a mouse takes no implicit capture, so it worked
  perfectly on a desktop and did nothing on a phone or in Chrome's device toolbar. Check
  `e.target === root` (`SwipePager.onLostCapture`).
- **A nil Go slice marshals to `null`, and the client's type says it is an array.** `rejected` on the
  push response is the case that shipped: `res.rejected.length` threw a `TypeError` on every
  *successful* push, the engine classified `TypeError` as a transport failure, and the app reported
  itself offline while every request returned 200. Two rules came out of it — a response slice is
  initialised, never nil (`api.rejections`), and only the HTTP layer decides what "offline" means, by
  wrapping a rejected `fetch` in `NetworkError` (`api/http.ts`). Never classify a network failure by
  error *type* further up: `TypeError` is also what a plain bug produces.
- **Every request through `api/http.ts` carries a 15s `AbortSignal.timeout`.** Without it, a `fetch`
  that never gets a response — a captive portal, a proxy that silently drops the connection — simply
  never resolves, and `auth.bootstrap()` awaits one in the router guard before rendering anything: an
  installed PWA opened on a bad connection would sit on a blank screen for as long as the browser's
  own connection timeout, which can be minutes, instead of reaching the offline fallback that already
  exists for exactly this (`meta.userProfile`, `stores/auth.ts`). The abort surfaces as the same
  `NetworkError` a dropped connection would, so nothing downstream needs to know the difference. 15s
  rather than something tighter because the CSV import commit parses and resolves thousands of rows
  server-side inside one request — a timeout tuned for a typical small JSON payload would fire on the
  one call size actually matters for.
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
