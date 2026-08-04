/**
 * Strip anything IndexedDB cannot store.
 *
 * IndexedDB writes go through the structured clone algorithm, which throws on
 * exotic objects — and a Vue `reactive()`/`ref()` value **is** one: a Proxy.
 * That is not a theoretical hazard. Handing `user.value` (a deep `ref`, so the
 * getter returns a proxy) straight to `meta.put` failed registration with
 *
 *   Failed to execute 'put' on 'IDBObjectStore': #<Object> could not be cloned.
 *
 * and, because the throw happened after the account was created server-side,
 * left the user staring at an error on a form they had already completed.
 *
 * `toRaw` is not enough: it unwraps the outer proxy only, so a nested reactive
 * array or object survives it. A JSON round-trip is total — it flattens every
 * proxy at every depth and drops functions and `undefined` members, which is
 * exactly the shape a stored row should have anyway. Every value that reaches
 * the replica is already JSON (an API response or an object literal), so
 * nothing meaningful is lost; a `Date` would become a string, which is why
 * dates are stored as `YYYY-MM-DD` strings throughout.
 */
export function plain<T>(value: T): T {
  if (value === null || typeof value !== 'object') return value
  return JSON.parse(JSON.stringify(value)) as T
}
