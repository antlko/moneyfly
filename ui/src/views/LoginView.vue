<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const email = ref('')
const password = ref('')
const error = ref('')
const retryAfter = ref<number | null>(null)
const busy = ref(false)

async function submit() {
  error.value = ''
  retryAfter.value = null
  busy.value = true
  try {
    await auth.login(email.value, password.value)
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/budget'
    await router.replace(auth.user?.must_change_password ? '/change-password' : redirect)
  } catch (err) {
    if (err instanceof ApiError) {
      // The server deliberately does not say whether the address exists.
      error.value =
        err.status === 429
          ? 'Too many attempts. Wait a moment and try again.'
          : 'That email and password combination was not accepted.'
      retryAfter.value = err.retryAfter
    } else {
      error.value = 'Could not reach the server.'
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto mt-12 max-w-sm">
    <h1 class="mb-1 text-3xl font-bold tracking-tight">MoneyApp</h1>
    <p class="mb-8 text-sm text-slate-500 dark:text-slate-400">
      Self-hosted budget and net-worth tracker.
    </p>

    <form class="space-y-4" @submit.prevent="submit">
      <div>
        <label for="email" class="mb-1 block text-sm font-medium">Email</label>
        <input
          id="email"
          v-model="email"
          type="email"
          autocomplete="username"
          required
          class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
        />
      </div>
      <div>
        <label for="password" class="mb-1 block text-sm font-medium">Password</label>
        <input
          id="password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
          class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
        />
      </div>

      <p
        v-if="error"
        class="rounded-xl bg-state-severe/10 px-3 py-2 text-sm text-state-severe"
        role="alert"
      >
        {{ error }}
        <span v-if="retryAfter"> Try again in {{ retryAfter }}s.</span>
      </p>

      <button
        type="submit"
        class="tap-target w-full rounded-xl bg-brand-600 px-4 py-3 font-semibold text-white hover:bg-brand-700 disabled:opacity-60"
        :disabled="busy"
      >
        {{ busy ? 'Signing in…' : 'Sign in' }}
      </button>
    </form>

    <p class="mt-8 text-xs text-slate-400">
      Accounts are created by an administrator; there is no public sign-up.
    </p>
    <p v-if="auth.build" class="mt-2 text-xs text-slate-400">
      Backend <span class="font-mono">{{ auth.build.version }}</span> (<span class="font-mono">{{
        auth.build.commit
      }}</span
      >)
    </p>
  </div>
</template>
