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
  /**
   * Currency codes this person has turned on.
   *
   * Declared rather than inferred. Deriving the list from the accounts that
   * already exist is circular — you cannot open a forint account until forint
   * is a currency, and forint only became one because an account used it. It is
   * a synced setting so adding a currency on the phone offers it on the laptop.
   */
  currencies: 'currency.enabled',
  /**
   * The account the last record was written against.
   *
   * Spending is habitual: the account you paid from an hour ago is
   * overwhelmingly the one you are about to pay from again. Defaulting to the
   * *first* account instead meant anyone whose everyday wallet was not first in
   * the list re-picked it on every single record — and picking it is a sheet, a
   * scroll and a tap, on the screen whose whole promise is three taps total.
   *
   * Synced like any other preference, so the phone and the laptop agree on
   * which account is "current" rather than each keeping its own idea.
   */
  lastAccount: 'record.lastAccount',
  /** How the desktop dashboard draws "By category": 'pie' or 'bars'. */
  desktopChart: 'view.desktopChart',
} as const
