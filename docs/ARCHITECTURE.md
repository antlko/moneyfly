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
| `config.yaml` | `<config-dir>/config.yaml` | listen address, public URL, OIDC providers, FX chain, retention | a human, in an editor |
| SQLite | `<config-dir>/moneyfly.db` | users, identities, sessions, devices, **every domain row**, `change_log`, FX rates, integrations **and their secrets** | `internal/db` |
| IndexedDB | each browser profile | a full replica of that user's domain rows, the push queue, the sync cursor, **plus a cache of exchange rates** | `web-ui/src/db`, driven by `web-ui/src/sync` and `web-ui/src/stores/fx.ts` |

The split rule inherited from upmonitor still holds — hand-editable configuration goes in YAML;
history, volume and secrets go in SQLite — but moneyfly lands almost everything in SQLite, because
almost everything syncs. That is why `internal/config` is **load-only**: no `Save`, no `Clone`, no
copy-on-write path, and no endpoint that returns the raw file.

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
                                              ├── /api/... handlers
                                              └── serveSPA (catch-all, last)
```

`serveSPA` reads from the embedded FS (`internal/web`, `//go:embed all:dist`) and falls back to
`index.html` for unknown paths so client-side routes work on a hard refresh. Unmatched `/api/*`
paths return a JSON 404 instead of HTML — a typo'd endpoint should never hand the client a page it
would try to parse as JSON.

Errors: handlers return `fiber.NewError(code, msg)`; the central `errorHandler` renders every one as
`{"error": msg}`, which is the only shape `ApiError` on the client parses. Unexpected (non-Fiber)
errors are the only ones logged — a 404 is traffic, not an incident.

## 3. Background workers

| Worker | Cadence | Job | Status |
| --- | --- | --- | --- |
| retention | hourly | drop expired sessions and abandoned OIDC states; trim `change_log` past `sync.change_log_retention_days` | built |
| FX refresh | daily at `fx.refresh_at` | walk the provider chain, store the day's rates | built |
| recurring | hourly | materialise due `recurring_rule` rows, idempotent on `(rule_id, occurred_on)` so a restart cannot double-post | *planned* |

Trimming the journal costs a long-absent device a full re-bootstrap and never costs anyone data —
the domain rows are untouched.

## 4. Identity

Multi-user: several independent people on one instance, data isolated by `user_id`. Sign-in is
either email + password (argon2id) or any configured OIDC provider. Sessions are opaque 256-bit
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

Unlinking refuses to remove the last way in (`db.ErrLastSignInMethod`): an account with no password
and no identities is unreachable, and a self-hosted instance has no support desk.

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

## 5. Where to make a change

| Change | Go here |
| --- | --- |
| A new API endpoint | `internal/api/handlers_*.go`, register it in `Server.routes` |
| A new persisted field | a new goose migration + the entity's file in `internal/db` + the sync payload + the Dexie schema |
| Anything about conflict handling | `internal/sync` and `web-ui/src/sync` — **and [SYNC.md](SYNC.md) in the same change** |
| A screen's layout | `web-ui/src/components/monefy/*` — and check it against [MONEFY-PARITY.md](MONEFY-PARITY.md) |
| A colour | `web-ui/src/assets/tailwind.css` `@theme` block, nowhere else |
| Anything about currency conversion | `internal/fx` and `web-ui/src/lib/fx.ts` — **both, with their mirrored test tables** |
| An overlay's animation | the named transitions in `web-ui/src/assets/tailwind.css`, applied by wrapping the `v-if` at the call site |
| A new export destination | one `internal/exporter/target_*.go` that self-registers in `init()` |
| A config field | `internal/config/config.go` (struct + `normalize` + `Validate`), `config.example.yaml`, [CONFIGURATION.md](CONFIGURATION.md) |

## 6. Deliberate non-goals

- **No native app.** The PWA installs from the home screen on both platforms. A TWA wrapper adds
  nothing while that holds.
- **No stored balances.** See [SYNC.md](SYNC.md) §2 — this is load-bearing for sync correctness, not
  a style preference.
- **No raw-config endpoint.** upmonitor has one; here the file holds OIDC secrets.
- **No third-party cloud sync.** The server *is* the sync backend; that is the product.
