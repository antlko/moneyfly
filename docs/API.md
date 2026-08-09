# API

JSON over HTTP, same origin as the SPA. Errors are always `{"error": "message"}` with a meaningful
status — there is no second error shape anywhere.

> Health, auth, sync, exchange rates, CSV import, export, API tokens, webhooks and admin user
> management exist today. This document is filled in as later phases land. Budgets and recurring
> records (phases 6–7) introduced no new endpoints — both ride the sync entities already listed
> below; see docs/ARCHITECTURE.md §4c for the recurring worker.

## Conventions

- Request and response bodies are camelCase; database columns are snake_case. The DTO layer
  (`internal/api/dto.go`) is where the two meet.
- Money is an integer in **minor units** plus a currency code — never a float. `-1440` with `EUR` is
  €14.40 of spending.
- Dates on transactions are `YYYY-MM-DD` strings with no timezone; timestamps elsewhere are Unix
  seconds.
- Authentication is a session cookie (HttpOnly, SameSite=Lax). API tokens use
  `Authorization: Bearer <token>` against the same routes.

## `GET /api/health`

Public. Liveness plus the few instance facts the SPA needs before anyone signs in — which sign-in
methods to offer, what currency to default to.

```jsonc
{
  "status": "ok",
  "version": "dev",
  "registrationAllowed": true,     // config is `open`, OR nobody has claimed the instance yet
  "claimed": false,                // false on a brand-new instance with no accounts
  "defaultCurrency": "EUR",
  "oidcProviders": [               // id and name only — never client ids or secrets
    { "id": "google", "name": "Google" }
  ],
  "webauthnEnabled": true          // passkeys need a public URL — see MONEYFLY_BASE_URL
}
```

## Authentication

Sessions are an HttpOnly cookie (`moneyfly_session`). `deviceId` is minted by the client
(UUIDv7, `web-ui/src/lib/device.ts`) and registers the browser for sync.

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `POST` | `/api/auth/register` | — | `{email, password, displayName?, deviceId, platform}` → `User`. 403 when registration is closed, 409 when the email is taken. |
| `POST` | `/api/auth/login` | — | Same body minus `displayName` → `User`. 401 on bad credentials (identical message whether or not the account exists), 429 after 8 failures in 15 minutes. |
| `POST` | `/api/auth/logout` | — | 204. Succeeds with no cookie. |
| `GET` | `/api/auth/me` | session | `User`. 401 when not signed in — the normal answer, not an error. |
| `PUT` | `/api/auth/password` | session | `{currentPassword, newPassword}` → `User`. Revokes every other session and re-issues the caller's. `currentPassword` is not required for an account that has none. |
| `GET` | `/api/auth/identities` | session | `Identity[]` |
| `DELETE` | `/api/auth/identities/:id` | session | 204. 409 if it is the only sign-in method left. |
| `GET` | `/api/auth/oidc/:provider/start` | — | **Navigate**, don't fetch: 302 to the provider. `?link=1` attaches to the signed-in account, `?redirect=/path` sets where to land (same-origin paths only). |
| `GET` | `/api/auth/oidc/:provider/callback` | — | 302 back into the SPA, or to `/signin?error=…` on failure. |
| `GET` | `/api/devices` | session | `Device[]`, scoped to the account. |
| `DELETE` | `/api/devices/:id` | session | 204. Drops the sync cursor, so that browser re-bootstraps if it returns. |

### Passkeys

**Call, don't navigate** — the mirror image of the OIDC rows above. A passkey ceremony is a
JavaScript call to the authenticator, so there is nowhere to send the browser; these are ordinary
JSON endpoints and the client drives `navigator.credentials` between them.

Each ceremony is two calls: `options` issues a challenge, `verify` checks the answer, threaded by the
`sessionId` the first returns. That challenge is single-use and deleted the moment it is looked up,
expired or not, so a captured response cannot be replayed. `publicKey` and `credential` are the W3C
JSON shapes — exactly what `@simplewebauthn/browser` produces and consumes, with no translation on
either side.

