import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { ApiError } from '@/api/http'
import * as http from '@/api/http'
import { db, META_PROFILE, resetReplica } from '@/db'
import { deviceId, platform } from '@/lib/device'
import { sync } from '@/sync/engine'

/**
 * Who is signed in, and what the instance offers before they are.
 *
 * `ready` matters more than it looks: the router guard must not redirect to the
 * sign-in screen while the session is still being checked, or a hard refresh on
 * any page bounces the user to /signin and loses where they were.
 */
export const useAuthStore = defineStore('auth', () => {
  const user = ref<http.User | null>(null)
  const health = ref<http.Health | null>(null)
  const ready = ref(false)
  /** Signed in from the cached profile because the server could not be reached. */
  const offline = ref(false)

  const isSignedIn = computed(() => user.value !== null)
  const providers = computed(() => health.value?.oidcProviders ?? [])

  /** In-flight bootstrap, so concurrent navigations await one, not several. */
  let inflight: Promise<void> | null = null

  /**
   * Resolve the current session, and get out of the way.
   *
   * This is the one thing the router guard awaits before rendering anything, so
   * whatever it does is the app's time-to-first-pixel. It used to await two
   * network round trips — which meant an installed PWA opened on a bad
   * connection showed nothing at all until they resolved or hit the 15s
   * timeout, on an app whose entire ledger was already on the device. The
   * network is not allowed to be on that path.
   *
   * So: the device answers first. If this replica has a cached profile, that is
   * what renders, immediately, off one IndexedDB read; the server is then asked
   * in the background and reconciled. Only a first-ever launch has nothing to
   * show and genuinely has to wait.
   *
   * Reconciling is where the care goes, because the two failure modes are
   * **not** the same and conflating them is what makes an offline-first app
   * useless offline:
   *
   *   * **401** — the server answered, and the answer is "you are not signed
   *     in". Clear everything and show the sign-in screen.
   *   * **anything else** — no answer at all (a tunnel, a dead server,
   *     aeroplane mode), or a 500, or a timeout. Keep the cached account and
   *     carry on. Treating this as a sign-out puts a login form in front of
   *     data the user already has, with no way to get past it.
   */
  function bootstrap(): Promise<void> {
    if (ready.value) return Promise.resolve()
    inflight ??= run().finally(() => {
      inflight = null
    })
    return inflight
  }

  async function run() {
    const cached = await db.getMeta<http.User | null>(META_PROFILE, null)
    if (cached) {
      // Render now, verify later. `offline` stays false for the moment — it
      // means "the server could not be reached", which is not yet known and
      // usually will not be true; revalidate sets it if the check fails.
      user.value = cached
      ready.value = true
      void sync.start(cached.id)
      void revalidate()
      return
    }
    await revalidate()
    ready.value = true
    if (user.value) void sync.start(user.value.id)
  }

  async function revalidate() {
    const [healthResult, meResult] = await Promise.allSettled([http.getHealth(), http.getMe()])

    if (healthResult.status === 'fulfilled') health.value = healthResult.value

    if (meResult.status === 'fulfilled') {
      user.value = meResult.value
      offline.value = false
      await db.setMeta(META_PROFILE, meResult.value)
      return
    }
    // Only "not signed in" ends a session. A 500 is the server having a bad
    // day, not a statement about this cookie, and signing someone out over one
    // would be indistinguishable to them from losing their data.
    if (meResult.reason instanceof ApiError && meResult.reason.status === 401) {
      user.value = null
      offline.value = false
      await db.setMeta(META_PROFILE, null)
      return
    }
    // Unreachable. Whatever was cached stays; `offline` says why.
    offline.value = user.value !== null
  }

  function credentials(email: string, password: string, displayName?: string): http.Credentials {
    return { email, password, displayName, deviceId: deviceId(), platform: platform() }
  }

  async function signIn(email: string, password: string) {
    user.value = await http.login(credentials(email, password))
    await db.setMeta(META_PROFILE, user.value)
    void sync.start(user.value.id)
  }

  async function createAccount(email: string, password: string, displayName?: string) {
    user.value = await http.register(credentials(email, password, displayName))
    // A newly claimed instance stops offering registration to the next visitor.
    health.value = await http.getHealth()
    await db.setMeta(META_PROFILE, user.value)
    void sync.start(user.value.id)
  }

  /**
   * Sign out, and clear the local replica — unless there is unsent work.
   *
   * The trade is deliberate. Leaving someone's ledger in a shared browser is a
   * privacy problem, so the default is to wipe. But wiping expenses recorded
   * offline would destroy the only copy, and that is worse. If the final flush
   * cannot drain the outbox, the replica stays; signing in as a different
   * account resets it anyway (`SyncEngine.start`).
   */
  async function signOut() {
    try {
      await sync.sync()
    } catch {
      /* offline — handled by the pending check below */
    }
    const unsent = await db.outbox.count()
    sync.stop()

    await http.logout()
    user.value = null
    await db.setMeta(META_PROFILE, null)

    if (unsent === 0) {
      await resetReplica()
    } else {
      console.warn(`sync: keeping the local replica, ${unsent} operations are still unsent`)
    }
  }

  /** Re-read the account after a change that the server owns (linking, password). */
  async function refresh() {
    try {
      user.value = await http.getMe()
    } catch {
      user.value = null
    }
  }

  return {
    user,
    health,
    ready,
    offline,
    isSignedIn,
    providers,
    bootstrap,
    signIn,
    createAccount,
    signOut,
    refresh,
  }
})
