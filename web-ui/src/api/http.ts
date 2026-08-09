/**
 * Thin fetch wrapper for the moneyfly JSON API.
 *
 * Everything the dashboard reads comes from IndexedDB, not from here — this
 * client exists for auth, sync and the operations that genuinely need a server
 * (import, export, integrations). That is why it has no caching and no retries:
 * the sync engine owns retrying.
 */

import type { Rate } from '@/lib/fx'
import type { Op, PullResponse, PushResponse, SnapshotResponse } from '@/sync/types'

/** An error carrying the HTTP status, so callers can branch on 401 vs the rest. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

/**
 * The request never reached a server — a tunnel, a dead host, aeroplane mode.
 *
 * Declared here, at the only place that can actually tell, because callers
 * cannot. `fetch` signals a transport failure by rejecting with a `TypeError`,
 * and a `TypeError` is also what a plain bug produces — reading `.length` off a
 * null, say. Deciding by type further up therefore filed local defects as
 * "offline", which sends whoever is debugging to check a connection that was
 * never the problem. That shipped: one null field in a response had the app
 * reporting itself offline while every request returned 200.
 *
 * So the distinction is made where the evidence is: only a rejected `fetch` call
 * becomes one of these.
 */
export class NetworkError extends Error {
  constructor(readonly cause: unknown) {
    super(cause instanceof Error ? cause.message : String(cause))
    this.name = 'NetworkError'
  }
}

/**
 * How long a request gets before it is treated as unreachable.
 *
 * Without this, a `fetch` that never gets a response — a captive portal, a
 * satellite link mid-handshake, a proxy that silently swallows the
 * connection — simply never resolves, for as long as the browser's own
 * connection timeout, which can be minutes. The one caller that awaits a
 * request before rendering anything (`auth.bootstrap`, in the router guard)
 * already has a documented offline fallback — the profile cached in
 * `meta.userProfile` (stores/auth.ts) — that exists precisely for this
 * moment. It only helps if the failing request actually fails; an installed
 * PWA opened on a bad connection is exactly when this matters most; a fast
 * connection never notices the timeout exists.
 *
 * 15s, not something tighter: most calls here are a single small JSON
 * payload and would be safe well under a second, but a Monefy import commit
 * parses and resolves thousands of rows server-side in the same request, and
 * a timeout tuned for the common case would fire on the one case size
 * actually matters for.
 */
const REQUEST_TIMEOUT_MS = 15_000

/**
 * A human sentence for a status the server did not explain itself.
 *
 * Reached when the body is not this API's `{"error": …}` shape — which means
 * something in front of the app answered: a reverse proxy, a gateway, an auth
 * layer. Those are exactly the failures an operator most needs named, and
 * exactly the ones that used to surface as an empty string.
 */
function describeStatus(status: number): string {
  switch (status) {
    case 400:
      return 'The server rejected the request (400). If this was a file, it may not be the format this screen expects.'
    case 401:
      return 'Not signed in (401).'
    case 403:
      return 'Not allowed (403). This action needs an administrator account.'
    case 404:
      return 'Not found (404). The server may be running an older version than this app.'
    case 413:
      return 'Too large (413). A proxy in front of the app may be limiting the upload size.'
    case 502:
    case 503:
    case 504:
      return `The server is unreachable behind its proxy (${status}).`
    default:
      return `The server returned an error (${status}).`
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  const timeout = AbortSignal.timeout(REQUEST_TIMEOUT_MS)
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: timeout,
    })
  } catch (e) {
    // A timeout aborts with the same DOMException a dropped connection would
    // throw for any other reason, so it lands here exactly like one — same
    // NetworkError, same offline fallback, because functionally that is
    // what it is.
    throw new NetworkError(e)
  }

  if (!res.ok) {
    // The server renders every error as {"error": "..."} via its central
    // ErrorHandler; anything else means we did not reach the API at all — a
    // reverse proxy, a gateway, an auth layer in front of it.
    //
    // `statusText` cannot be the fallback: it is **always empty over HTTP/2**,
    // which is what any instance behind a TLS terminator is serving. Falling
    // back to it produced an empty error message in exactly the deployment
    // where something had gone wrong, so the app said nothing at all and the
    // only way to find out what happened was the network tab.
    let message = ''
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      /* non-JSON body — fall through to the generic description */
    }
    throw new ApiError(res.status, message || describeStatus(res.status))
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  del: <T>(path: string) => request<T>('DELETE', path),
}

// --- Types (mirror backend/internal/api/dto.go) ---------------------------------

export interface OidcProvider {
  id: string
  name: string
}

/** Instance facts served before anyone is signed in. */
export interface Health {
  status: string
  version: string
  registrationAllowed: boolean
  /** False on a brand-new instance that nobody has claimed yet. */
  claimed: boolean
  defaultCurrency: string
  oidcProviders: OidcProvider[]
}

export interface Identity {
  id: string
  provider: string
  email: string
  createdAt: number
}

export interface User {
  id: string
  email: string
  displayName: string
  baseCurrency: string
  isAdmin: boolean
  hasPassword: boolean
  identities: Identity[]
}

