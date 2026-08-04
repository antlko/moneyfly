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
| IndexedDB | each browser profile | a full replica of that user's domain rows, the push queue, the sync cursor | `web-ui/src/db`, driven by `web-ui/src/sync` |

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
| FX refresh | daily at `fx.refresh_at` | walk the provider chain, store the day's rates | *planned* |
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

## 5. Where to make a change

| Change | Go here |
| --- | --- |
| A new API endpoint | `internal/api/handlers_*.go`, register it in `Server.routes` |
| A new persisted field | a new goose migration + the entity's file in `internal/db` + the sync payload + the Dexie schema |
| Anything about conflict handling | `internal/sync` and `web-ui/src/sync` — **and [SYNC.md](SYNC.md) in the same change** |
| A screen's layout | `web-ui/src/components/monefy/*` — and check it against [MONEFY-PARITY.md](MONEFY-PARITY.md) |
| A colour | `web-ui/src/assets/tailwind.css` `@theme` block, nowhere else |
| A new export destination | one `internal/exporter/target_*.go` that self-registers in `init()` |
| A config field | `internal/config/config.go` (struct + `normalize` + `Validate`), `config.example.yaml`, [CONFIGURATION.md](CONFIGURATION.md) |

## 6. Deliberate non-goals

- **No native app.** The PWA installs from the home screen on both platforms. A TWA wrapper adds
  nothing while that holds.
- **No stored balances.** See [SYNC.md](SYNC.md) §2 — this is load-bearing for sync correctness, not
  a style preference.
- **No raw-config endpoint.** upmonitor has one; here the file holds OIDC secrets.
- **No third-party cloud sync.** The server *is* the sync backend; that is the product.
