import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { API_BASE, api } from '@/api/client'
import type { RateProvider, Setting, TelegramLink, TelegramLinkCode } from '@/api/types'

/**
 * One mechanism behind every configurable number (docs/06-fx-and-providers.md §6.4).
 *
 * The store keeps both the manual and the provider value, because the screen
 * shows both: "auto suggested 51.08 — you set 51.00" tells you something neither
 * number does alone.
 */
export const useSettingsStore = defineStore('settings', () => {
  const items = ref<Setting[]>([])
  const providers = ref<RateProvider[]>([])
  const telegram = ref<TelegramLink[]>([])
  const linkCode = ref<TelegramLinkCode | null>(null)
  const busy = ref<string | null>(null)

  /** The screen's grouping, from docs/08-ux.md §8.7. */
  const groups = computed(() => [
    {
      title: 'Currencies',
      hint: 'rates, refreshed daily',
      keys: items.value.filter((s) => s.key.startsWith('fx.')),
    },
    {
      title: 'Prices',
      hint: 'metals and crypto',
      keys: items.value.filter((s) => s.key.startsWith('price.')),
    },
    {
      title: 'Thresholds',
      hint: 'when a category turns amber or red',
      keys: items.value.filter((s) => s.key.startsWith('threshold.')),
    },
    {
      title: 'Reports',
      hint: 'base currency, fiscal year, valuation',
      keys: items.value.filter((s) => s.key.startsWith('report.')),
    },
  ])

  const stale = computed(() => items.value.filter((s) => s.stale))

  async function load(): Promise<void> {
    const [settings, sources, links] = await Promise.all([
      api.get<Setting[]>(`${API_BASE}/settings`),
      api.get<RateProvider[]>(`${API_BASE}/providers`),
      api.get<TelegramLink[]>(`${API_BASE}/telegram/links`),
    ])
    items.value = settings
    providers.value = sources
    telegram.value = links
  }

  /**
   * Mints a single-use code to type into the chat. It lives for ten minutes and
   * is shown once — there is no way to read it back.
   */
  async function mintLinkCode(): Promise<TelegramLinkCode> {
    const minted = await api.post<TelegramLinkCode>(`${API_BASE}/telegram/link-code`)
    linkCode.value = minted
    return minted
  }

  async function revokeLink(id: number): Promise<void> {
    await api.delete(`${API_BASE}/telegram/links/${id}`)
    telegram.value = await api.get<TelegramLink[]>(`${API_BASE}/telegram/links`)
  }

  function replace(updated: Setting) {
    items.value = items.value.map((s) => (s.key === updated.key ? updated : s))
  }

  async function setManual(key: string, value: string): Promise<void> {
    busy.value = key
    try {
      replace(await api.patch<Setting>(`${API_BASE}/settings/${key}`, { manual_value: value }))
    } finally {
      busy.value = null
    }
  }

  /** Drops the override and goes back to whatever the provider says. */
  async function reset(key: string): Promise<void> {
    busy.value = key
    try {
      replace(await api.patch<Setting>(`${API_BASE}/settings/${key}`, { clear_manual: true }))
    } finally {
      busy.value = null
    }
  }

  async function setMode(key: string, mode: 'manual' | 'auto'): Promise<void> {
    busy.value = key
    try {
      replace(await api.patch<Setting>(`${API_BASE}/settings/${key}`, { mode }))
    } finally {
      busy.value = null
    }
  }

  /**
   * Forces a fetch. A provider outage is not an error here: the request succeeds,
   * the message lands in last_error, and the previous value stands.
   */
  async function refresh(key: string): Promise<Setting> {
    busy.value = key
    try {
      const updated = await api.post<Setting>(`${API_BASE}/settings/${key}/refresh`)
      replace(updated)
      return updated
    } finally {
      busy.value = null
    }
  }

  return {
    items,
    providers,
    telegram,
    linkCode,
    busy,
    groups,
    stale,
    load,
    setManual,
    reset,
    setMode,
    refresh,
    mintLinkCode,
    revokeLink,
  }
})
