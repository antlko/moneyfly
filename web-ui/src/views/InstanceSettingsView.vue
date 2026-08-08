<script setup lang="ts">
import { onMounted, ref } from 'vue'

import * as http from '@/api/http'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'

/**
 * Instance-wide settings — the config.yaml fields an admin may now change
 * without hand-editing the file and restarting. Only reachable by an admin
 * (see router/index.ts's `adminOnly` meta and, underneath it, the server's
 * own adminMW — the route guard is a clean redirect, not the boundary).
 *
 * Deliberately smaller than config.yaml itself: `server.*` and `oidc.*` stay
 * file/env-only. See backend/internal/config's package doc for why —
 * `server.addr`/`base_url` are transport config a running process cannot
 * rebind itself anyway, and an OIDC client secret must never round-trip
 * through a write endpoint.
 */

// The fixed set of provider ids `fx.providers` accepts — mirrors
// backend/internal/config's FXProviders. Small and stable enough that
// fetching it from an endpoint would be a round trip for two constants.
const FX_PROVIDERS: { id: string; label: string }[] = [
  { id: 'open-er-api', label: 'open.er-api.com' },
  { id: 'fawazahmed0', label: 'fawazahmed0/currency-api' },
]

const settings = ref<http.InstanceSettings | null>(null)
const busy = ref(false)
const message = ref('')
const error = ref('')

onMounted(load)

async function load() {
  error.value = ''
  try {
    settings.value = await http.getSettings()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

function toggleProvider(id: string, on: boolean) {
  if (!settings.value) return
  const set = new Set(settings.value.fxProviders)
  if (on) set.add(id)
  else set.delete(id)
  // FX_PROVIDERS order, not insertion order — providers are tried in the
  // order they are stored, and that order should stay predictable rather
  // than depending on which checkbox happened to be clicked last.
  settings.value.fxProviders = FX_PROVIDERS.map((p) => p.id).filter((id) => set.has(id))
}

async function save() {
  if (!settings.value) return
  busy.value = true
  message.value = ''
  error.value = ''
  try {
    settings.value = await http.updateSettings(settings.value)
    message.value = 'Settings saved and applied — no restart needed.'
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Instance settings" />

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <p v-if="message" class="rounded-lg bg-mf-green-soft/40 p-3 text-sm text-mf-green-dark">
        {{ message }}
      </p>
      <p v-if="error" role="alert" class="rounded-lg bg-mf-red/20 p-3 text-sm text-mf-red-text">
        {{ error }}
      </p>

      <form v-if="settings" class="space-y-4" @submit.prevent="save">
        <section class="space-y-3 rounded-2xl bg-mf-surface p-4">
          <h2 class="font-medium">Accounts</h2>

          <label class="block text-sm">
            <span class="mb-1 block text-mf-muted">Registration</span>
            <select
              v-model="settings.registration"
              class="w-full rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 outline-none focus:border-mf-green"
            >
              <option value="open">Open — anyone who can reach this instance may sign up</option>
              <option value="closed">Closed — no new accounts except by an admin</option>
            </select>
          </label>

          <label class="block text-sm">
            <span class="mb-1 block text-mf-muted">Default currency for a new account</span>
            <input
              v-model="settings.defaultCurrency"
              type="text"
              maxlength="3"
              required
              class="w-24 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 uppercase outline-none focus:border-mf-green"
              @input="settings.defaultCurrency = settings.defaultCurrency.toUpperCase()"
            />
          </label>

          <label class="block text-sm">
            <span class="mb-1 block text-mf-muted">Session lifetime (days)</span>
            <input
              v-model.number="settings.sessionTtlDays"
              type="number"
              min="1"
              required
              class="w-32 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 outline-none focus:border-mf-green"
            />
          </label>
        </section>

        <section class="space-y-3 rounded-2xl bg-mf-surface p-4">
          <h2 class="font-medium">Sync</h2>
          <label class="block text-sm">
            <span class="mb-1 block text-mf-muted">Change log retention (days)</span>
            <input
              v-model.number="settings.changeLogRetentionDays"
              type="number"
              min="1"
              required
              class="w-32 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 outline-none focus:border-mf-green"
            />
            <span class="mt-1 block text-xs text-mf-muted">
              A device offline longer than this re-bootstraps from a full snapshot instead of
              replaying deltas. Lower it if the journal gets large — it never affects your
              transactions, only how it syncs them.
            </span>
          </label>
        </section>

        <section class="space-y-3 rounded-2xl bg-mf-surface p-4">
          <h2 class="font-medium">Exchange rates</h2>

          <label class="flex items-center gap-2 text-sm">
            <input v-model="settings.fxEnabled" type="checkbox" class="size-4" />
            Fetch daily exchange rates
          </label>
          <p class="text-xs text-mf-muted">
            Turning this off leaves records in a currency other than the base one out of every
            total instead of converting them.
          </p>

          <template v-if="settings.fxEnabled">
            <label class="block text-sm">
              <span class="mb-1 block text-mf-muted">Daily refresh time</span>
              <input
                v-model="settings.fxRefreshAt"
                type="time"
                required
                class="w-32 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 outline-none focus:border-mf-green"
              />
            </label>

            <div class="text-sm">
              <span class="mb-1 block text-mf-muted">Providers, tried in order</span>
              <label v-for="p in FX_PROVIDERS" :key="p.id" class="flex items-center gap-2 py-1">
                <input
                  type="checkbox"
                  class="size-4"
                  :checked="settings.fxProviders.includes(p.id)"
                  @change="toggleProvider(p.id, ($event.target as HTMLInputElement).checked)"
                />
                {{ p.label }}
              </label>
              <p v-if="!settings.fxProviders.length" class="text-xs text-mf-red-text">
                At least one provider is required while exchange rates are on.
              </p>
            </div>
          </template>
        </section>

        <button
          type="submit"
          :disabled="busy"
          class="w-full rounded-full bg-mf-green py-2.5 text-sm font-medium text-white disabled:opacity-50"
        >
          {{ busy ? 'Saving…' : 'Save' }}
        </button>
      </form>
    </main>
  </div>
</template>
