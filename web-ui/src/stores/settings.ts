import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'

/**
 * Preferences that follow the person between devices — which dashboard view,
 * how to sort, later the theme and the first day of the month.
 *
 * They are a synced entity like any other, with the **setting key as the row
 * id**. Two devices changing one preference therefore collide on a single row
 * and last-write-wins settles it, which is the behaviour you want; separate rows
 * per device would leave both values alive with no way to choose.
 */
export const useSettingsStore = defineStore('settings', () => {
  const rows = useLiveQuery<Row[]>(() => db.user_setting.toArray(), [])

  const map = computed(
    () => new Map(rows.value.filter((r) => !r.deleted).map((r) => [r.id, r.value])),
  )

  function get<T>(key: string, fallback: T): T {
    const value = map.value.get(key)
    return value === undefined ? fallback : (value as T)
  }

  const set = (key: string, value: unknown) => sync.write('user_setting', { value }, key)

  return { get, set }
})

/** Setting keys, in one place so a typo cannot silently create a second setting. */
export const SETTING = {
  view: 'view.mode',
  sort: 'view.sort',
  /** Account ids the dashboard is limited to. Empty means all of them. */
  accounts: 'filter.accounts',
} as const