Sign-in is **usernameless**: no email is sent, no credential list is returned, and the account is
resolved from the user handle inside the assertion itself. Registration needs a session, because a
passkey is added to an account that already exists — there is no create-an-account-with-a-passkey
path, so none of the OIDC email-collision reasoning applies here.

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `POST` | `/api/auth/webauthn/register/options` | session | → `{sessionId, publicKey}`. Already-registered credentials are excluded, so an authenticator says "you already have one" instead of quietly making a second. 503 when `webauthnEnabled` is false. |
| `POST` | `/api/auth/webauthn/register/verify` | session | `{sessionId, name, credential}` → the new passkey. 400 on an expired, forged, or another account's challenge; 409 if this authenticator is already registered. |
| `POST` | `/api/auth/webauthn/login/options` | — | → `{sessionId, publicKey}`. Answers identically whether or not any account exists. 503 when `webauthnEnabled` is false. |
| `POST` | `/api/auth/webauthn/login/verify` | — | `{sessionId, credential, deviceId, platform}` → `User`, and a session cookie — the same result as `/api/auth/login`. 401 on every failure, with one message, for the same reason password sign-in has one. |
| `GET` | `/api/auth/webauthn/credentials` | session | `Passkey[]` — id, name, timestamps. Never the public key or attestation. |
| `DELETE` | `/api/auth/webauthn/credentials/:id` | session | 204. 409 if it is the only sign-in method left, the same guard unlinking an identity has. |

The two management routes above are deliberately **not** gated on `webauthnEnabled`: a passkey that
is already registered must stay visible and removable even if the instance's public URL is later
unset, exactly as an OIDC identity stays linked after its provider leaves the config.

```jsonc
// Passkey
{ "id": "0199…", "name": "iPhone", "createdAt": 1785000000, "lastUsedAt": 0 }
```

```jsonc
// User
{
  "id": "0199…", "email": "you@example.com", "displayName": "You",
  "baseCurrency": "EUR", "isAdmin": true, "hasPassword": true, "hasPasskey": true,
  "identities": [{ "id": "…", "provider": "google", "email": "…", "createdAt": 1785000000 }]
}
```

## Sync

