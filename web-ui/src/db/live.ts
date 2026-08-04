import { liveQuery, type Subscription } from 'dexie'
import { onScopeDispose, ref, watch, type Ref } from 'vue'

/**
 * A Dexie query as a reactive ref.
 *
 * Dexie re-runs the query whenever anything it read changes — including from
 * another tab, over a BroadcastChannel. So a screen written against this stays
 * correct when the sync engine applies a change underneath it, with no manual
 * invalidation anywhere.
 *
 * `deps` covers the other direction. Dexie watches the *database*, not your
 * component state, so a query that closes over a ref (the selected month, say)
 * would never re-run when that ref changed. Pass a getter for whatever the query
 * reads from outside the database and the subscription is rebuilt when it moves.
 */
export function useLiveQuery<T>(
  querier: () => Promise<T>,
  initial: T,
  deps?: () => unknown,
): Ref<T> {
  const value = ref(initial) as Ref<T>
  let subscription: Subscription | null = null

  const subscribe = () => {
    subscription?.unsubscribe()
    subscription = liveQuery(querier).subscribe({
      next: (result) => {
        value.value = result
      },
      error: (err) => {
        console.error('live query failed', err)
      },
    })
  }

  subscribe()
  if (deps) watch(deps, subscribe)
  onScopeDispose(() => subscription?.unsubscribe())

  return value
}
