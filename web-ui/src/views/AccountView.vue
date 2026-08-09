<script setup lang="ts">
import { browserSupportsWebAuthn } from '@simplewebauthn/browser'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import * as http from '@/api/http'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { platform } from '@/lib/device'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()

const devices = ref<http.Device[]>([])
const currentPassword = ref('')
const newPassword = ref('')
const message = ref('')
const error = ref('')

// A provider is offered for linking only if it is not linked already — the
// server would reject a second link anyway, and a dead button is worse.
const linkable = computed(() =>
  auth.providers.filter((p) => !auth.user?.identities.some((i) => i.provider === p.id)),
)

/*
 * The passkey section is hidden entirely when it could not work — this browser
 * has no WebAuthn, or this instance has no relying party configured — rather
 * than shown with a button that always fails. Unlike the providers section
 * above it is not gated on anything being configured *per provider*: passkeys
 * are a capability, not a list.
 */
const passkeySupported = browserSupportsWebAuthn()
const canUsePasskeys = computed(
  () => passkeySupported && (auth.health?.webauthnEnabled ?? false),
)

const passkeys = ref<http.Passkey[]>([])
const newPasskeyName = ref(platform())
const addingPasskey = ref(false)

onMounted(async () => {
  await loadDevices()
  await loadPasskeys()
})

