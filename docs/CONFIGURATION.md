# Configuration

Everything lives in `<config-dir>/config.yaml`. The config directory is `./config` by default,
`/config` in Docker, and can be set with `--config-dir` or `MONEYFLY_CONFIG_DIR`.

**A missing config.yaml is not an error** — the instance starts fully defaulted, and that full set
of defaults is also written out to `config.yaml` on the spot, so the file shows every field there
is to adjust rather than only the ones an operator already knew to set.

**Every value has exactly one owner.** There are three tiers, and nothing appears in two of them:

| Tier | What | How it is set |
| --- | --- | --- |
| **Environment-only** | Transport (`addr`, `base_url`), config dir, log level | Environment variables. Never written to `config.yaml`. |
| **File + Settings screen** | `app`, `sync`, `fx` | Hand-edit `config.yaml`, or change it live in **Settings → Instance** (`GET`/`PUT /api/admin/settings`). Applied without a restart and written back to the file. |
| **File-only** | `oidc` | Hand-edit `config.yaml`. Client secrets are better supplied by environment variable. Restart required. |

That split is the point: a field settable from two places has no single source of truth, and the
symptom is a Settings screen that accepts a change, says "saved and applied", and shows the old
value again — while quietly writing the environment's value into `config.yaml` as a side effect of
saving something unrelated. Transport moved out of the file entirely rather than being marked
read-only, because a running process cannot rebind its own listen address anyway: `addr` in a file
is a value that looks editable and is not.

There is still no raw config editor and no endpoint that exposes the whole file — the settings API
only ever sees `app`/`sync`/`fx`, on purpose, because `oidc.*` can carry a client secret which must
never round-trip through a write endpoint. An OIDC secret supplied only by environment variable is
never even readable by that endpoint, let alone writable — see `oidc` below.

Start from `config/config.example.yaml`.

## `server` — environment-only

Not a section of `config.yaml` any more. Set these as environment variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `MONEYFLY_ADDR` | `:5007` | Listen address. |
| `MONEYFLY_BASE_URL` | *empty* | Externally reachable origin, e.g. `https://money.example.com`. **Required once any OIDC provider is configured** — the provider must be handed an absolute redirect URI. |

The default port is 5007 rather than the usual 8080: on a machine that self-hosts
anything at all, 8080 is already taken, and the failure mode is a container that
will not start for a reason that has nothing to do with this application.

> **Upgrading from a config.yaml with a `server:` block.** It still works. The block is read, a
> warning names the variable to move to, and — importantly — it is **written back unchanged** on
> every save, so an instance does not keep working until someone edits an unrelated setting and then
> fail to start on the restart after that. Set the matching variable and the block is released: the
> next save removes it and the migration is done. Nothing to do by hand, and nothing breaks if you
> never get round to it.

## `app` — live, via Settings → Instance

| Field | Default | Meaning |
| --- | --- | --- |
| `registration` | `open` | `open` — anyone who can reach the instance may create an account. `closed` — no new accounts, except the very first one. Set this after creating yours if the instance is public. `MONEYFLY_REGISTRATION` **seeds it on first boot only** — see below. |
| `default_currency` | `EUR` | Currency a new account starts with. Three-letter code. |
| `session_ttl_days` | `365` | Session lifetime, 1 to 3650. Long by design: this is a phone app you should not have to sign in to twice a year. |

`MONEYFLY_REGISTRATION` is a **seed, not an override**. On a brand-new instance — no `config.yaml`
yet — it decides what registration starts as and is written into the file like any other default.
After that the file owns it, and the variable is never read again, so changing registration in
Settings sticks instead of reverting on the next restart.

That is what makes `MONEYFLY_REGISTRATION=closed` still worth setting on a public container: an
instance whose config volume is empty comes up closed, so only *your* first account can be created
and nobody who finds the URL first can claim it. Previously the variable also re-asserted itself on
every load, which meant saving any unrelated setting silently baked it into `config.yaml` forever,
and changing the dropdown appeared to work and then reverted.

## `sync` — live, via Settings → Instance

| Field | Default | Meaning |
| --- | --- | --- |
| `change_log_retention_days` | `90` | How long sync deltas are kept. A device offline longer re-bootstraps from a full snapshot instead of replaying deltas — correct either way, just more bytes. Lower it if the table gets large; it does not affect your transactions, only the change journal. |

## `fx` — live, via Settings → Instance

| Field | Default | Meaning |
| --- | --- | --- |
| `enabled` | `true` | Fetch daily exchange rates. Set it to `false` to turn the refresh off; records in a currency other than the base one are then left out of totals and counted instead. |
| `refresh_at` | `04:00` | Local time of the daily refresh, `HH:MM`. The next occurrence is computed each cycle rather than ticking every 24h, so it does not drift across restarts or daylight saving. |
| `providers` | `[open-er-api, fawazahmed0]` | Tried in order; the first that answers wins. Both are free and keyless. |

