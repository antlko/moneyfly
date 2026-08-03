/**
 * The single HTTP entry point. No component calls fetch directly; components are
 * dumb, stores hold state (docs/implementation-plan/00-conventions.md §10).
 */

export const API_BASE = '/api/v1'

/** ProblemDetail is the RFC 7807 body every error response carries. */
export interface ProblemDetail {
  type: string
  title: string
  status: number
  detail?: string
  errors?: { field: string; message: string }[]
  request_id?: string
}

/** ApiError carries the problem document so a form can show field-level messages. */
export class ApiError extends Error {
  readonly status: number
  readonly problem: ProblemDetail | null
  readonly retryAfter: number | null

  constructor(status: number, problem: ProblemDetail | null, retryAfter: number | null = null) {
    super(problem?.detail || problem?.title || `Request failed with status ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.problem = problem
    this.retryAfter = retryAfter
  }

  /** fieldErrors maps a field name to its message, for inline form validation. */
  get fieldErrors(): Record<string, string> {
    const out: Record<string, string> = {}
    for (const e of this.problem?.errors ?? []) out[e.field] = e.message
    return out
  }

  get isUnauthorized(): boolean {
    return this.status === 401
  }

  /** True while the initial password has not been changed. */
  get isForbidden(): boolean {
    return this.status === 403
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  headers?: Record<string, string>
  signal?: AbortSignal
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { ...(opts.headers ?? {}) }
  let body: string | undefined
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }

  const response = await fetch(path.startsWith('/') ? path : `${API_BASE}/${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body,
    // The session lives in a host-only cookie, so credentials must be sent.
    credentials: 'same-origin',
    signal: opts.signal,
  })

  if (response.status === 204) return undefined as T
  const text = await response.text()

  if (!response.ok) {
    let problem: ProblemDetail | null = null
    try {
      problem = text ? (JSON.parse(text) as ProblemDetail) : null
    } catch {
      problem = null
    }
    const retryAfter = response.headers.get('Retry-After')
    throw new ApiError(
      response.status,
      problem,
      retryAfter ? Number.parseInt(retryAfter, 10) : null,
    )
  }
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, body?: unknown, headers?: Record<string, string>) =>
    request<T>(path, { method: 'POST', body, headers }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PATCH', body }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}

/**
 * Builds an Idempotency-Key so a retried optimistic save cannot become two
 * transactions.
 */
export function idempotencyKey(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}
