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

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (e) {
    // Everything inside this try is one call, so there is nothing else here that
    // could throw and be mislabelled.
    throw new NetworkError(e)
  }

  if (!res.ok) {
    // The server renders every error as {"error": "..."} via its central
    // ErrorHandler; anything else means we did not reach the API at all.
    let message = res.statusText
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      /* non-JSON body — keep the status text */
    }
    throw new ApiError(res.status, message)
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

/** Where to send the browser to start an OIDC flow. Not a fetch — a navigation. */
export function oidcStartUrl(providerId: string, opts: { link?: boolean; redirect?: string } = {}) {
  const params = new URLSearchParams()
  if (opts.link) params.set('link', '1')
  if (opts.redirect) params.set('redirect', opts.redirect)
  const query = params.toString()
  return `/api/auth/oidc/${encodeURIComponent(providerId)}/start${query ? `?${query}` : ''}`
}
