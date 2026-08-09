<script setup lang="ts">
import { ChevronDown, ChevronUp } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'

import * as http from '@/api/http'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { CURRENCY_OPTIONS } from '@/lib/currencies'

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

/*
 * Human labels for provider ids. Presentation only — the authoritative *set*
 * comes from the server (`fxProvidersAvailable`), because a hardcoded copy here
 * does not just go stale: `toggleProvider` used to rebuild the saved list from
 * a local constant, so a provider this build had never heard of was silently
 * deleted from config.yaml the next time anyone saved anything at all.
 *
 * An id with no entry here falls back to showing the id, so a provider added on
 * the backend is usable immediately without a frontend release.
 */
const PROVIDER_LABELS: Record<string, string> = {
  'open-er-api': 'open.er-api.com',
  fawazahmed0: 'fawazahmed0/currency-api',
}

const providerLabel = (id: string) => PROVIDER_LABELS[id] ?? id

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

/*
 * Add or remove one provider, leaving the rest of the list — and crucially its
 * order — exactly as it was. Providers are tried in order and the first that
 * answers wins, so the order is a real setting, not an artefact.
 *
 * A newly enabled provider goes on the end, where it acts as a fallback, rather
 * than displacing whatever the operator chose to try first.
 */
function toggleProvider(id: string, on: boolean) {
  if (!settings.value) return
  const current = settings.value.fxProviders
  if (on) {
    if (!current.includes(id)) settings.value.fxProviders = [...current, id]
  } else {
    settings.value.fxProviders = current.filter((p) => p !== id)
  }
}

/** Move a provider one place up or down the try-order. */
function moveProvider(id: string, delta: number) {
  if (!settings.value) return
  const list = [...settings.value.fxProviders]
  const from = list.indexOf(id)
  const to = from + delta
  if (from < 0 || to < 0 || to >= list.length) return
  list.splice(to, 0, ...list.splice(from, 1))
  settings.value.fxProviders = list
}

/**
 * Every provider the server accepts, enabled ones first in their configured
 * order, then the rest. Reordering only applies to the enabled ones, since the
 * order of something that is not tried means nothing.
 */
const providerRows = computed(() => {
  const s = settings.value
  if (!s) return []
  const enabled = s.fxProviders.filter((id) => s.fxProvidersAvailable.includes(id))
  const disabled = s.fxProvidersAvailable.filter((id) => !s.fxProviders.includes(id))
  return [
    ...enabled.map((id, i) => ({ id, on: true, first: i === 0, last: i === enabled.length - 1 })),
    ...disabled.map((id) => ({ id, on: false, first: false, last: false })),
  ]
})

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
            <!--
              A datalist rather than a select: lib/currencies.ts is deliberately
              a shortlist of what people actually keep money in, not ISO 4217 in
              full, so restricting the field to it would make a perfectly valid
              code unenterable. Suggest, do not constrain.
            -->
            <input
              v-model="settings.defaultCurrency"
              type="text"
              list="instance-currencies"
              maxlength="3"
              required
              class="w-24 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 uppercase outline-none focus:border-mf-green"
              @input="settings.defaultCurrency = settings.defaultCurrency.toUpperCase()"
            />
            <datalist id="instance-currencies">
              <option v-for="c in CURRENCY_OPTIONS" :key="c.code" :value="c.code">
                {{ c.name }}
              </option>
            </datalist>
          </label>

          <label class="block text-sm">
            <span class="mb-1 block text-mf-muted">Session lifetime (days)</span>
            <input
              v-model.number="settings.sessionTtlDays"
              type="number"
              min="1"
              :max="settings.sessionTtlDaysMax"
              required
              class="w-32 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 outline-none focus:border-mf-green"
            />
            <span class="mt-1 block text-xs text-mf-muted">
              How long a signed-in device stays signed in. Long on purpose — this is a phone app you
              should not have to sign in to twice a year.
            </span>
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
              <div v-for="p in providerRows" :key="p.id" class="flex items-center gap-2 py-1">
                <label class="flex min-w-0 flex-1 items-center gap-2">
                  <input
                    type="checkbox"
                    class="size-4 shrink-0"
                    :checked="p.on"
                    @change="toggleProvider(p.id, ($event.target as HTMLInputElement).checked)"
                  />
                  <span class="truncate">{{ providerLabel(p.id) }}</span>
                </label>
                <div v-if="p.on" class="flex shrink-0 gap-1">
                  <button
                    type="button"
                    aria-label="Try earlier"
                    :disabled="p.first"
                    class="grid size-7 place-items-center rounded-lg border border-mf-muted/60 disabled:opacity-30"
                    @click="moveProvider(p.id, -1)"
                  >
                    <ChevronUp :size="14" />
                  </button>
                  <button
                    type="button"
                    aria-label="Try later"
                    :disabled="p.last"
                    class="grid size-7 place-items-center rounded-lg border border-mf-muted/60 disabled:opacity-30"
                    @click="moveProvider(p.id, 1)"
                  >
                    <ChevronDown :size="14" />
                  </button>
                </div>
              </div>
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