export interface Device {
  id: string
  name: string
  platform: string
  createdAt: number
  lastSeenAt: number
  current: boolean
}

export interface Credentials {
  email: string
  password: string
  displayName?: string
  deviceId: string
  platform: string
}

// --- Endpoints ------------------------------------------------------------------

export const getHealth = () => api.get<Health>('/api/health')
export const register = (body: Credentials) => api.post<User>('/api/auth/register', body)
export const login = (body: Credentials) => api.post<User>('/api/auth/login', body)
export const logout = () => api.post<void>('/api/auth/logout')
export const getMe = () => api.get<User>('/api/auth/me')

export const changePassword = (currentPassword: string, newPassword: string) =>
  api.put<User>('/api/auth/password', { currentPassword, newPassword })

export const unlinkIdentity = (id: string) => api.del<void>(`/api/auth/identities/${id}`)

export const getDevices = () => api.get<Device[]>('/api/devices')
export const forgetDevice = (id: string) => api.del<void>(`/api/devices/${id}`)

// --- Sync (see docs/SYNC.md) ------------------------------------------------------

export const syncPush = (deviceId: string, ops: Op[]) =>
  api.post<PushResponse>('/api/sync/push', { deviceId, ops })

export const syncPull = (since: number, deviceId: string) =>
  api.get<PullResponse>(`/api/sync/pull?since=${since}&deviceId=${encodeURIComponent(deviceId)}`)

export const syncSnapshot = () => api.get<SnapshotResponse>('/api/sync/snapshot')

// --- Exchange rates ---------------------------------------------------------------

/** The most recent rate for every quote the server holds. */
export const fxLatest = () => api.get<{ base: string; rates: Rate[] }>('/api/fx/latest')

/**
 * A pair's history, so a record dated last month is priced with last month's
 * rate. Re-pricing old records at today's rate would make past totals move
 * every time the app is opened.
 */
