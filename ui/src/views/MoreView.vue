<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { readChoice, setChoice, type ThemeChoice } from '@/lib/theme'
import { useAuthStore } from '@/stores/auth'

interface SessionRow {
  id: string
  user_agent: string
  created_at: string
  expires_at: string
  current: boolean
}

const auth = useAuthStore()
const router = useRouter()
const theme = ref<ThemeChoice>(readChoice())
const sessions = ref<SessionRow[]>([])

const links = [
  { to: '/import', label: 'Import', hint: 'a Monefy CSV export — preview, map, commit, undo' },
  { to: '/settings', label: 'Settings', hint: 'rates, prices, thresholds — manual or auto' },
  { to: '/settings/categories', label: 'Categories', hint: 'names, icons, essential flag, order' },
  { to: '/settings/accounts', label: 'Accounts', hint: 'asset class, liquid, net worth' },
  { to: '/settings/plan', label: 'Plan', hint: 'one figure per category, applied to a range' },
  { to: '/settings/aliases', label: 'Import aliases', hint: 'source names from Monefy exports' },
]

function pickTheme(choice: ThemeChoice) {
  theme.value = choice
  setChoice(choice)
}

async function loadSessions() {
  sessions.value = await api.get<SessionRow[]>('/api/v1/auth/sessions')
}

async function revoke(id: string) {
  await api.delete(`/api/v1/auth/sessions/${id}`)
  await loadSessions()
}

async function signOut() {
  await auth.logout()
  await router.replace('/login')
}

onMounted(loadSessions)
</script>

<template>
  <section>
    <h1 class="mb-4 text-lg font-semibold">More</h1>

    <ul class="mb-6 space-y-1">
      <li v-for="link in links" :key="link.to">
        <RouterLink
          :to="link.to"
          class="tap-target flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
        >
          <span class="flex-1">
            <span class="block font-medium">{{ link.label }}</span>
            <span class="block text-xs text-slate-400">{{ link.hint }}</span>
          </span>
          <span aria-hidden="true" class="text-slate-400">›</span>
        </RouterLink>
      </li>
    </ul>

    <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-500">Appearance</h2>
    <div class="mb-6 flex gap-1 rounded-xl bg-slate-100 p-1 text-sm dark:bg-slate-800" role="group">
      <button
        v-for="choice in ['system', 'light', 'dark'] as ThemeChoice[]"
        :key="choice"
        type="button"
        class="tap-target flex-1 rounded-lg px-3 py-1.5 capitalize"
        :class="
          theme === choice ? 'bg-white font-semibold shadow dark:bg-slate-700' : 'text-slate-500'
        "
        :aria-pressed="theme === choice"
        @click="pickTheme(choice)"
      >
        {{ choice }}
      </button>
    </div>

    <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-500">Account</h2>
    <dl class="mb-3 space-y-1 text-sm">
      <div class="flex justify-between">
        <dt class="text-slate-500">Signed in as</dt>
        <dd>{{ auth.user?.email }}</dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-slate-500">Role</dt>
        <dd>{{ auth.user?.role }}</dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-slate-500">Base currency</dt>
        <dd>{{ auth.user?.base_currency }}</dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-slate-500">Fiscal year starts</dt>
        <dd>month {{ auth.user?.fiscal_year_start_month }}</dd>
      </div>
    </dl>

    <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-400">Sessions</h3>
    <ul class="mb-3 space-y-1">
      <li
        v-for="session in sessions"
        :key="session.id"
        class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs dark:border-slate-800 dark:bg-slate-900"
      >
        <span class="flex-1 truncate">
          {{ session.user_agent || 'unknown device' }}
          <span v-if="session.current" class="ml-1 font-semibold text-brand-600">this device</span>
        </span>
        <button
          v-if="!session.current"
          type="button"
          class="tap-target px-2 text-state-severe underline"
          @click="revoke(session.id)"
        >
          revoke
        </button>
      </li>
    </ul>

    <RouterLink to="/change-password" class="text-sm text-brand-600 underline dark:text-brand-300">
      Change password
    </RouterLink>

    <button
      type="button"
      class="tap-target mt-4 w-full rounded-xl border border-slate-300 px-4 py-2.5 text-sm dark:border-slate-700"
      @click="signOut"
    >
      Sign out
    </button>

    <p v-if="auth.build" class="mt-8 text-center text-xs text-slate-400">
      MoneyApp <span class="font-mono">{{ auth.build.version }}</span> ·
      <span class="font-mono">{{ auth.build.commit }}</span>
    </p>
  </section>
</template>
