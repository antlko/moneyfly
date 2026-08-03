# 09 — API

REST, versioned at `/api/v1`, JSON only. Machine-readable spec: [openapi.yaml](openapi.yaml).

## 9.1 Conventions

**Auth.** Server-side session in an `HttpOnly; Secure; SameSite=Strict` cookie. `user_id` is read **only** from the session — never from a body, query or header. Every repository call takes it explicitly.

**Money.** Always an object, never a bare number:

```json
{ "amount_minor": 6500, "currency": "EUR", "exponent": 2 }
```

Clients format from `amount_minor` and `exponent`; no float ever crosses the wire. Values in base currency additionally carry `fx_rate_id` and `as_of_date` so a figure can be traced to its rate.

**Absent.** `null` means not recorded. `0` means recorded zero. Never interchangeable.

**Errors.** RFC 7807 `application/problem+json`:

```json
{
  "type": "https://moneyapp.local/problems/validation",
  "title": "Validation failed",
  "status": 422,
  "detail": "2 fields are invalid",
  "errors": [
    { "field": "amount_minor", "message": "must be greater than zero" },
    { "field": "occurred_on",  "message": "must be a date in YYYY-MM-DD form" }
  ]
}
```

**Pagination.** Cursor-based on `(occurred_on, id)`. Offset pagination drifts when rows are inserted mid-scroll, and imports insert thousands at once.

```json
{ "items": [], "next_cursor": "eyJkIjoiMjAyNi0wNy0wMSIsImkiOjkxfQ==", "has_more": true }
```

**Idempotency.** `POST` accepts `Idempotency-Key`. Replaying a key returns the original response rather than creating a duplicate — necessary because entry is optimistic and may retry.

**Rate limits.** Per-IP on `/auth/*`, per-user on `/imports`. `429` with `Retry-After`.

## 9.2 Endpoints

### Auth

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/auth/login` | Email + password → session cookie |
| `POST` | `/auth/logout` | Revoke current session |
| `GET` | `/auth/me` | Current user, role, base currency, fiscal-year start |
| `POST` | `/auth/password` | Change password; revokes other sessions |
| `GET` | `/auth/sessions` | List active sessions |
| `DELETE` | `/auth/sessions/{token_id}` | Revoke one |

No `POST /auth/register`. Users are created by an admin or from an invite ([adr/0010](adr/0010-invite-only-auth.md)).

### Categories, accounts, aliases

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/categories` | `?kind=`, `?include_archived=` |
| `POST` | `/categories` | |
| `PATCH` | `/categories/{id}` | Rename, icon, colour, `is_essential`, sort |
| `DELETE` | `/categories/{id}` | Archives; never orphans transactions |
| `POST` | `/categories/{id}/merge` | Body `{ "into_id": n }`; rewrites rows, leaves an alias |
| `GET`/`POST`/`DELETE` | `/categories/aliases[/{id}]` | The §4.7 mapping table |
| `GET`/`POST`/`PATCH`/`DELETE` | `/accounts[/{id}]` | Same shape; parents are computed and reject direct values |
| `GET`/`POST`/`DELETE` | `/accounts/aliases[/{id}]` | |

### Transactions

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/transactions` | `?from=&to=&category_id=&account_id=&kind=&q=&cursor=&limit=` |
| `POST` | `/transactions` | Quick entry. Honours `Idempotency-Key` |
| `GET` | `/transactions/{id}` | |
| `PATCH` | `/transactions/{id}` | |
| `DELETE` | `/transactions/{id}` | Soft delete |
| `POST` | `/transfers` | Creates the paired rows in one `transfer_group_id` |

### Budgets

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/budgets?period=YYYY-MM` | Planned per category |
| `PUT` | `/budgets/{category_id}/{period}` | Upsert one |
| `POST` | `/budgets/bulk` | `{ "from_period", "to_period", "items": [...] }` — seeds a year from one figure per category |

### Snapshots

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/snapshots?period=YYYY-MM` | Every account, with previous month pre-filled for the entry screen |
| `PUT` | `/snapshots/{account_id}/{period}` | `amount_minor` **or** `quantity_nano`, never both |
| `POST` | `/snapshots/bulk` | Whole month in one call |
| `GET` | `/snapshots/{period}/reconciliation` | Snapshot vs transaction-implied, with drift per account |

### Imports

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/imports` | `multipart/form-data`. Returns a batch in `parsed` or `needs_mapping` |
| `GET` | `/imports` | Batch history with counts |
| `GET` | `/imports/{id}` | Batch detail + preview payload ([04](04-import-monefy.md) §4.11) |
| `GET` | `/imports/{id}/rows` | `?status=unmapped\|duplicate\|rejected\|new` |
| `POST` | `/imports/{id}/mappings` | Supply decisions; re-resolves the batch |
| `POST` | `/imports/{id}/commit` | Single transaction. `409` if not `previewed` |
| `POST` | `/imports/{id}/revert` | Soft-deletes exactly this batch's rows |