The protocol, and why it is shaped this way, is [SYNC.md](SYNC.md). All four require a session.

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/sync/push` | `{deviceId, ops[]}` → `{accepted, rejected[], serverSeq, lamport}`. `rejected` is `[]`, never `null`. 413 above 1000 ops. |
| `GET` | `/api/sync/pull?since=N&limit=500&deviceId=…` | `{changes[], serverSeq, hasMore}`. **409** when the cursor predates the surviving journal — bootstrap instead. |
| `GET` | `/api/sync/snapshot` | `{rows[], serverSeq, lamport}` — every live row, no tombstones. |
| `GET` | `/api/sync/events` | SSE. `event: ready` once, then `event: changed` with `{seq, deviceId}`, plus a `: ping` comment every 25s. |

```jsonc
// One operation. `data` is the row body; the server validates its shape, not its references.
{
  "entity": "txn",             // account | category | txn | budget | recurring_rule | user_setting
  "id": "0199…",               // UUIDv7, minted on the client
  "lamport": 412,
  "deviceId": "0199…",
  "deleted": false,
  "data": { "kind": "expense", "occurredOn": "2026-08-03", "amountMinor": -1440, "currency": "EUR" }
}
```

The account comes from the session; any `userId` in the payload is ignored. A **conflict is not an
error** — the losing op is silently not applied, which is what makes retrying a push safe. Only a
structurally invalid op appears in `rejected`, and only that op: one bad row never costs a device the
rest of its batch.

**`rejected` is always an array, never `null`.** Nothing rejected is an empty list, not the absence
of an answer. A nil Go slice marshals to `null`, the client's own type says the field is an array,
and the first thing it does with it is read `.length` — so the happy path was the one that threw, and
the app announced itself offline while every request returned 200.

## Exchange rates

Not scoped to a user — a rate is a fact about the world. Reads and the one write are both behind the
session cookie: an instance nobody is signed in to should answer nothing but `/api/health`.

Rates are only ever stored **against EUR**. `X -> EUR` is the computed inverse and `USD -> HUF` is a
cross rate through EUR, so there is no endpoint to ask for those directly; the client does that
arithmetic itself (`web-ui/src/lib/fx.ts`), which is also what lets it convert with no connection.

**`rate` is a decimal string, not a number.** A JSON number is an IEEE 754 double in every browser,
and this value multiplies into every figure derived from it.

### `GET /api/fx/latest`

The newest rate held for each quote currency. One request fills a client's cache for today.

```json
{
  "base": "EUR",
  "rates": [
    { "asOf": "2026-08-04", "base": "EUR", "quote": "HUF",
      "rate": "363.942549", "source": "open-er-api", "ageDays": 0 }
  ]
}
```

`ageDays` is how stale the rate is. Non-zero is normal — rates do not move at weekends — but a large
value is how a provider that has quietly stopped publishing becomes visible, instead of every total
silently freezing at last month's number.

### `GET /api/fx/rates?quote=HUF&from=&to=`

One pair's history, ascending, **one row per date**: the rate that applies, chosen exactly as the
server chooses it internally. `from` defaults to 90 days before `to`, `to` to today; both are
`YYYY-MM-DD`. `base` defaults to `EUR` and is the only value that returns anything.

One per date rather than every stored source, because the client cache is keyed on (quote, date) and
can hold only one anyway — handing it several would let it keep whichever arrived last, and convert
with a different rate from the one the server would use.

A client pulls this so that a record dated last month is priced with last month's rate. Re-pricing
old records at today's rate would make past totals move every time the app is opened.

### `PUT /api/fx/rates`

Record a rate by hand: "1 EUR is worth `rate` `quote`", on `asOf` (today if omitted).

```json
{ "quote": "HUF", "rate": "391.5", "asOf": "2026-08-05" }
```

Returns the stored rate, with `"source": "manual"`. Only `EUR`-based rates are accepted, exactly as
for a provider — the inverse is computed and never stored, which is what makes it impossible to hold
EUR→USD 1.14 and USD→EUR 0.88 at once. A rate that is not a positive decimal is `400`.

It exists because the provider chain is not the whole world: a currency no free feed publishes, an
internal rate a household has agreed, or the rate actually paid at a counter. None of those can be
fetched, and without a way to enter one those records stay outside every total.

**It overrides by ordinary means, not by privilege.** Same date, different source, and every lookup
prefers `manual` within a date — so a provider rate published on a *later* day takes over again. This
records what a rate was on a day, not a standing preference. For a currency nobody publishes, no
later rate ever arrives and the entered one keeps applying.

A hand-entered rate is never the yardstick for the refresher's plausibility check. Measuring a
published rate against a typed one would let a single misplaced decimal point reject every real rate
from then on.

### `GET /api/fx/currencies`

The exponent table — how many decimal places each currency's minor unit implies.

```json
{ "default": 2, "currencies": [{ "code": "HUF", "exponent": 0 }] }
```

The client ships its own copy of this so it can format from the first paint offline; the endpoint
exists so the two can be compared after a server upgrade.

## Import

Both require a session. See [MONEFY-PARITY.md §5](MONEFY-PARITY.md) for the CSV format and why an
unrecognised category or account is never guessed at, and `internal/importer` for the resolver.

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/import/monefy/preview` | `{csv}` → parse only, nothing written |
| `POST` | `/api/import/monefy/commit` | `{csv, categoryMap, accountMap}` → writes what resolves |

