<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { Setting } from '@/api/types'
import { pushToast } from '@/lib/toast'
import { useSettingsStore } from '@/stores/settings'

/**
 * One screen, one mechanism (docs/08-ux.md §8.7).
 *
 * Every auto setting shows its provider, its last fetch and its freshness, and an
 * override shows both figures with a reset. A provider outage is a badge and a
 * message — never a broken screen.
 */
const settings = useSettingsStore()
const drafts = ref<Record<string, string>>({})
const error = ref('')

function labelFor(key: string): string {
  const [group, name] = key.split('.')
  if (group === 'fx') return name.replace('EUR_', 'EUR → ')
  if (group === 'price') return `${name} price`
  return name.replace(/_/g, ' ')
}

function freshness(s: Setting): string {
  if (s.mode !== 'auto') return 'manual — nothing refreshes it'
  if (!s.last_fetched_at) return 'never fetched'
  const when = new Date(s.last_fetched_at)
  const minutes = Math.round((Date.now() - when.getTime()) / 60000)
  if (minutes < 2) return 'just now'
  if (minutes < 60) return `${minutes} minutes ago`
  const hours = Math.round(minutes / 60)
  if (hours < 48) return `${hours} hours ago`
  return `${Math.round(hours / 24)} days ago`
}

function beginEdit(s: Setting) {
  drafts.value[s.key] = s.manual_value ?? s.effective_value ?? ''
}

async function save(s: Setting) {
  const value = drafts.value[s.key]
  if (value === undefined) return
  error.value = ''
  try {
    await settings.setManual(s.key, value.trim())
    delete drafts.value[s.key]
    pushToast(`${labelFor(s.key)} set to ${value.trim()}.`, 'success')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The setting could not be saved.'
  }
}

async function reset(s: Setting) {
  error.value = ''
  try {
    await settings.reset(s.key)
    delete drafts.value[s.key]
    pushToast(`${labelFor(s.key)} is back to the provider's value.`, 'info')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The override could not be cleared.'
  }
}

