<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { pushToast } from '@/lib/toast'

const auth = useAuthStore()
const router = useRouter()

const current = ref('')
const next = ref('')
const confirm = ref('')
const fieldErrors = ref<Record<string, string>>({})
const error = ref('')
const busy = ref(false)

async function submit() {
  error.value = ''
  fieldErrors.value = {}
  if (next.value !== confirm.value) {
    fieldErrors.value = { new_password: 'The two entries do not match.' }
    return
  }
  busy.value = true
  try {
    await auth.changePassword(current.value, next.value)
    pushToast('Password changed. Other sessions were signed out.', 'success')
    await router.replace('/budget')
  } catch (err) {
    if (err instanceof ApiError) {
      fieldErrors.value = err.fieldErrors
      error.value = Object.keys(err.fieldErrors).length ? '' : err.message
    } else {
      error.value = 'Could not reach the server.'
    }
  } finally {
    busy.value = false
  }
}

async function signOut() {
  await auth.logout()
  await router.replace('/login')
}
</script>

<template>
  <div class="mx-auto mt-12 max-w-sm">
    <h1 class="mb-1 text-2xl font-bold">Choose a new password</h1>
    <p class="mb-6 text-sm text-slate-500 dark:text-slate-400">
      This account still has its initial password. Nothing else is reachable until it is changed.
      Every other session will be signed out.
    </p>

    <form class="space-y-4" @submit.prevent="submit">
      <div>
        <label for="current" class="mb-1 block text-sm font-medium">Current password</label>
        <input
          id="current"
          v-model="current"
          type="password"
          autocomplete="current-password"
          required
          class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
        />
        <p v-if="fieldErrors.current_password" class="mt-1 text-sm text-state-severe" role="alert">
          {{ fieldErrors.current_password }}
        </p>
      </div>
      <div>
        <label for="next" class="mb-1 block text-sm font-medium">New password</label>
        <input
          id="next"
          v-model="next"
          type="password"
          autocomplete="new-password"
          minlength="8"
          required
          class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
        />
        <p v-if="fieldErrors.new_password" class="mt-1 text-sm text-state-severe" role="alert">
          {{ fieldErrors.new_password }}
        </p>
      </div>
      <div>
        <label for="confirm" class="mb-1 block text-sm font-medium">Repeat new password</label>
        <input
          id="confirm"
          v-model="confirm"
          type="password"
          autocomplete="new-password"
          required
          class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
        />
      </div>

      <p v-if="error" class="text-sm text-state-severe" role="alert">{{ error }}</p>

      <button
        type="submit"
        class="tap-target w-full rounded-xl bg-brand-600 px-4 py-3 font-semibold text-white hover:bg-brand-700 disabled:opacity-60"
        :disabled="busy"
      >
        {{ busy ? 'Saving…' : 'Change password' }}
      </button>
    </form>

    <button
      type="button"
      class="tap-target mt-6 w-full text-sm text-slate-500 underline"
      @click="signOut"
    >
      Sign out instead
    </button>
  </div>
</template>