async function loadDevices() {
  try {
    devices.value = await http.getDevices()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

async function loadPasskeys() {
  if (!canUsePasskeys.value) return
  try {
    passkeys.value = await http.listPasskeys()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

async function addPasskey() {
  addingPasskey.value = true
  try {
    await run(async () => {
      await auth.registerPasskey(newPasskeyName.value.trim() || platform())
      await loadPasskeys()
      newPasskeyName.value = platform()
    }, 'Passkey added. You can now sign in with it.')
  } finally {
    addingPasskey.value = false
  }
}

const removePasskey = (id: string) =>
  run(async () => {
    await http.deletePasskey(id)
    await loadPasskeys()
    await auth.refresh()
  }, 'Passkey removed.')

async function run(action: () => Promise<unknown>, ok: string) {
  message.value = ''
  error.value = ''
  try {
    await action()
    message.value = ok
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

const savePassword = () =>
  run(async () => {
    await http.changePassword(currentPassword.value, newPassword.value)
    currentPassword.value = ''
    newPassword.value = ''
    await auth.refresh()
    await loadDevices() // other sessions were revoked, so the list changed
  }, 'Password updated. Other devices have been signed out.')

const unlink = (id: string) =>
  run(async () => {
    await http.unlinkIdentity(id)
    await auth.refresh()
  }, 'Provider unlinked.')

const forget = (id: string) =>
  run(async () => {
    await http.forgetDevice(id)
    await loadDevices()
  }, 'Device forgotten. It will re-sync from scratch if it comes back.')

async function signOut() {
  await auth.signOut()
  await router.replace('/signin')
}

const when = (unix: number) => (unix ? new Date(unix * 1000).toLocaleString() : '—')
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Account" />

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <p v-if="message" class="rounded-lg bg-mf-green-soft/40 p-3 text-sm text-mf-green-dark">
        {{ message }}
      </p>
      <p v-if="error" role="alert" class="rounded-lg bg-mf-red/20 p-3 text-sm text-mf-red-text">
        {{ error }}
      </p>

      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-2 font-medium">{{ auth.user?.displayName }}</h2>
        <p class="text-sm text-mf-muted">{{ auth.user?.email }}</p>
        <p class="text-sm text-mf-muted">Base currency: {{ auth.user?.baseCurrency }}</p>
      </section>

      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-3 font-medium">
          {{ auth.user?.hasPassword ? 'Change password' : 'Set a password' }}
        </h2>
        <form class="flex flex-col gap-3" @submit.prevent="savePassword">
          <input
            v-if="auth.user?.hasPassword"
            v-model="currentPassword"
            type="password"
            placeholder="Current password"
            autocomplete="current-password"
            class="rounded-lg border border-mf-muted/60 px-3 py-2 text-sm outline-none focus:border-mf-green"
          />
          <input
            v-model="newPassword"
            type="password"
            placeholder="New password"
            autocomplete="new-password"
            minlength="8"
            required
            class="rounded-lg border border-mf-muted/60 px-3 py-2 text-sm outline-none focus:border-mf-green"
          />
          <button
            type="submit"
            class="self-start rounded-full bg-mf-green px-5 py-2 text-sm font-medium text-white"
          >
            Save
          </button>
        </form>
      </section>

      <section v-if="auth.providers.length" class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-3 font-medium">Sign-in providers</h2>
        <ul class="mb-3 space-y-2">
          <li
            v-for="identity in auth.user?.identities ?? []"
            :key="identity.id"
            class="flex items-center justify-between text-sm"
          >
            <span>
              {{ identity.provider }}
              <span class="text-mf-muted">· {{ identity.email }}</span>
            </span>
            <button type="button" class="text-mf-red-text" @click="unlink(identity.id)">
              Unlink
            </button>
          </li>
          <li v-if="!auth.user?.identities.length" class="text-sm text-mf-muted">
            No providers linked.
          </li>
        </ul>
        <a
          v-for="p in linkable"
          :key="p.id"
          :href="http.oidcStartUrl(p.id, { link: true, redirect: '/account' })"
          class="mr-2 inline-block rounded-full border border-mf-green px-4 py-1.5 text-sm text-mf-green-dark"
        >
          Link {{ p.name }}
        </a>
      </section>

      <section v-if="canUsePasskeys" class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-1 font-medium">Passkeys</h2>
        <p class="mb-3 text-sm text-mf-muted">
          Sign in with your fingerprint, face or device PIN instead of a password. Each device you
          add gets its own — removing one here never affects the others.
        </p>

        <ul class="mb-3 space-y-3">
          <li v-for="k in passkeys" :key="k.id" class="flex items-start justify-between text-sm">
            <div>
              <p>{{ k.name }}</p>
              <p class="text-xs text-mf-muted">
                Added {{ when(k.createdAt) }} ·
                {{ k.lastUsedAt ? `last used ${when(k.lastUsedAt)}` : 'never used' }}
              </p>
            </div>
            <button type="button" class="text-mf-red-text" @click="removePasskey(k.id)">
              Remove
            </button>
          </li>
          <li v-if="!passkeys.length" class="text-sm text-mf-muted">No passkeys yet.</li>
        </ul>

        <form class="flex items-center gap-2" @submit.prevent="addPasskey">
          <input
            v-model="newPasskeyName"
            type="text"
            placeholder="Name this device"
            maxlength="60"
            class="min-w-0 flex-1 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-1.5 text-sm outline-none focus:border-mf-green"
          />
          <button
            type="submit"
            :disabled="addingPasskey"
            class="shrink-0 rounded-full border border-mf-green px-4 py-1.5 text-sm text-mf-green-dark disabled:opacity-50"
          >
            {{ addingPasskey ? 'Waiting…' : 'Add a passkey' }}
          </button>
        </form>
      </section>

      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-3 font-medium">Devices</h2>
        <ul class="space-y-3">
          <li v-for="d in devices" :key="d.id" class="flex items-start justify-between text-sm">
            <div>
              <p>
                {{ d.platform || 'Unknown device' }}
                <span v-if="d.current" class="text-mf-green-dark">· this device</span>
              </p>
              <p class="text-xs text-mf-muted">Last seen {{ when(d.lastSeenAt) }}</p>
            </div>
            <button v-if="!d.current" type="button" class="text-mf-red-text" @click="forget(d.id)">
              Forget
            </button>
          </li>
        </ul>
      </section>

      <button
        type="button"
        class="w-full rounded-full border border-mf-red py-2.5 text-sm font-medium text-mf-red-text"
        @click="signOut"
      >
        Sign out
      </button>
    </main>
  </div>
</template>
