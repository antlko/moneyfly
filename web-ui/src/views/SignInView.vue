<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { oidcStartUrl } from '@/api/http'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const email = ref('')
const password = ref('')
const displayName = ref('')
const busy = ref(false)
const error = ref('')

const canRegister = computed(() => auth.health?.registrationAllowed ?? false)
// Nobody has claimed this instance yet: there is nothing to sign in to, so show
// only the create form. This is the whole of the "setup screen" — a separate one
// would just be another thing to get out of sync with the registration rules.
const unclaimed = computed(() => auth.health?.claimed === false)

const mode = ref<'signin' | 'register'>('signin')

onMounted(() => {
  if (unclaimed.value) mode.value = 'register'
  // The OIDC callback redirects here with a message when something goes wrong;
  // it cannot render JSON into a top-level navigation.
  const fromRedirect = route.query.error
  if (typeof fromRedirect === 'string') error.value = fromRedirect
})

async function submit() {
  error.value = ''
  busy.value = true
  try {
    if (mode.value === 'register') {
      await auth.createAccount(email.value, password.value, displayName.value)
    } else {
      await auth.signIn(email.value, password.value)
    }
    const next = typeof route.query.next === 'string' ? route.query.next : '/'
    await router.replace(next)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="flex min-h-full flex-col items-center justify-center gap-6 bg-mf-bg px-6 py-12">
    <div class="flex flex-col items-center gap-3">
      <img src="/icon.svg" alt="" class="h-16 w-16" />
      <h1 class="text-2xl font-semibold tracking-tight text-mf-green-dark">moneyfly</h1>
      <p v-if="unclaimed" class="max-w-xs text-center text-sm text-mf-ink/70">
        Nobody has claimed this instance yet. Create the first account — it becomes the admin.
      </p>
    </div>

    <form
      class="flex w-full max-w-sm flex-col gap-4 rounded-2xl bg-mf-surface p-6 shadow-sm"
      @submit.prevent="submit"
    >
      <div v-if="canRegister && !unclaimed" class="flex rounded-full bg-mf-bg p-1 text-sm">
        <button
          type="button"
          class="flex-1 rounded-full py-1.5 transition"
          :class="mode === 'signin' ? 'bg-mf-green font-medium text-white' : 'text-mf-ink/70'"
          @click="mode = 'signin'"
        >
          Sign in
        </button>
        <button
          type="button"
          class="flex-1 rounded-full py-1.5 transition"
          :class="mode === 'register' ? 'bg-mf-green font-medium text-white' : 'text-mf-ink/70'"
          @click="mode = 'register'"
        >
          Create account
        </button>
      </div>

      <label class="flex flex-col gap-1 text-sm">
        <span class="text-mf-ink/70">Email</span>
        <input
          v-model="email"
          type="email"
          autocomplete="email"
          required
          class="rounded-lg border border-mf-muted/60 px-3 py-2 outline-none focus:border-mf-green"
        />
      </label>

      <label v-if="mode === 'register'" class="flex flex-col gap-1 text-sm">
        <span class="text-mf-ink/70">Name <span class="text-mf-muted">(optional)</span></span>
        <input
          v-model="displayName"
          type="text"
          autocomplete="name"
          class="rounded-lg border border-mf-muted/60 px-3 py-2 outline-none focus:border-mf-green"
        />
      </label>

      <label class="flex flex-col gap-1 text-sm">
        <span class="text-mf-ink/70">Password</span>
        <input
          v-model="password"
          type="password"
          :autocomplete="mode === 'register' ? 'new-password' : 'current-password'"
          required
          minlength="8"
          class="rounded-lg border border-mf-muted/60 px-3 py-2 outline-none focus:border-mf-green"
        />
      </label>

      <p v-if="error" role="alert" class="text-sm text-mf-red-text">{{ error }}</p>

      <button
        type="submit"
        :disabled="busy"
        class="rounded-full bg-mf-green py-2.5 font-medium text-white transition disabled:opacity-50"
      >
        {{ mode === 'register' ? 'Create account' : 'Sign in' }}
      </button>

      <template v-if="auth.providers.length">
        <div class="flex items-center gap-3 text-xs text-mf-muted">
          <span class="h-px flex-1 bg-mf-muted/40" />or<span class="h-px flex-1 bg-mf-muted/40" />
        </div>
        <!--
          A link, not a fetch: the provider redirects the browser back to our
          callback, which is what sets the session cookie.
        -->
        <a
          v-for="p in auth.providers"
          :key="p.id"
          :href="oidcStartUrl(p.id)"
          class="rounded-full border border-mf-green py-2.5 text-center font-medium text-mf-green-dark"
        >
          Continue with {{ p.name }}
        </a>
      </template>
    </form>

    <p v-if="!canRegister && mode === 'signin'" class="text-xs text-mf-muted">
      Registration is closed on this instance.
    </p>
  </main>
</template>