```jsonc
// preview response
{
  "totalRows": 1683,
  "parseErrors": [{ "line": 45, "reason": "date \"31.13.2021\" is not a recognised format (DD.MM.YYYY or M/D/YYYY)" }],
  "categories": [
    { "key": "expense:Utilities", "name": "Utilities", "kind": "expense",
      "resolved": false, "count": 119 },
    { "key": "expense:HotelTrip", "name": "HotelTrip", "kind": "expense", "resolved": true,
      "id": "cat:hotel-trip", "viaAlias": true, "count": 58 }
  ],
  "accounts": [
    { "key": "EUR:EUR", "name": "EUR", "currency": "EUR", "resolved": true,
      "id": "acc:eur", "count": 340 },
    // Same name, different currency — a different wallet, listed separately.
    { "key": "HUF:EUR", "name": "EUR", "currency": "HUF", "resolved": false,
      "currencyMismatch": true, "count": 12 }
  ],
  // Every currency the file uses. Rows in one with no rate import fine and then
  // sit outside every converted total, so the screen offers to declare them.
  "currencies": [{ "code": "EUR", "count": 1331 }, { "code": "HUF", "count": 352 }],
  // Rows per (category, account) pair, so a client can say exactly how many rows
  // a mapping decision covers. Summing the two `count` fields instead would
  // double-count a row whose category *and* account are both unmapped.
  "groups": [{ "categoryKey": "expense:Utilities", "accountKey": "EUR:EUR", "count": 119 }]
}
```

Accounts are grouped by `(name, currency)` and categories by `(name, kind)`, matching exactly what
the resolver matches on — so preview and commit cannot disagree about what a thing *is*. Grouping
accounts by name alone reported one resolved entry where commit found two, and the second currency's
rows were dropped as unresolved after the operator had been told the file was clean.

`categoryMap` / `accountMap` on commit are keyed on the **`key`** each preview entry carries —
`"expense:Gifts"`, `"HUF:Cash"` — not on the bare name. A name is not an identity: one file can use
`Gifts` as both an expense and an income category, and one account name can appear in two
currencies, and each of those is two distinct things resolving against different existing rows.
Keyed on the name, mapping one silently mapped the other, onto a category of the wrong kind or an
account in the wrong currency — rows written to the wrong place rather than skipped, which is the one
outcome this endpoint is built to avoid. Build nothing yourself: echo back the `key` preview gave
you. A bare name is still accepted as a fallback so older callers keep working, and a composite key
wins over one when both are present.

There is no way to create a category or account through this endpoint: create it the ordinary way
first (an op through `/api/sync/push`, same as the record screen does) and map to the id that
returns. Push before committing — an id still sitting in a client's outbox names a row the server has
never seen. Commit re-parses the same `csv` rather than trusting anything from the preview response,
for the same reason the sync protocol never trusts a client's idea of state: what a name resolves to
may have changed in the seconds between the two requests.

```jsonc
// commit response
{
  "imported": 1683,       // newly written
  "alreadyImported": 12,  // matched an existing natural key — a re-run of an overlapping export
  "parseErrors": [ /* same shape as preview */ ],
  "unresolved": [{ "line": 45, "reason": "category \"Utilities\" is not mapped" }],
  "failed": 0             // resolved but unwritable; see below. Absent reason when zero.
}
```

A row is never coerced onto some other category and never silently dropped: it either writes, or it
is one of `parseErrors` (could not be read at all) or `unresolved` (read fine, but its category or
account was not mapped), always with the line number a spreadsheet would show.

**A partial write answers 200, not 500.** The commit applies its ops in batches, because a real
export is thousands of rows and `ApplyOps` takes at most `MaxOpsPerPush` (1000) at a time — which
means N independent transactions rather than one, and a failure part way through leaves the earlier
batches committed. Reporting that as an error would be a lie about a database that now holds several
thousand new rows, and the central `errorHandler` renders only `{"error": …}`, so the count of what
landed would be lost. Instead `imported` is what was written, `failed` is what was not, and
`failureReason` carries the underlying error. Re-running the identical file is the fix and is always
safe: natural-key de-duplication skips everything that already landed.

## API tokens

