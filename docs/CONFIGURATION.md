# Configuration

Everything lives in `<config-dir>/config.yaml`. The config directory is `./config` by default,
`/config` in Docker, and can be set with `--config-dir` or `MONEYFLY_CONFIG_DIR`.

**A missing config.yaml is not an error** — the instance starts fully defaulted. **Config is read
only at startup**, so changes need a restart; there is no admin UI for it and no API that exposes
it, because it carries OIDC client secrets.

Start from `config/config.example.yaml`.

## `server`

| Field | Default | Meaning |
| --- | --- | --- |
| `addr` | `:5007` | Listen address. Overridden by `MONEYFLY_ADDR`. |
| `base_url` | *empty* | Externally reachable origin, e.g. `https://money.example.com`. **Required once any OIDC provider is configured** — the provider must be handed an absolute redirect URI. Overridden by `MONEYFLY_BASE_URL`. |

The default port is 5007 rather than the usual 8080: on a machine that self-hosts
anything at all, 8080 is already taken, and the failure mode is a container that
will not start for a reason that has nothing to do with this application.

## `app`

| Field | Default | Meaning |
| --- | --- | --- |
| `registration` | `open` | `open` — anyone who can reach the instance may create an account. `closed` — no new accounts. Set this after creating yours if the instance is public. Overridden by `MONEYFLY_REGISTRATION`. |
| `default_currency` | `EUR` | Currency a new account starts with. Three-letter code. |
| `session_ttl_days` | `365` | Session lifetime. Long by design: this is a phone app you should not have to sign in to twice a year. |

## `sync`

| Field | Default | Meaning |
| --- | --- | --- |
| `change_log_retention_days` | `90` | How long sync deltas are kept. A device offline longer re-bootstraps from a full snapshot instead of replaying deltas — correct either way, just more bytes. Lower it if the table gets large; it does not affect your transactions, only the change journal. |

## `fx`

| Field | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` (`true` in `config.example.yaml`) | Fetch daily exchange rates. With it off, records in a currency other than the base one are left out of totals and counted instead. |
| `refresh_at` | `04:00` | Local time of the daily refresh, `HH:MM`. The next occurrence is computed each cycle rather than ticking every 24h, so it does not drift across restarts or daylight saving. |
| `providers` | `[open-er-api, fawazahmed0]` | Tried in order; the first that answers wins. Both are free and keyless. |

Three things are rejected at startup rather than at 04:00 the next morning: an unknown provider id,
a `refresh_at` that is not a time of day, and `enabled: true` with an empty provider list. An unknown
id used to be skipped in silence, and the only symptom was rates that quietly stopped updating.

Nothing here is in a request path. A provider outage keeps the last stored rates and logs it; a
single-day move over 15% is rejected and the previous rate kept, because a broken feed and a real
currency event are indistinguishable on screen and only one of them should be believed.

Which currencies a provider must publish to be accepted is not configured — it is read from your own
accounts and records, so adding a forint account is what makes this instance start requiring a
forint rate.

## `oidc`

A list of identity providers. Any standards-compliant OIDC issuer works — Google, Authentik,
Keycloak, Zitadel. Nothing in the implementation is Google-specific.

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

Applied after the file and always winning, so a container can be configured without mounting a
config at all.

| Variable | Overrides |
| --- | --- |
| `MONEYFLY_CONFIG_DIR` | config directory (also `--config-dir`, which wins) |
| `MONEYFLY_ADDR` | `server.addr` |
| `MONEYFLY_BASE_URL` | `server.base_url` |
| `MONEYFLY_REGISTRATION` | `app.registration` |
| `MONEYFLY_LOG_LEVEL` | log level: `debug` \| `info` \| `warn` \| `error` |
| `MONEYFLY_OIDC_<ID>_CLIENT_ID` | that provider's `client_id` |
| `MONEYFLY_OIDC_<ID>_CLIENT_SECRET` | that provider's `client_secret` |

`<ID>` is the provider's `id`, upper-cased with `-` replaced by `_`: provider `my-idp` reads
`MONEYFLY_OIDC_MY_IDP_CLIENT_SECRET`.

## Validation

Startup fails, loudly, rather than running with a config that cannot work: an unknown `registration`
mode, a currency code that is not three letters, a retention below one day, an OIDC provider missing
`issuer` or `client_id`, duplicate provider ids, or OIDC configured without `server.base_url`.
