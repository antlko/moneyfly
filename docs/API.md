# API

JSON over HTTP, same origin as the SPA. Errors are always `{"error": "message"}` with a meaningful
status — there is no second error shape anywhere.

> Health, auth, sync and exchange rates exist today (phases 0–5). This document is filled in as
> later phases land.

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
  ]
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

```jsonc
// User
{
  "id": "0199…", "email": "you@example.com", "displayName": "You",
  "baseCurrency": "EUR", "isAdmin": true, "hasPassword": true,
  "identities": [{ "id": "…", "provider": "google", "email": "…", "createdAt": 1785000000 }]
}
```

## Sync

The protocol, and why it is shaped this way, is [SYNC.md](SYNC.md). All four require a session.

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/sync/push` | `{deviceId, ops[]}` → `{accepted, rejected[], serverSeq, lamport}`. 413 above 1000 ops. |
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

## Exchange rates

Read-only, and not scoped to a user — a rate is a fact about the world. Still behind the session
cookie: an instance nobody is signed in to should answer nothing but `/api/health`.

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

One pair's history, ascending. `from` defaults to 90 days before `to`, `to` to today; both are
`YYYY-MM-DD`. `base` defaults to `EUR` and is the only value that returns anything.

A client pulls this so that a record dated last month is priced with last month's rate. Re-pricing
old records at today's rate would make past totals move every time the app is opened.

### `GET /api/fx/currencies`

The exponent table — how many decimal places each currency's minor unit implies.

```json
{ "default": 2, "currencies": [{ "code": "HUF", "exponent": 0 }] }
```

The client ships its own copy of this so it can format from the first paint offline; the endpoint
exists so the two can be compared after a server upgrade.

## Not found

Any unmatched path under `/api/` returns `404` as JSON:

```json
{ "error": "not found" }
```

Everything else falls through to the SPA, so a client-side route survives a hard refresh.
