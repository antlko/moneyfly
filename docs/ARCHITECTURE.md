# Architecture & behaviour

How moneyfly behaves at runtime: what owns which data, how a request flows, and where to make a
change. Companion docs: [SYNC.md](SYNC.md) (the sync protocol — the heart of the app),
[MONEFY-PARITY.md](MONEFY-PARITY.md) (the UI contract), [CONFIGURATION.md](CONFIGURATION.md),
[API.md](API.md), [DEVELOPMENT.md](DEVELOPMENT.md).

> Sections marked *(planned)* describe design decided in the plan but not yet built. They are here
> so the next change lands in the right place, not to describe code that exists.

---

## 1. Where data lives

| Store | Path | Holds | Written by |
| --- | --- | --- | --- |
| `config.yaml` | `<config-dir>/config.yaml` | listen address, public URL, OIDC providers, FX chain, retention | a human, in an editor, **or** an admin from Settings → Instance for the `app`/`sync`/`fx` sections only |
| SQLite | `<config-dir>/moneyfly.db` | users, identities, sessions, devices, **every domain row**, `change_log`, FX rates, integrations **and their secrets** | `internal/db` |
| IndexedDB | each browser profile | a full replica of that user's domain rows, the push queue, the sync cursor, **plus a cache of exchange rates** | `web-ui/src/db`, driven by `web-ui/src/sync` and `web-ui/src/stores/fx.ts` |

The split rule inherited from upmonitor still holds — hand-editable configuration goes in YAML;
history, volume and secrets go in SQLite — but moneyfly lands almost everything in SQLite, because
almost everything syncs. `internal/config` is not fully load-only any more: `config.UpdateSettings`
(`GET`/`PUT /api/admin/settings`, admin-only) can change and persist the `app`/`sync`/`fx` sections
at runtime. What still holds, on purpose, is that there is no endpoint that returns or accepts the
*whole* file — `server.*` (a running process cannot rebind its own listen address) and `oidc.*`
(can carry a client secret) are never read or written by that path. `UpdateSettings` re-reads those
two sections fresh from disk before every write rather than round-tripping the in-memory `Config`,
so an OIDC secret pulled in from the environment can never end up written back to the file.

**The client, not the server, is the UI's data source.** Screens read IndexedDB. The API is used for
authentication, sync, and the operations that genuinely need a server (import, export, integrations,
FX). This is what makes the app work in airplane mode, and it is the single most important thing to
keep in mind when adding a feature: "fetch it from the API on mount" is almost always the wrong
shape here.

## 2. Request flow

```
main.go → api.New(configDir) → config.Load → fiber.New
                                              ├── recover
                                              ├── requestLogger
                                              ├── compress (skips the SSE route)
                                              ├── /api/... handlers
                                              └── serveSPA (catch-all, last)
```

`serveSPA` reads from the embedded FS (`internal/web`, `//go:embed all:dist`) and falls back to
`index.html` for unknown paths so client-side routes work on a hard refresh. Unmatched `/api/*`
paths return a JSON 404 instead of HTML — a typo'd endpoint should never hand the client a page it
would try to parse as JSON.

**Compression covers everything except `/api/sync/events`.** Fiber does not compress by default, and
the SPA's critical path is ~356 KB of JS and CSS that gzips to ~121 KB — a ~2.9× cut paid back on
every cold load, plus the same saving on `/api/sync/snapshot` and `/api/sync/pull`, which are the
largest JSON the app ever moves. The event stream is the one exclusion and it is not optional: SSE is
a response that never ends, delivered a few bytes at a time, and a compressor buffers those bytes
until it has something worth compressing — so events stop arriving when the server sent them. Nothing
errors; devices simply look offline while the server answers perfectly well. The skip is matched on
the request path (the response content type is not known when the middleware runs), and the route is
registered through the same `eventStreamPath` constant the skip tests against so the two cannot
drift.

Errors: handlers return `fiber.NewError(code, msg)`; the central `errorHandler` renders every one as
`{"error": msg}`, which is the only shape `ApiError` on the client parses. Unexpected (non-Fiber)
errors are logged in detail there — the real Go error value, not just its rendered message — because
a 500 from something no handler anticipated is exactly the case an operator needs the most context on.

