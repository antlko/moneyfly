<script setup lang="ts">
import { onMounted, ref } from 'vue'

import * as http from '@/api/http'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { useAuthStore } from '@/stores/auth'

/**
 * Root manages the others: every account on this instance, in one place.
 * Only reachable by an admin — the router guard sends anyone else to `/`
 * (see router/index.ts's `adminOnly` meta) — and this screen never renders
 * for a non-admin even transiently, since it is only ever linked to from
 * navigation that is itself conditional on `auth.user.isAdmin`.
 */
const auth = useAuthStore()

const users = ref<http.AdminUser[]>([])
const message = ref('')
const error = ref('')
const creating = ref(false)
const newEmail = ref('')
const newPassword = ref('')
const newName = ref('')

onMounted(load)

async function load() {
  users.value = await http.listUsers()
}

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

async function createUser() {
  await run(async () => {
    await http.adminCreateUser(newEmail.value.trim(), newPassword.value, newName.value.trim())
    newEmail.value = ''
    newPassword.value = ''
    newName.value = ''
    creating.value = false
    await load()
  }, 'Account created. Share the password with them directly — it will not be shown again here.')
}

const toggleAdmin = (u: http.AdminUser) =>
  run(async () => {
    await http.setAdmin(u.id, !u.isAdmin)
    await load()
  }, u.isAdmin ? `${u.email} is no longer an admin.` : `${u.email} is now an admin.`)

const removeUser = (u: http.AdminUser) =>
  run(async () => {
    await http.adminDeleteUser(u.id)
    await load()
  }, `${u.email} and everything they recorded has been removed.`)

const when = (unix: number) => new Date(unix * 1000).toLocaleDateString()
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Users" />

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <p v-if="message" class="rounded-lg bg-mf-green-soft/40 p-3 text-sm text-mf-green-dark">
        {{ message }}
      </p>
      <p v-if="error" role="alert" class="rounded-lg bg-mf-red/20 p-3 text-sm text-mf-red-text">
        {{ error }}
      </p>

      <section class="rounded-2xl bg-mf-surface p-4">
        <ul class="divide-y divide-mf-muted/20">
          <li v-for="u in users" :key="u.id" class="flex items-center gap-3 py-3 text-sm">
            <div class="min-w-0 flex-1">
              <p class="flex items-center gap-2 truncate">
                {{ u.displayName || u.email }}
                <span v-if="u.isAdmin" class="rounded-full bg-mf-green-soft/40 px-2 py-0.5 text-xs text-mf-green-dark">
                  admin
                </span>
                <span v-if="u.id === auth.user?.id" class="text-xs text-mf-muted">(you)</span>
              </p>
              <p class="text-xs text-mf-muted">{{ u.email }} · joined {{ when(u.createdAt) }}</p>
            </div>
            <button
              type="button"
              class="text-xs text-mf-green-dark"
              @click="toggleAdmin(u)"
            >
              {{ u.isAdmin ? 'Demote' : 'Promote' }}
            </button>
            <button
              v-if="u.id !== auth.user?.id"
              type="button"
              class="text-xs text-mf-red-text"
              @click="removeUser(u)"
            >
              Delete
            </button>
          </li>
        </ul>
      </section>

      <section class="rounded-2xl bg-mf-surface p-4">
        <button
          v-if="!creating"
          type="button"
          class="w-full rounded-full bg-mf-green py-2.5 text-sm font-medium text-white"
          @click="creating = true"
        >
          + Create an account
        </button>

        <form v-else class="space-y-3" @submit.prevent="createUser">
          <h2 class="font-medium">New account</h2>
          <p class="text-sm text-mf-muted">
            Provisioned immediately — no invitation, no self-registration. Give them the password
            directly; it is not shown again after this.
          </p>
          <input
            v-model="newEmail"
            type="email"
            placeholder="Email"
            required
            class="w-full rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 text-sm outline-none focus:border-mf-green"
          />
          <input
            v-model="newName"
            type="text"
            placeholder="Name (optional)"
            class="w-full rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 text-sm outline-none focus:border-mf-green"
          />
          <input
            v-model="newPassword"
            type="text"
            placeholder="Password (at least 8 characters)"
            required
            minlength="8"
            class="w-full rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 text-sm outline-none focus:border-mf-green"
          />
          <div class="flex gap-3">
            <button
              type="button"
              class="flex-1 rounded-full border border-mf-muted py-2.5 text-sm"
              @click="creating = false"
            >
              Cancel
            </button>
            <button type="submit" class="flex-1 rounded-full bg-mf-green py-2.5 text-sm font-medium text-white">
              Create
            </button>
          </div>
        </form>
      </section>
    </main>
  </div>
</template>