Committing a batch with `rows_unmapped > 0` is **impossible** — `409 Conflict`. This is the API-level guarantee against the 14% loss.

### Reports

| Method | Path | Returns |
| --- | --- | --- |
| `GET` | `/reports/budget?period=` | Per category: actual, planned, percentage, threshold state |
| `GET` | `/reports/summary?from=&to=` | Per period: spend, income, diff, saved % |
| `GET` | `/reports/categories?from=&to=` | Totals, averages, per-period series |
| `GET` | `/reports/capital?period=` | Net worth, liquid, allocation over leaf accounts, runway |
| `GET` | `/reports/capital/series?from=&to=` | Net worth over time; `delta_total`, `delta_real`, `delta_fx` |
| `GET` | `/reports/runway?period=&burn_mode=` | `burn_mode` per [07](07-metrics-and-budgets.md) §7.5 |
| `GET` | `/reports/net-worth-in/{currency}?period=` | Rows 71–72 generalised |

All accept `?valuation=contemporaneous|constant` ([06](06-fx-and-providers.md) §6.6). Default `contemporaneous`.

### Settings, providers, FX

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/settings` | All, with mode, provider, staleness, last error |
| `PATCH` | `/settings/{key}` | Set `mode`, `manual_value`, `provider_key`, interval |
| `POST` | `/settings/{key}/refresh` | Force a fetch now |
| `GET` | `/providers` | Registry with health |
| `GET` | `/fx/rates?base=&quote=&from=&to=` | Rate history |
| `GET` | `/fx/rates/latest` | Current set with age |

### Metrics and dashboard

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/metrics` | Built-in + user-defined |
| `POST`/`PATCH`/`DELETE` | `/metrics[/{key}]` | Built-ins are immutable |
| `POST` | `/metrics/validate` | Parse without saving; returns AST errors |
| `GET` | `/metrics/{key}/series?from=&to=` | Evaluated per period |
| `GET`/`PUT` | `/dashboard` | Widget layout |

### Telegram

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/telegram/link-code` | Mint a single-use code, 10-min TTL |
| `GET` | `/telegram/links` | Linked chats |
| `DELETE` | `/telegram/links/{id}` | Revoke |

### Data portability and ops

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/export?format=json\|csv` | Everything the user owns |
| `POST` | `/import/full` | Restore from a `/export` archive |
| `GET` | `/healthz` | Liveness. Unauthenticated |
| `GET` | `/readyz` | DB reachable, migrations current |
| `GET` | `/version` | Build info |

## 9.3 Report response shape

```json
{
  "period": "2026-06",
  "valuation": "contemporaneous",
  "spend_total":  { "amount_minor": 271427, "currency": "EUR", "exponent": 2 },
  "planned_total":{ "amount_minor": 235000, "currency": "EUR", "exponent": 2 },
  "income":       { "amount_minor": 487300, "currency": "EUR", "exponent": 2 },
  "diff":         { "amount_minor": 215873, "currency": "EUR", "exponent": 2 },
  "saved_percent": 0.4429975785,
  "categories": [
    {
      "category_id": 13,
      "name": "Transport",
      "actual":  { "amount_minor": 46287, "currency": "EUR", "exponent": 2 },
      "planned": { "amount_minor": 15000, "currency": "EUR", "exponent": 2 },
      "ratio": 3.0858,
      "state": "severely_over",
      "average": { "amount_minor": 23698, "currency": "EUR", "exponent": 2 }
    }
  ]
}
```

`state` is computed server-side from the §7.6 precedence so every client colours identically. `saved_percent` is a ratio, unclamped — negative when spend exceeds income, `null` when income is zero.

## 9.4 Validation

- Dates `YYYY-MM-DD`, periods `YYYY-MM`, rejected otherwise. The importer's `DD.MM.YYYY` handling is confined to the importer.
- `amount_minor` is a non-negative integer; sign is carried by `kind`.
- Currency must exist in `currency`; `amount_minor` must be consistent with its exponent.
- `category_id` is forbidden on transfers and required otherwise — mirroring the `CHECK` in [03](03-data-model.md).
- Snapshots accept exactly one of `amount_minor` or `quantity_nano`.
- Unknown JSON fields are rejected rather than ignored, so a client typo surfaces immediately.

## 9.5 Not in the API

| Absent | Why |
| --- | --- |
| Anything accepting `user_id` | Always from the session |
| Bulk delete | Reverting a batch is the sanctioned bulk operation |
| Hard delete | Soft delete only |
| Public share links | No unauthenticated read path to financial data |
| Google Sheets sync | One-time historical import via CLI, not an endpoint |
| Webhooks | Nothing consumes them |