async function refresh(s: Setting) {
  error.value = ''
  try {
    const updated = await settings.refresh(s.key)
    if (updated.last_error) {
      pushToast(`${labelFor(s.key)} could not be refreshed; the last value stands.`, 'error')
    } else {
      pushToast(`${labelFor(s.key)} is now ${updated.last_value}.`, 'success')
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The refresh could not be started.'
  }
}

async function toggleMode(s: Setting) {
  error.value = ''
  try {
    await settings.setMode(s.key, s.mode === 'auto' ? 'manual' : 'auto')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The mode could not be changed.'
  }
}

async function connectTelegram() {
  error.value = ''
  try {
    await settings.mintLinkCode()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'A link code could not be created.'
  }
}

async function revokeLink(id: number) {
  error.value = ''
  try {
    await settings.revokeLink(id)
    pushToast('That chat can no longer import anything.', 'info')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The link could not be revoked.'
  }
}

onMounted(() => settings.load())
</script>

<template>
  <section>
    <header class="mb-4">
      <h1 class="text-lg font-semibold">Settings</h1>
      <p class="text-sm text-slate-500">
        Rates, prices and thresholds. Anything on auto maintains itself; anything you type wins.
      </p>
    </header>

    <p v-if="error" class="mb-3 text-sm text-state-severe" role="alert">{{ error }}</p>

    <p
      v-if="settings.stale.length"
      class="mb-4 rounded-xl border border-state-approach/40 bg-state-approach/10 p-3 text-xs"
    >
      {{ settings.stale.length }} value{{ settings.stale.length === 1 ? '' : 's' }} have not been
      refreshed recently. The last figure is still in use — nothing is guessed.
    </p>

    <div v-for="group in settings.groups" :key="group.title" class="mb-6">
      <h2 class="mb-1 text-sm font-semibold uppercase tracking-wide text-slate-500">
        {{ group.title }}
      </h2>
      <p class="mb-2 text-xs text-slate-400">{{ group.hint }}</p>

      <ul class="space-y-1">
        <li
          v-for="item in group.keys"
          :key="item.key"
          class="rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
        >
          <div class="flex items-baseline justify-between gap-2">
            <span class="font-medium">{{ labelFor(item.key) }}</span>
            <span class="money text-lg font-semibold">{{ item.effective_value ?? '—' }}</span>
          </div>

          <p class="mt-0.5 text-xs text-slate-400">
            <span
              class="rounded px-1"
              :class="
                item.mode === 'auto'
                  ? 'bg-brand-100 text-brand-700 dark:bg-brand-900/40 dark:text-brand-200'
                  : 'bg-slate-100 dark:bg-slate-800'
              "
            >
              {{ item.mode }}
            </span>
            <template v-if="item.provider_key"> · {{ item.provider_key }}</template>
            · {{ freshness(item) }}
            <span v-if="item.stale" class="text-state-approach">· stale</span>
          </p>

          <!-- Both values, when they differ. This is the whole point of §8.7. -->
          <p v-if="item.overridden" class="mt-1 text-xs text-slate-500">
            auto suggested <span class="money">{{ item.last_value }}</span> — you set
            <span class="money">{{ item.manual_value }}</span>
            <button
              type="button"
              class="ml-2 underline"
              :disabled="settings.busy === item.key"
              @click="reset(item)"
            >
              reset
            </button>
          </p>

          <p v-if="item.last_error" class="mt-1 text-xs text-state-severe">
            last attempt failed: {{ item.last_error }}
          </p>

          <div class="mt-2 flex flex-wrap items-center gap-2">
            <template v-if="drafts[item.key] !== undefined">
              <input
                v-model="drafts[item.key]"
                class="money tap-target w-32 rounded-lg border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-700 dark:bg-slate-800"
                :aria-label="`New value for ${labelFor(item.key)}`"
                @keyup.enter="save(item)"
              />
              <button
                type="button"
                class="tap-target rounded-lg bg-brand-600 px-3 py-1.5 text-xs font-semibold text-white"
                :disabled="settings.busy === item.key"
                @click="save(item)"
              >
                Save
              </button>
              <button
                type="button"
                class="tap-target px-2 text-xs text-slate-500 underline"
                @click="delete drafts[item.key]"
              >
                Cancel
              </button>
            </template>
            <template v-else>
              <button
                type="button"
                class="tap-target rounded-lg border border-slate-300 px-3 py-1.5 text-xs dark:border-slate-700"
                @click="beginEdit(item)"
              >
                Set manually
              </button>
              <button
                v-if="item.mode === 'auto'"
                type="button"
                class="tap-target rounded-lg border border-slate-300 px-3 py-1.5 text-xs dark:border-slate-700"
                :disabled="settings.busy === item.key"
                @click="refresh(item)"
              >
                {{ settings.busy === item.key ? 'Refreshing…' : 'Refresh now' }}
              </button>
              <button
                v-if="item.provider_key"
                type="button"
                class="tap-target px-2 text-xs text-slate-500 underline"
                :disabled="settings.busy === item.key"
                @click="toggleMode(item)"
              >
                switch to {{ item.mode === 'auto' ? 'manual' : 'auto' }}
              </button>
            </template>
          </div>
        </li>
      </ul>
    </div>

    <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-500">Telegram</h2>
    <p class="mb-2 text-xs text-slate-400">
      Share a Monefy export to the bot and it imports — the phone never has to reach this server.
      The bot does nothing else.
    </p>

    <div
      v-if="settings.linkCode"
      class="mb-2 rounded-xl border border-brand-300 bg-brand-50 p-3 text-sm dark:border-brand-800 dark:bg-brand-900/20"
      aria-live="polite"
    >
      <p class="font-mono text-2xl font-bold tracking-widest">{{ settings.linkCode.code }}</p>
      <p class="mt-1 text-xs text-slate-600 dark:text-slate-300">
        {{ settings.linkCode.instructions }}
      </p>
    </div>

    <ul v-if="settings.telegram.length" class="mb-2 space-y-1 text-xs">
      <li
        v-for="l in settings.telegram"
        :key="l.id"
        class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 dark:border-slate-800 dark:bg-slate-900"
      >
        <span class="flex-1 truncate">
          {{ l.chat_id ? `chat ${l.chat_id}` : 'code not yet used' }}
        </span>
        <span :class="l.live ? 'text-state-within' : 'text-slate-400'">
          {{ l.live ? 'linked' : l.revoked_at ? 'revoked' : 'pending' }}
        </span>
        <button
          v-if="l.live"
          type="button"
          class="tap-target px-2 text-state-severe underline"
          @click="revokeLink(l.id)"
        >
          revoke
        </button>
      </li>
    </ul>

    <button
      type="button"
      class="tap-target mb-6 rounded-xl border border-slate-300 px-4 py-2 text-sm dark:border-slate-700"
      @click="connectTelegram"
    >
      Connect a chat
    </button>

    <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-500">Providers</h2>
    <p class="mb-2 text-xs text-slate-400">
      Free and keyless. There is no credentialed provider and none is planned.
    </p>
    <ul class="space-y-1 text-xs">
      <li
        v-for="p in settings.providers"
        :key="p.key"
        class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 dark:border-slate-800 dark:bg-slate-900"
      >
        <span class="flex-1 truncate font-medium">{{ p.key }}</span>
        <span class="text-slate-400">{{ p.kind }}</span>
        <span class="text-slate-400">priority {{ p.priority }}</span>
        <span :class="p.enabled ? 'text-state-within' : 'text-slate-400'">
          {{ p.enabled ? 'enabled' : 'off' }}
        </span>
      </li>
    </ul>
  </section>
</template>
