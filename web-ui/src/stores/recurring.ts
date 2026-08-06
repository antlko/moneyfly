import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import type { RecurringFreq } from '@/lib/period'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'

export interface RecurringInput {
  kind: 'expense' | 'income'
  freq: RecurringFreq
  nextOn: string
  amountMinor: number
  currency: string
  categoryId: string
  accountId: string
  note?: string
}

/**
 * Recurring rules, live from the replica.
 *
 * A rule only ever describes what to post next. The transactions it produces
 * are ordinary rows in `txn`, materialised hourly by the server
 * (backend/internal/api/recurring.go) — this store never touches that side,
 * the same way the dashboard never waits on it: `nextOn` moving forward is
 * something this device finds out about on the next sync, like any other
 * change made somewhere else.
 */
export const useRecurringStore = defineStore('recurring', () => {
  const rules = useLiveQuery<Row[]>(
    () => db.recurring_rule.where('deleted').equals(0).toArray(),
    [],
  )

  const bySoonest = computed(() =>
    [...rules.value].sort((a, b) => String(a.nextOn).localeCompare(String(b.nextOn))),
  )

  const create = (input: RecurringInput) => sync.write('recurring_rule', { ...input })

  return { rules, bySoonest, create }
})