**Every request also gets one summary line from `requestLogger`, at a level keyed to what happened**:
Debug for 2xx/3xx, Warn for 4xx, Error for 5xx. This matters because `MONEYFLY_LOG_LEVEL` defaults to
`info` — before the split, *every* request logged at Debug regardless of outcome, so a stock
deployment recorded nothing at all for a rejected request: no line, no status, no error, nothing. That
read as broken logging when it was working exactly as configured, just not logging what anyone needed
to see. The rejected-request line carries the same message the client received (pulled from the
`{"error": …}` body's would-be contents, not read back off the response — see `requestLogger`'s own
doc comment for why it has to be computed from the returned error, since `errorHandler` itself has not
rendered anything yet by the time `requestLogger`'s post-`c.Next()` code runs) plus the request's byte
size. That size is the diagnostic: if a request the client genuinely made produces *no* line here, not
even at Debug, it never reached this process — something in front of it (reverse proxy, tunnel, WAF)
answered first, which this middleware has no way to see or log.

## 3. Background workers

| Worker | Cadence | Job | Status |
| --- | --- | --- | --- |
| retention | hourly | drop expired sessions and abandoned OIDC states; trim `change_log` past `sync.change_log_retention_days` | built |
| FX refresh | daily at `fx.refresh_at` | walk the provider chain, store the day's rates | built |
| recurring | hourly | materialise due `recurring_rule` rows, idempotent on `(rule_id, occurred_on)` so a restart cannot double-post | built |

Trimming the journal costs a long-absent device a full re-bootstrap and never costs anyone data —
the domain rows are untouched.

## 4. Identity

Multi-user: several independent people on one instance, data isolated by `user_id`. Sign-in is
email + password (argon2id), any configured OIDC provider, or a passkey. Sessions are opaque 256-bit
tokens in an HttpOnly, SameSite=Lax cookie, stored **hashed** — a database leak must not yield a
working cookie.

**There is no separate setup screen.** Registration is allowed when the instance is configured
`open` *or* when no account exists yet. That second clause is the whole of "claim this instance": an
operator can ship `registration: closed` and the first visit still gets to create the admin account.
`GET /api/health` exposes both `registrationAllowed` and `claimed` so the sign-in screen can tell
"brand new instance" from "open instance that already has users".

### How an OIDC sign-in resolves to an account

In order — `handleOIDCCallback` in `internal/api/handlers_oidc.go`:

1. the `(provider, subject)` pair is already linked → sign that account in;
2. the flow was started with `link=1` by someone already signed in → attach the provider to them;
3. registration is allowed → create a new account from the claimed email;
4. otherwise → refuse.

**Step 3 deliberately does not adopt an existing account with the same email.** Auto-linking on a
claimed email is an account-takeover route whenever the provider does not verify emails, so instead
the user is told to sign in with their password and link from settings. This is the one place where
the obvious behaviour is the wrong one.

Unlinking refuses to remove the last way in (`db.ErrLastSignInMethod`): an account with no password,
no identities and no passkeys is unreachable, and a self-hosted instance has no support desk. That
guard is `countSignInMethods` in `internal/db/identities.go`, and it has to span all three tables at
once — two independent checks, each seeing only its own, would each let the account's last way in go
while the other looked safe.

### How a passkey sign-in resolves to an account

Nothing like the OIDC ladder above, and deliberately so: **there is no create-an-account path**. A
passkey is added from Account settings by someone already signed in, so registration answers "which
account?" before the ceremony starts, and sign-in has only one case —

1. the assertion carries a user handle, which *is* the account id → sign that account in.

The handle is `db.User.ID`'s raw UUID bytes (`webAuthnUser.WebAuthnID`), so resolving it is a plain
`UserByID` — never an email, never a claim from a third party. That is why none of step 3's
email-collision caution applies here: this instance issued the credential, and a credential it did
not issue names nothing.

Sign-in is discoverable ("usernameless"): no email is collected and no credential list is returned,
so `/api/auth/webauthn/login/options` answers identically whether or not any account exists.

The relying-party id is derived **once at startup** from `server.base_url` (`auth.NewWebAuthn`),
never per-request. `RPOrigins` is the allowlist the library checks the browser-asserted origin
against, so building it from an incoming request's own Host would compare a value with itself; and a
credential is permanently bound to the id it was created under, so an id that varied by request would
strand every passkey the moment it changed. No public URL configured means no relying party, which
means `webauthnEnabled: false` and no passkey button — password and OIDC sign-in are unaffected.

### Root manages the others

The first account is always the admin (`db.CreateUser`), and `/api/admin/*`
(`internal/api/handlers_admin.go`) is how they run the instance for everyone else on it: list every
account, provision one directly — bypassing `registration` entirely, since an admin adding someone
is not the public signing up — promote, demote, or delete. See [API.md](API.md) "Admin" for the
routes.

**Every action that could leave an instance with zero admins is refused** (`db.ErrLastAdmin`): both
deleting the last admin and demoting them. The same shape as `ErrLastSignInMethod` above, for the
same reason — there is no support desk to recover a self-hosted instance from that.

**Self-delete is refused before the request ever reaches that check.** Managing your own account
from the same screen that manages everyone else's is a different, more consequential action than
Settings — confusing enough to just not offer, since there is nothing here you cannot already do to
your own account elsewhere. One consequence worth knowing before touching this code: the
last-admin-delete path is consequently unreachable through the API by construction. Reaching
`handleAdminDeleteUser` at all requires the caller to be an admin; if the target is also "the last
admin," the caller can only be that same account, which the self-delete guard already refused for a
clearer reason. `DeleteUser`'s own check still exists as a db-layer safety net for any future caller
without the same guard, and is exercised directly at that layer
(`TestDeleteUserRefusesTheLastAdmin`) rather than through a route that cannot reach it. Demoting has
no such guard — stepping down when someone else already holds admin is reasonable — so it is the one
where the rule is actually reachable over HTTP: the sole admin demoting themselves.

## 4a. Running with no network

Three separate pieces have to hold for an installed app to open in a tunnel, and each fails
differently:

| Piece | Without it |
| --- | --- |
| **Service worker** precaching the shell (`vite-plugin-pwa`, `web-ui/vite.config.ts`) | The launcher icon shows the browser's offline page — nothing runs at all |
| **IndexedDB replica + push queue** (`src/db`, `src/sync`) | The app loads and is empty |
| **Cached profile** (`meta.userProfile`) | The app loads with the data present, and shows a sign-in form nobody can complete |

The service worker caches the **shell only**. API responses are never cached: the sync engine has to
see a real transport failure to know it is offline, and a stale 200 from a cache would leave it
believing it had synced.

`navigator.storage.persist()` is requested at startup. Safari evicts IndexedDB after roughly a week
of not using a site; eviction costs no *data* — the server has it all and the device re-bootstraps —
but it does cost the **unsent queue**, which exists nowhere else.

## 4a-bis. How much fits

The dashboard holds **every** live transaction in memory — one `liveQuery` over the whole `txn`
table, with each period sliced out of it by a string comparison on the date. Not a query per period:
paging the carousel used to tear down a Dexie subscription and build another, so every swipe went
back to the database for data the device already had in full, and the screen it landed on arrived a
frame or two late.

That is only reasonable because the numbers are small, so here they are.

| | per row | 10k rows | 100k rows |
| --- | --- | --- | --- |
| stored in IndexedDB (JSON + indexes) | ~300 B | ~3 MB | ~30 MB |
| held as JS objects | ~1 KB | ~10 MB | ~100 MB |

For scale: the real Monefy export this project is tested against is **1,683 rows for two and a half
years** of daily spending. Ten thousand rows is roughly fifteen years of it; a hundred thousand is
not a personal ledger.

The ceilings, none of which these come close to:

* **IndexedDB quota** — Chrome allows an origin up to about 60% of free disk; Safari grants ~1 GB and
  more on request. `navigator.storage.persist()` is called at startup (`main.ts`), which is what stops
  Safari evicting the replica after a week of not opening the app. Eviction costs no data — the
  server has everything and the device re-bootstraps — but it does cost the *unsent* queue.
* **The array itself** — an engine handles a hundred thousand small objects without complaint. The
  cost that would bite first is re-deriving the totals on every change, and those are computed
  properties over one filtered slice, not over the whole table.
* **The snapshot request** — a new device bootstraps from `/api/sync/snapshot` in one response.
  That is the one place a very large ledger would be felt, and it is once per device.

If a ledger ever did grow past this, the fix is not to go back to per-period queries: it is to bound
what is held to a window of years around the visible period, keeping the slice synchronous.

## 4b. Exchange rates

Rates are the one thing in the app that is neither configuration nor synced user data, and they are
modelled accordingly.

**Storage is EUR-based, one direction only.** `fx_rate` holds `EUR -> X` and nothing else.
`X -> EUR` is the computed inverse; `USD -> HUF` is a cross rate through EUR whose date is the
*stalest* of the two legs. This removes by construction the failure where a database holds
EUR→USD 1.14 and USD→EUR 0.88 and quietly disagrees with itself.

**A lookup takes the exact date, else the nearest earlier one — never a later one.** A total
computed for last March must not change because a rate arrived in April. Weekends and holidays leave
gaps, so "earlier" is the normal case, not the exception.

**Within one date, a hand-entered rate beats a fetched one, and that ordering is written once.**
`db.ratePrecedence` is the whole rule — later date, then `source = 'manual'`, then the later write —
and `RateOn`, `Latest` and `History` all order by it. They used to each phrase it slightly
differently: ordering on `id` alone made an override depend on whether the daily refresh happened to
run after the person typed their rate, and `Latest` grouped on `MAX(id)`, so entering a rate for an
older date made that date look like the newest one everywhere.

**`fx_rate` is not a synced table.** No `user_id`, no `data` JSON, no lamport, no tombstone. A rate
is a fact about the world, so replicating it through a private ordered log would buy nothing. The
server fetches it from providers; clients pull it over plain REST into a Dexie table of their own.
That table is the one exception to "IndexedDB is a replica of synced rows", and it is worth knowing
about before adding a second one.

**Conversion happens on the client, from that cache, synchronously.** The rule from §1 applies
here too: a total that needs a round trip is a total that disappears on the underground.
`web-ui/src/lib/fx.ts` and `backend/internal/fx/fx.go` are therefore the same arithmetic written
twice — rounding half-away-from-zero exactly once at the target exponent — with mirrored case tables
in both test suites. This is the same deliberate duplication as the LWW rule, for the same reason.

**Nothing in a request path waits on a provider.** The refresh worker is the only caller. A provider
that fails is skipped for the next in `fx.providers`; a total outage keeps yesterday's rates and logs
it. A single-day move over 15% is rejected and the previous rate kept, because a broken feed and a
real currency event look identical on screen, and the wrong one corrupts every derived figure.

What a provider must publish to be accepted is derived from the data (`db.UsedCurrencies`), not
hardcoded: adding a forint account is what makes this instance start insisting on a forint rate.

## 4c. Recurring records

A `recurring_rule` row carries the same fields a transaction does (kind, amount, currency, category,
account) plus `freq` and `nextOn` in place of `occurredOn`. The worker (`internal/api/recurring.go`)
finds every rule whose `nextOn` is due, across every user, and turns each one into an ordinary `txn`
row through `DB.ApplyOps` — the same entry point `POST /api/sync/push` uses. A materialised
transaction is therefore indistinguishable from one a person typed in; it reaches every device the
same way, over the same op-log.

**A rule behind by several periods catches up fully, one transaction per missed occurrence, rather
than skipping to today.** The worker is not scheduled tightly — an hourly tick, no wall-clock
anchor — so this is the normal path after any outage, not an edge case: those were real days of
spending and the ledger says so.

**Idempotence is the transaction's `naturalKey` (`recurring:<ruleId>:<occurredOn>`), checked before
every insert — the same structural de-duplication `idx_txn_natural_key` exists for (docs/SYNC.md),
not a constraint.** This is what makes a crash between "the transaction was created" and "`next_on`
was advanced" safe to retry: the next tick rebuilds the same candidate set, finds the natural keys
already present, and only advances the rule. A deleted materialised transaction is not recreated
either — the key is checked regardless of the tombstone, because a person removing one is intent, not
an accident to correct.

**Server-authored ops use `deviceId = "server"`**, a constant with no corresponding row in `devices` —
it exists purely to participate in the Lamport tiebreak like any device id would. In practice it never
needs to: a materialised transaction starts at lamport 1 and a rule's advance is `stored lamport + 1`,
so a person's own edit — at their device's already-higher clock — wins outright on lamport alone, the
ordinary case last-write-wins is built around.

**Monthly and yearly clamp to the last real day of the target month rather than overflow into the
month after.** `time.AddDate` in Go (and a hand-rolled equivalent in `lib/period.ts`, for the "make
recurring" sheet's preview only — the worker's own copy is what actually governs what gets posted)
normalises an out-of-range day forward: 31 January plus one month is 3 March, not 28 February. A rule
for "the 31st of every month" silently drifting to "the 3rd" a few months later is the kind of bug
that is only ever noticed once the totals stop matching a bank statement.

## 4d. Integrations: tokens and webhooks

`api_token` and `webhook` (migration `00004_integrations.sql`) are the other tables in the
"integrations and their secrets" row of §1 — neither is synced, for the same reason `fx_rate` is
not: a token or a webhook secret is not a domain row replicated to every device, it is a fact about
how *this account* is reached from outside, held once, in SQLite.

**A webhook's URL is checked against private, loopback and link-local address ranges on every
delivery, not only when it is created.** The check runs inside the HTTP client's own dial function
(`internal/api/webhooks.go`), against the address DNS actually resolved to for *this* request — a
webhook URL is entered by whoever is signed in, which on a multi-user instance is not necessarily
the operator, and the server is what makes the outbound request. Checking a hostname once, at
creation, is exactly what a DNS-rebinding attack defeats: resolve to a public address during
validation, a private one once it is trusted. Checked at dial time, on the resolved address, closes
that gap.

**Only the ordinary push path fires webhooks — not the recurring worker, not the CSV importer.**
Both of those write in bursts (a year of missed occurrences catching up at once; a few thousand
imported rows), and a webhook subscriber almost certainly wants "a person just recorded something,"
not one HTTP delivery per row of a bulk operation. If a use case for the other two shows up, the
payload shape (`buildWebhookPayload`) does not need to change — only where it is called from.

**Delivery uses `ApplyResult.Applied`, never the request's own op list.** An op that loses its
last-write-wins comparison is still a perfectly well-formed op; filtering the input down to
"whatever was not in `rejected`" would still report a transaction that never actually landed.
`Applied` is the subset that genuinely won and was written — see the field's own doc in
`internal/db/sync.go`.

## 4e. The desktop shell

One reactive switch, `useIsDesktop()` (`web-ui/src/lib/breakpoint.ts`, `min-width: 640px`, mirrored
in a `tailwind.css` media query so the CSS and the JS agree on the same number without importing
from each other), read in exactly two places:

- **`App.vue`** wraps every route in `DesktopShell.vue` — a persistent sidebar (nav, the two record
  shortcuts, sync status) — once, at the top, rather than each screen growing its own copy. Signed-out
  routes (`meta.public`: sign-in, the 404) are excluded even at desktop width: the sidebar links to
  screens nobody can reach yet and would show an account that does not exist.
- **`DashboardView.vue`** separately swaps its own body for `DesktopDashboardContent.vue`. This is
  the *only* screen genuinely redesigned for the width — a period selector, summary cards, a category
  breakdown, account balances, a recent-records table — because it is the only one whose mobile layout
  (donut chart, swipe-paged carousel) does not already work at any width.

Every other route (Accounts, Budgets, the record screen, …) still renders its **existing** component
— same template, same logic — inside `DesktopShell`'s content area, deliberately never a second
version built for the width. Building one would be a second place for every future field to be
added. Two small, shared adjustments carry the rest of the way, both keyed off the same
`useIsDesktop()`:

- **`ScreenHeader.vue`** — the title bar roughly ten screens already shared — drops the green
  background and the back chevron at desktop width instead of every screen growing its own
  variant. No chevron because `DesktopShell`'s sidebar is always visible there; "back" has no
  meaning a highlighted nav link does not already give, unlike on the phone where a screen can be
  the only way in. Each screen's own header-action buttons (a `+`, a refresh icon) are left
  unstyled for colour on purpose, so the same markup reads white on the green mobile bar and
  `mf-ink` on the plain desktop one with no second version of the button either.
- **A `sm:max-w-2xl` (or `sm:max-w-md` for the narrower record/transfer screens) on each screen's
  own content wrapper** keeps a short list or form from stretching edge-to-edge across a wide
  content pane. This is the one per-screen touch — a single class on the existing root element, not
  a restructure — because the *right* width differs by content (a table wants more room than a
  settings form), so one blanket rule on `DesktopShell` itself would be wrong for some screen either
  way.

Both switches read data from the stores the mobile screens already use — `dashboard.ts`,
`accounts.ts`, `taxonomy.ts` — never a separate desktop computation. A total that disagreed between
the phone and the desktop dashboard would be far worse than the desktop one looking plain.

**`#app` needs an explicit `height: 100vh` at this width, not `min-height`** — see the entry in
CLAUDE.md's "Conventions that bite" before touching this rule; the short version is that a
percentage `height` (`h-full`, which every reused mobile screen's root still has) does not resolve
against a `min-height`-only ancestor, only a definite one.

## 5. Where to make a change

| Change | Go here |
| --- | --- |
| A new API endpoint | `internal/api/handlers_*.go`, register it in `Server.routes` |
| A new persisted field | a new goose migration + the entity's file in `internal/db` + the sync payload + the Dexie schema |
| Anything about conflict handling | `internal/sync` and `web-ui/src/sync` — **and [SYNC.md](SYNC.md) in the same change** |
| A screen's layout | `web-ui/src/components/monefy/*` — and check it against [MONEFY-PARITY.md](MONEFY-PARITY.md). This is the mobile layout only; the desktop dashboard has no reference screenshot to match against, since it is not a Monefy likeness at all. |
| The desktop dashboard's own content | `web-ui/src/components/desktop/DesktopDashboardContent.vue` — reads the same stores the mobile dashboard does (`dashboard.ts`, `accounts.ts`, `taxonomy.ts`); it is a second view of the same computed data, never a second calculation of it |
| The desktop sidebar or nav | `web-ui/src/components/desktop/DesktopShell.vue`, mounted once in `App.vue` around every route |
| A colour | `web-ui/src/assets/tailwind.css` `@theme` block, nowhere else |
| Anything about currency conversion | `internal/fx` and `web-ui/src/lib/fx.ts` — **both, with their mirrored test tables** |
| An overlay's animation | the named transitions in `web-ui/src/assets/tailwind.css`, applied by wrapping the `v-if` at the call site |
| A new export column shape | one `internal/exporter/profile_*.go` implementing `Profile`, self-registered in `init()` — `Get`/`IDs` resolve it by a string id, same shape as the FX provider chain |
| Monefy CSV parsing or category/account matching | `internal/importer` — pure and DB-agnostic, see its tests before `internal/api/handlers_import.go`'s |
| A config field | `internal/config/config.go` (struct + `normalize` + `Validate`), `config.example.yaml`, [CONFIGURATION.md](CONFIGURATION.md) |

## 6. Deliberate non-goals

- **No native app.** The PWA installs from the home screen on both platforms. A TWA wrapper adds
  nothing while that holds.
- **No stored balances.** See [SYNC.md](SYNC.md) §2 — this is load-bearing for sync correctness, not
  a style preference.
- **No raw-config endpoint.** upmonitor has one; here the file holds OIDC secrets.
- **No third-party cloud sync.** The server *is* the sync backend; that is the product.