Changing any of these three sections from Settings → Instance takes effect immediately, not on the
next restart — including `enabled`: turning it back on wakes the refresh loop and, if today's rates
are missing, fetches them right away rather than waiting for the next `refresh_at`.

**`enabled` defaults on, and absent is not the same as `false`.** It was a plain Go bool, so an
instance with no `config.yaml` — a bare `docker run` against an empty volume, which is a supported
way to run this — got the zero value and never fetched a single rate. Every foreign-currency record
then sat outside every total indefinitely, under a caption reading "no exchange rate yet", with
nothing on any screen or in any log to say the feature had never been switched on.

Three things are rejected at startup rather than at 04:00 the next morning: an unknown provider id,
a `refresh_at` that is not a time of day, and an enabled `fx` with an empty provider list. An unknown
id used to be skipped in silence, and the only symptom was rates that quietly stopped updating.

Nothing here is in a request path. A provider outage keeps the last stored rates and logs it; a
single-day move over 15% is rejected and the previous rate kept, because a broken feed and a real
currency event are indistinguishable on screen and only one of them should be believed.

Which currencies a provider must publish to be accepted is not configured — it is read from your own
accounts and records, so adding a forint account is what makes this instance start requiring a
forint rate.

## `oidc` — restart required, file/env only

A list of identity providers. Any standards-compliant OIDC issuer works — Google, Authentik,
Keycloak, Zitadel. Nothing in the implementation is Google-specific.

Not in the settings API, deliberately: `client_secret` must never be readable or writable through
an HTTP endpoint. The settings API (`GET`/`PUT /api/admin/settings`) never even parses this section
of the file — it reads and writes `app`/`sync`/`fx` only and leaves whatever `oidc` (and `server`)
already contain untouched, so this list, and any secret in it, can only ever be changed by editing
`config.yaml` directly or through the environment variables below.

| Field | Required | Meaning |
| --- | --- | --- |
| `id` | yes | Short slug, unique. Appears in the callback URL. |
| `name` | no | Label on the sign-in button. Defaults to `id`. |
| `issuer` | yes | Issuer URL; discovery is done from `<issuer>/.well-known/openid-configuration`. |
| `client_id` | yes | |
| `client_secret` | no | Better supplied by environment variable — see below. |
| `scopes` | no | Defaults to `[openid, email, profile]`. |

The redirect URI to register with the provider is:

```
<base_url>/api/auth/oidc/<id>/callback
```

## Environment variables

A container can be configured without mounting a config at all.

| Variable | Sets | When it is read |
| --- | --- | --- |
| `MONEYFLY_CONFIG_DIR` | config directory (also `--config-dir`, which wins) | Startup |
| `MONEYFLY_ADDR` | listen address | Every load — the only source |
| `MONEYFLY_BASE_URL` | public origin | Every load — the only source |
| `MONEYFLY_LOG_LEVEL` | log level: `debug` \| `info` \| `warn` \| `error` | Startup |
| `MONEYFLY_REGISTRATION` | `app.registration` | **First boot only**, as a seed |
| `MONEYFLY_ADMIN_EMAIL` | promotes that account to administrator | Every start, idempotent |
| `MONEYFLY_OIDC_<ID>_CLIENT_ID` | that provider's `client_id` | Every load |
| `MONEYFLY_OIDC_<ID>_CLIENT_SECRET` | that provider's `client_secret` | Every load |

### Recovering administrator access

Admin goes to whoever registers first, and only an existing admin can grant it to
anyone else. That is a dead end if the first account was a throwaway, or belongs to
someone who has left: every admin action answers `403 admin only`, Settings → Instance
is unreachable, and the image is distroless so there is no shell to run a query in.

`MONEYFLY_ADMIN_EMAIL` is the way out. Set it to the address of an existing account and
restart; that account becomes an administrator, and the promotion is logged. It is
idempotent, so leaving it set does nothing on later restarts, and an address that matches
no account logs a warning rather than refusing to start — a typo should not turn a
recoverable situation into an outage.

```
MONEYFLY_ADMIN_EMAIL=you@example.com
```

Only non-empty values count — a variable set to the empty string is the same as not setting it, so
you cannot use one to force a field back to empty. The OIDC variables apply to providers already
listed in `config.yaml`; you cannot define a whole provider from the environment.

`<ID>` is the provider's `id`, upper-cased with `-` replaced by `_`: provider `my-idp` reads
`MONEYFLY_OIDC_MY_IDP_CLIENT_SECRET`.

## Validation

Startup fails, loudly, rather than running with a config that cannot work: an unknown `registration`
mode, a currency code that is not three letters, a retention below one day, an OIDC provider missing
`issuer` or `client_id`, duplicate provider ids, or OIDC configured without `server.base_url`.