A named, long-lived credential for scripted access — `Authorization: Bearer <token>` against any
route a session cookie would authenticate, `authMW` in `internal/api/middleware.go`. Unlike a
session it has no device and no expiry.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/tokens` | List this account's tokens — never the value, only `id`, `name`, `createdAt`, `lastUsedAt` |
| `POST` | `/api/tokens` | `{name}` → the token, **once**. It is not recoverable after this response; only its hash is stored. |
| `DELETE` | `/api/tokens/:id` | Revoke it. 204 whether or not `id` existed — the endpoint does not confirm another account's token id. |

## Webhooks

Notified with every transaction a push accepts from any of this account's devices — not from the
recurring worker or the CSV importer, which fire in bursts a webhook subscriber does not want one
delivery per row of (`internal/api/webhooks.go`).

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/webhooks` | List, including the secret — unlike a token, seeing it again is how the receiving end gets configured |
| `POST` | `/api/webhooks` | `{name, url}` → the stored webhook. `url` must start with `http://` or `https://`. |
| `DELETE` | `/api/webhooks/:id` | Remove it |

A delivery is `POST {event: "txn.created", transactions: [...]}` to `url`, with
`X-Moneyfly-Event: txn.created` and `X-Moneyfly-Signature`: hex `HMAC-SHA256` of the exact request
body, keyed on the webhook's secret — verify it before trusting a delivery claims to be from this
instance. Best-effort: no retry queue, and the URL's resolved address is checked against
private/loopback/link-local ranges on every delivery (not only at creation, which a changed DNS
record would outdate) — a webhook is a way to make this server issue a request on someone's behalf,
and on a multi-user instance "someone" is not necessarily the operator.

## Export

`GET /api/export/transactions.csv?profile=native|monefy` streams every live transaction as CSV,
`Content-Disposition: attachment`. `native` is this app's own shape (`date,kind,account,category,
amount,currency,note`); `monefy` matches the positional format [MONEFY-PARITY.md §5](MONEFY-PARITY.md)
documents, so a file this produces reads back through this app's own importer or opens in Monefy
itself. An unknown `profile` is `400`. See `internal/exporter` for adding a third shape.

## Admin

Root manages the others: every route needs both `authed` and `adminMW` (`internal/api/handlers_admin.go`)
— a non-admin gets `403`, same as an unauthenticated caller gets `401`. There is no separate
"owner" concept: the first account ever created is the admin (`db.CreateUser`), and any admin can
promote another.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/admin/users` | Every account on the instance, oldest first. Never a password hash. |
| `POST` | `/api/admin/users` | `{email, password, displayName?}` → provisions an account directly, bypassing `registration` entirely — this is the admin acting, not the public signing up. Not signed in by this call: the new person signs in themselves, with the password given here. |
| `DELETE` | `/api/admin/users/:id` | Removes the account and everything it owns, via the same foreign keys every synced table already carries back to `users` (docs/ARCHITECTURE.md §1) — no separate cleanup step. `400` on your own id: manage your own account from Settings, not here. `409` if it is the last admin. |
| `PUT` | `/api/admin/users/:id` | `{isAdmin}` → promotes or demotes. Acting on your own id is allowed here (stepping down when someone else already holds it is reasonable) and refused only by the same last-admin rule, `409`. |
| `GET` | `/api/admin/settings` | The instance-wide `app`/`sync`/`fx` config.yaml fields (docs/CONFIGURATION.md) — never `server.*` or `oidc.*`, which are not part of this response at all. |
| `PUT` | `/api/admin/settings` | Same shape back, with whatever changed — a full replacement, not a patch, so there is no ambiguity about what an absent field means. Validates and writes `config.yaml`, then swaps the running config in immediately: no restart. `400` with the validation message on a bad value (e.g. an unknown `registration` mode); the file is untouched on that path. |

```jsonc
// one entry from GET /api/admin/users
{ "id": "0199…", "email": "you@example.com", "displayName": "You",
  "baseCurrency": "EUR", "isAdmin": true, "createdAt": 1785000000 }
```

```jsonc
// GET /api/admin/settings
{ "registration": "open", "defaultCurrency": "EUR", "sessionTtlDays": 365,
  "changeLogRetentionDays": 90, "fxEnabled": true, "fxRefreshAt": "04:00",
  "fxProviders": ["open-er-api", "fawazahmed0"] }
```

## Not found

Any unmatched path under `/api/` returns `404` as JSON:

```json
{ "error": "not found" }
```

Everything else falls through to the SPA, so a client-side route survives a hard refresh.
