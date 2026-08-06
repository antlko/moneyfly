<script setup lang="ts">
import { Copy, Download, Trash2 } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { toast } from 'vue-sonner'

import * as http from '@/api/http'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'

/**
 * Export, API tokens and webhooks — three ways data leaves the app besides a
 * device syncing. Grouped on one screen because each is a small, occasional
 * action, not because they are one feature: three menu entries for
 * once-a-quarter actions would just be more to scroll past every other time.
 */
const message = ref('')
const error = ref('')

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

async function copy(text: string) {
  await navigator.clipboard.writeText(text)
  toast('Copied to clipboard')
}

const when = (unix: number) => (unix ? new Date(unix * 1000).toLocaleString() : 'never')

// --- tokens --------------------------------------------------------------------------

const tokens = ref<http.ApiToken[]>([])
const newTokenName = ref('')
/** Shown once, immediately after creation — see docs/API.md. */
const justCreatedToken = ref<http.CreatedApiToken | null>(null)

onMounted(loadTokens)

async function loadTokens() {
  tokens.value = await http.listTokens()
}

async function addToken() {
  const name = newTokenName.value.trim()
  if (!name) return
  await run(async () => {
    justCreatedToken.value = await http.createToken(name)
    newTokenName.value = ''
    await loadTokens()
  }, '')
}

const removeToken = (id: string) =>
  run(async () => {
    if (justCreatedToken.value?.id === id) justCreatedToken.value = null
    await http.deleteToken(id)
    await loadTokens()
  }, 'Token revoked.')

// --- webhooks ------------------------------------------------------------------------

const webhooks = ref<http.Webhook[]>([])
const newWebhookName = ref('')
const newWebhookUrl = ref('')

onMounted(loadWebhooks)

async function loadWebhooks() {
  webhooks.value = await http.listWebhooks()
}

async function addWebhook() {
  const name = newWebhookName.value.trim()
  const url = newWebhookUrl.value.trim()
  if (!name || !url) return
  await run(async () => {
    await http.createWebhook(name, url)
    newWebhookName.value = ''
    newWebhookUrl.value = ''
    await loadWebhooks()
  }, 'Webhook added.')
}

const removeWebhook = (id: string) =>
  run(async () => {
    await http.deleteWebhook(id)
    await loadWebhooks()
  }, 'Webhook removed.')
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Integrations" />

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <p v-if="message" class="rounded-lg bg-mf-green-soft/40 p-3 text-sm text-mf-green-dark">
        {{ message }}
      </p>
      <p v-if="error" role="alert" class="rounded-lg bg-mf-red/20 p-3 text-sm text-mf-red-text">
        {{ error }}
      </p>

      <!-- Export -->
      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-1 font-medium">Export</h2>
        <p class="mb-3 text-sm text-mf-muted">
          Every transaction, as CSV. The Monefy profile matches the format that app exports, so it
          can be read back in either direction.
        </p>
        <div class="flex gap-3">
          <a
            :href="http.exportCsvUrl('native')"
            class="flex flex-1 items-center justify-center gap-2 rounded-full border border-mf-green py-2.5 text-sm text-mf-green-dark"
          >
            <Download :size="16" :stroke-width="2" />
            Native CSV
          </a>
          <a
            :href="http.exportCsvUrl('monefy')"
            class="flex flex-1 items-center justify-center gap-2 rounded-full border border-mf-green py-2.5 text-sm text-mf-green-dark"
          >
            <Download :size="16" :stroke-width="2" />
            Monefy CSV
          </a>
        </div>
      </section>

      <!-- API tokens -->
      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-1 font-medium">API tokens</h2>
        <p class="mb-3 text-sm text-mf-muted">
          A long-lived credential for a script or a phone shortcut — <code>Authorization: Bearer
          &lt;token&gt;</code> on any request this app accepts from a browser.
        </p>

        <div v-if="justCreatedToken" class="mb-3 rounded-lg bg-mf-green-soft/30 p-3">
          <p class="mb-1 text-xs text-mf-ink/70">
            Copy this now — it will not be shown again.
          </p>
          <div class="flex items-center gap-2">
            <code class="min-w-0 flex-1 truncate text-sm">{{ justCreatedToken.token }}</code>
            <button type="button" aria-label="Copy token" @click="copy(justCreatedToken.token)">
              <Copy :size="16" :stroke-width="2" />
            </button>
          </div>
        </div>

        <ul class="mb-3 divide-y divide-mf-muted/20">
          <li v-for="t in tokens" :key="t.id" class="flex items-center gap-3 py-2 text-sm">
            <div class="min-w-0 flex-1">
              <p class="truncate">{{ t.name }}</p>
              <p class="text-xs text-mf-muted">Last used {{ when(t.lastUsedAt) }}</p>
            </div>
            <button
              type="button"
              class="grid size-8 shrink-0 place-items-center rounded-full text-mf-muted"
              aria-label="Revoke token"
              @click="removeToken(t.id)"
            >
              <Trash2 :size="16" :stroke-width="1.8" />
            </button>
          </li>
        </ul>

        <form class="flex gap-2" @submit.prevent="addToken">
          <input
            v-model="newTokenName"
            type="text"
            placeholder="What is this token for?"
            class="min-w-0 flex-1 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 text-sm outline-none focus:border-mf-green"
          />
          <button type="submit" class="rounded-full bg-mf-green px-4 py-2 text-sm text-white">
            Create
          </button>
        </form>
      </section>

      <!-- Webhooks -->
      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-1 font-medium">Webhooks</h2>
        <p class="mb-3 text-sm text-mf-muted">
          Notified with every transaction a push accepts from any of your devices, signed with the
          secret below in <code>X-Moneyfly-Signature</code> (HMAC-SHA256).
        </p>

        <ul class="mb-3 space-y-3">
          <li v-for="w in webhooks" :key="w.id" class="rounded-lg bg-mf-bg p-3 text-sm">
            <div class="flex items-center gap-3">
              <div class="min-w-0 flex-1">
                <p class="truncate font-medium">{{ w.name }}</p>
                <p class="truncate text-xs text-mf-muted">{{ w.url }}</p>
              </div>
              <button
                type="button"
                class="grid size-8 shrink-0 place-items-center rounded-full text-mf-muted"
                aria-label="Remove webhook"
                @click="removeWebhook(w.id)"
              >
                <Trash2 :size="16" :stroke-width="1.8" />
              </button>
            </div>
            <div class="mt-2 flex items-center gap-2">
              <code class="min-w-0 flex-1 truncate text-xs text-mf-muted">{{ w.secret }}</code>
              <button type="button" aria-label="Copy secret" @click="copy(w.secret)">
                <Copy :size="14" :stroke-width="2" />
              </button>
            </div>
          </li>
        </ul>

        <form class="flex flex-col gap-2" @submit.prevent="addWebhook">
          <input
            v-model="newWebhookName"
            type="text"
            placeholder="Name"
            class="rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 text-sm outline-none focus:border-mf-green"
          />
          <div class="flex gap-2">
            <input
              v-model="newWebhookUrl"
              type="url"
              placeholder="https://…"
              class="min-w-0 flex-1 rounded-lg border border-mf-muted/60 bg-mf-bg px-3 py-2 text-sm outline-none focus:border-mf-green"
            />
            <button type="submit" class="rounded-full bg-mf-green px-4 py-2 text-sm text-white">
              Add
            </button>
          </div>
        </form>
      </section>
    </main>
  </div>
</template>