export const fxHistory = (quote: string, from: string, to: string) =>
  api.get<{ rates: Rate[] }>(
    `/api/fx/rates?quote=${encodeURIComponent(quote)}` +
      `&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
  )

/**
 * Record a rate by hand: "1 EUR is worth `rate` `quote`", on `asOf` (today if
 * omitted).
 *
 * `rate` is a decimal *string* for the same reason the server sends one — a
 * `Number` would put it through a binary float before it ever reached a
 * multiplication.
 *
 * It overrides whatever a provider published for that date, and a provider rate
 * published on a later date takes over again: this records what a rate was on a
 * day, not a standing preference.
 */
export const fxSetRate = (quote: string, rate: string, asOf?: string) =>
  api.put<Rate>('/api/fx/rates', { quote, rate, asOf })

// --- Import (see docs/MONEFY-PARITY.md §5) -----------------------------------------

/** One distinct category or account name found in the file, and how it resolves. */
export interface ImportNameStatus {
  /**
   * How this entry is identified in `categoryMap` / `accountMap` — the
   * composite ("expense:Gifts", "HUF:Cash"), not the bare name. Built by the
   * server; echo it back, never construct it.
   *
   * A name alone is not an identity: one file can use "Gifts" as both an
   * expense and an income category, and one account name can appear in two
   * currencies. Keying the map on the name mapped both at once, onto a row of
   * the wrong kind or currency.
   */
  key: string
  name: string
  /** Category only — "expense" | "income". */
  kind?: string
  /** Account only — the currency its rows use. */
  currency?: string
  resolved: boolean
  id?: string
  viaAlias?: boolean
  /**
   * Account only — the name matched an existing account, but none of that
   * name has this currency. Distinct from a plain unresolved name: the
   * account exists, just not for this currency, so the operator needs a
   * different message and a nudge toward creating a same-name wallet in the
   * right currency rather than assuming the name was never set up.
   */
  currencyMismatch?: boolean
  /** How many rows use this name — what tells you whether an unresolved one is worth pausing for. */
  count: number
}

export interface ImportRowError {
  line: number
  reason: string
}

/** One currency the file uses, and how many rows are in it. */
export interface ImportCurrencyCount {
  code: string
  count: number
}

/**
 * How many rows use one (category, account) pair.
 *
 * The two name counts cannot simply be added to work out what a mapping choice
 * costs — a row blocked by an unmapped category *and* an unmapped account would
 * be counted twice, and the screen would claim to skip more rows than the file
 * has. These let the client take a union instead.
 */
export interface ImportGroupCount {
  categoryKey: string
  accountKey: string
  count: number
}

export interface ImportPreview {
  totalRows: number
  parseErrors: ImportRowError[]
  categories: ImportNameStatus[]
  accounts: ImportNameStatus[]
  currencies: ImportCurrencyCount[]
  groups: ImportGroupCount[]
}

export interface ImportResult {
  imported: number
  alreadyImported: number
  parseErrors: ImportRowError[]
  unresolved: ImportRowError[]
  /**
   * Rows that resolved but could not be written, because the commit is applied
   * in batches and one after the first failed. Zero on every ordinary import.
   *
   * This arrives on a 200, not an error: the earlier batches are committed, and
   * calling that a failure would misreport a database that now holds several
   * thousand new rows. Re-running the same file is safe — the server
   * de-duplicates on natural keys, so what landed is skipped.
   */
  failed: number
  failureReason?: string
}

/** Parses the file and reports what it will take to import cleanly. Writes nothing. */
export const importMonefyPreview = (csv: string) =>
  api.post<ImportPreview>('/api/import/monefy/preview', { csv })

/**
 * Re-parses the same text and writes every row it can resolve.
 *
 * `categoryMap` / `accountMap` are keyed on the CSV's own name for that
 * category or account — exactly what `ImportPreview` reported as unresolved —
 * mapping it to an existing id. Create a new category or account first, the
 * ordinary way (`sync.write`), and map to the id that returns.
 */
export const importMonefyCommit = (
  csv: string,
  categoryMap: Record<string, string>,
  accountMap: Record<string, string>,
) => api.post<ImportResult>('/api/import/monefy/commit', { csv, categoryMap, accountMap })

// --- Integrations ------------------------------------------------------------------

export interface ApiToken {
  id: string
  name: string
  createdAt: number
  lastUsedAt: number
}

/** Only the create response ever carries the plaintext — see `token`. */
export interface CreatedApiToken extends ApiToken {
  token: string
}

export const listTokens = () => api.get<ApiToken[]>('/api/tokens')
export const createToken = (name: string) => api.post<CreatedApiToken>('/api/tokens', { name })
export const deleteToken = (id: string) => api.del<void>(`/api/tokens/${id}`)

export interface Webhook {
  id: string
  name: string
  url: string
  secret: string
  createdAt: number
}

export const listWebhooks = () => api.get<Webhook[]>('/api/webhooks')
export const createWebhook = (name: string, url: string) =>
  api.post<Webhook>('/api/webhooks', { name, url })
export const deleteWebhook = (id: string) => api.del<void>(`/api/webhooks/${id}`)

/**
 * Where the browser downloads a CSV export from — a navigation (a plain
 * `<a href>`), not a fetch: the response already carries
 * `Content-Disposition: attachment`, so the browser does the rest.
 */
export const exportCsvUrl = (profile: 'native' | 'monefy') =>
  `/api/export/transactions.csv?profile=${profile}`

// --- Admin (root manages the others) ------------------------------------------------

/** One account as the admin users screen sees it — never a password or a device list. */
export interface AdminUser {
  id: string
  email: string
  displayName: string
  baseCurrency: string
  isAdmin: boolean
  createdAt: number
}

export const listUsers = () => api.get<AdminUser[]>('/api/admin/users')

/**
 * Provision an account directly, bypassing `registration` entirely — this is
 * the admin acting, not the public signing up. The new person is not signed
 * in by this call; they sign in themselves with the password given here.
 */
export const adminCreateUser = (email: string, password: string, displayName?: string) =>
  api.post<AdminUser>('/api/admin/users', { email, password, displayName })

export const adminDeleteUser = (id: string) => api.del<void>(`/api/admin/users/${id}`)

export const setAdmin = (id: string, isAdmin: boolean) =>
  api.put<AdminUser>(`/api/admin/users/${id}`, { isAdmin })

/**
 * The instance-wide settings an admin may change at runtime instead of
 * hand-editing config.yaml and restarting. Deliberately not everything in
 * config.yaml: `server.*` (transport config a running process cannot rebind
 * itself anyway) and `oidc.*` (can carry a client secret) stay
 * config.yaml/env-only — see backend/internal/config's package doc.
 */
export interface InstanceSettings {
  registration: string
  defaultCurrency: string
  sessionTtlDays: number
  changeLogRetentionDays: number
  fxEnabled: boolean
  fxRefreshAt: string
  /** Ordered — providers are tried in this order, first answer wins. */
  fxProviders: string[]
  /**
   * Every provider id the server accepts. Response-only; sending it back
   * changes nothing. Read this instead of hardcoding the set, or a provider
   * the frontend has not heard of gets stripped from config.yaml on the next
   * save.
   */
  fxProvidersAvailable: string[]
  /** Upper bound Validate enforces on sessionTtlDays. Response-only. */
  sessionTtlDaysMax: number
}

export const getSettings = () => api.get<InstanceSettings>('/api/admin/settings')

/** A PUT, not a PATCH: send the complete settings back with the one field changed. */
export const updateSettings = (settings: InstanceSettings) =>
  api.put<InstanceSettings>('/api/admin/settings', settings)

/** Where to send the browser to start an OIDC flow. Not a fetch — a navigation. */
export function oidcStartUrl(providerId: string, opts: { link?: boolean; redirect?: string } = {}) {
  const params = new URLSearchParams()
  if (opts.link) params.set('link', '1')
  if (opts.redirect) params.set('redirect', opts.redirect)
  const query = params.toString()
  return `/api/auth/oidc/${encodeURIComponent(providerId)}/start${query ? `?${query}` : ''}`
}
