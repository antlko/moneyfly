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

  /**
   * Load instance facts and resolve the current session. Safe to call twice.
   *
   * The two failure modes are **not** the same, and conflating them is what
   * makes an offline-first app useless offline:
   *
   *   * **401** — the server answered, and the answer is "you are not signed
   *     in". Clear everything and show the sign-in screen.
   *   * **no answer at all** — a tunnel, a dead server, aeroplane mode. Fall
   *     back to the account cached on this device and carry on, because the
   *     whole ledger is already in IndexedDB. Treating this as a sign-out puts
   *     a login form in front of data the user already has, with no way to get
   *     past it.
   */
  async function bootstrap() {
    if (ready.value) return
    const [healthResult, meResult] = await Promise.allSettled([http.getHealth(), http.getMe()])

    if (healthResult.status === 'fulfilled') health.value = healthResult.value

    if (meResult.status === 'fulfilled') {
      user.value = meResult.value
      await db.setMeta(META_PROFILE, meResult.value)
    } else if (meResult.reason instanceof ApiError) {
      // The server spoke. Believe it.
      user.value = null
      await db.setMeta(META_PROFILE, null)
    } else {
      user.value = await db.getMeta<http.User | null>(META_PROFILE, null)
      offline.value = user.value !== null
    }

    ready.value = true
    if (user.value) void sync.start(user.value.id)
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
