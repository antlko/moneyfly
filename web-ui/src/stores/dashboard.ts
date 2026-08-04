import { defineStore, storeToRefs } from 'pinia'
import { computed, ref } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import {
  periodBounds,
  periodLabel,
  periodOfKind,
  shiftPeriod,
  type Period,
  type PeriodKind,
} from '@/lib/period'
import type { Row } from '@/sync/types'
import { useAuthStore } from './auth'
import { useFxStore } from './fx'
import { SETTING, useSettingsStore } from './settings'
import { useTaxonomyStore } from './taxonomy'

export type ViewMode = 'donut' | 'list'
export type SortMode = 'amount' | 'name'

/** One category's share of a month. */
export interface CategoryTotal {
  category: Row
  /** Signed minor units — negative for spending, as stored. */
  totalMinor: number
  count: number
  /** Share of the month's spending, 0–1. Zero for income. */
  share: number
}

/**
 * The month the dashboard is showing, and everything derived from it.
 *
 * Every figure here is computed from the local replica. Nothing on this screen
 * waits on the network, and nothing is read back from the server — which is also
 * why balances are never stored (see docs/SYNC.md §2).
 */
export const useDashboardStore = defineStore('dashboard', () => {
  const auth = useAuthStore()
  const settings = useSettingsStore()
  const taxonomy = useTaxonomyStore()
  const fx = useFxStore()

  /**
   * The visible period. Not a month: the reference's left drawer switches
   * between day, week, month, year, all and a custom interval, and every total
   * on this screen follows it.
   */
  const period = ref<Period>(periodOfKind('month'))
  const label = computed(() => periodLabel(period.value))
  const bounds = computed(() => periodBounds(period.value))

  const view = computed<ViewMode>(() => settings.get(SETTING.view, 'donut'))
  const sort = computed<SortMode>(() => settings.get(SETTING.sort, 'amount'))

  const setPeriod = (next: Period) => {
    period.value = next
  }
  /** Switch the kind, keeping the day currently in view. */
  const setPeriodKind = (kind: PeriodKind) => setPeriod(periodOfKind(kind, period.value.anchor))
  const setInterval = (from: string, to: string) =>
    setPeriod({ kind: 'interval', anchor: from, until: to })
  /** Jump to the period containing a chosen day, keeping the kind. */
  const goToDay = (day: string) => setPeriod({ ...period.value, anchor: day })
  const step = (delta: number) => setPeriod(shiftPeriod(period.value, delta))
  /** Whether paging makes sense — "all time" has no neighbours. */
  const pageable = computed(() => period.value.kind !== 'all')
  const toggleView = () => settings.set(SETTING.view, view.value === 'donut' ? 'list' : 'donut')
  const toggleSort = () => settings.set(SETTING.sort, sort.value === 'amount' ? 'name' : 'amount')

  const baseCurrency = computed(() => auth.user?.baseCurrency ?? 'EUR')

  // Re-subscribes when the month changes: Dexie watches the database, not our refs.
  const periodRows = useLiveQuery<Row[]>(
    () => {
      const { from, to } = bounds.value
      return db.txn.where('[deleted+occurredOn]').between([0, from], [0, to, '￿']).toArray()
    },
    [],
    // Dexie watches the database, not our refs, so the subscription is rebuilt
    // whenever the period moves.
    () => `${period.value.kind}:${period.value.anchor}:${period.value.until ?? ''}`,
  )

  /**
   * Which accounts the dashboard is limited to. Empty means all of them, which
   * is both the default and what the header says.
   */
  const accountFilter = computed<string[]>(() => settings.get(SETTING.accounts, []))
  const setAccountFilter = (ids: string[]) => settings.set(SETTING.accounts, ids)

  const rows = computed(() =>
    accountFilter.value.length === 0
      ? periodRows.value
      : periodRows.value.filter((r) => accountFilter.value.includes(String(r.accountId ?? ''))),
  )

  const accountLabel = computed(() => {
    const ids = accountFilter.value
    if (ids.length === 0) return 'All accounts'
    const names = ids
      .map((id) => taxonomy.accounts.find((a) => a.id === id)?.name)
      .filter(Boolean) as string[]
    if (names.length === 0) return 'All accounts'
    return names.length === 1 ? names[0] : `${names.length} accounts`
  })

  /**
   * A row's amount in the base currency, priced with the rate on the day it
   * happened — not today's.
   *
   * Using today's rate would make last March's total move every time the app is
   * opened, which is indistinguishable from the app losing track of money.
   *
   * `null` means no rate is known for that pair. The row is then left out of the
   * sums and counted instead: a total that quietly omits rows is exactly how a
   * month comes to look cheaper than it was.
   */
  function inBase(row: Row): number | null {
    const currency = String(row.currency ?? baseCurrency.value)
    if (currency === baseCurrency.value) return Number(row.amountMinor ?? 0)
    return fx.convert(
      Number(row.amountMinor ?? 0),
      currency,
      baseCurrency.value,
      String(row.occurredOn ?? ''),
    )
  }

  /** Rows whose currency cannot be converted to the base one. */
  const unconvertedCount = computed(
    () => rows.value.filter((r) => r.currency !== baseCurrency.value && inBase(r) === null).length,
  )

  /** Rows that can be added up, each already in the base currency. */
  const spendable = computed(() =>
    rows.value
      .map((row) => ({ row, minor: inBase(row) }))
      .filter((r): r is { row: Row; minor: number } => r.minor !== null),
  )

  const expenseMinor = computed(() =>
    spendable.value.filter((r) => r.row.kind === 'expense').reduce((sum, r) => sum + r.minor, 0),
  )
  const incomeMinor = computed(() =>
    spendable.value.filter((r) => r.row.kind === 'income').reduce((sum, r) => sum + r.minor, 0),
  )
  /** What the balance pill shows: income minus spending for the month. */
  const balanceMinor = computed(() => incomeMinor.value + expenseMinor.value)

  const byCategory = computed<CategoryTotal[]>(() => {
    const totals = new Map<string, { totalMinor: number; count: number }>()
    for (const { row, minor } of spendable.value) {
      if (row.kind !== 'expense') continue
      const key = String(row.categoryId ?? '')
      const entry = totals.get(key) ?? { totalMinor: 0, count: 0 }
      entry.totalMinor += minor
      entry.count++
      totals.set(key, entry)
    }

    const spent = Math.abs(expenseMinor.value)
    const out: CategoryTotal[] = []
    for (const [id, entry] of totals) {
      const category = taxonomy.byId.get(id)
      // A transaction can outlive its category (deleted on another device, or
      // not yet synced). Showing it as "Uncategorised" beats dropping money
      // out of the total with no explanation.
      out.push({
        category: category ?? uncategorised(id),
        totalMinor: entry.totalMinor,
        count: entry.count,
        share: spent === 0 ? 0 : Math.abs(entry.totalMinor) / spent,
      })
    }

    return out.sort((a, b) =>
      sort.value === 'name'
        ? String(a.category.name).localeCompare(String(b.category.name))
        : Math.abs(b.totalMinor) - Math.abs(a.totalMinor),
    )
  })

  const isEmpty = computed(() => rows.value.length === 0)

  return {
    period,
    label,
    accountFilter,
    accountLabel,
    setAccountFilter,
    bounds,
    pageable,
    view,
    sort,
    rows,
    baseCurrency,
    inBase,
    unconvertedCount,
    expenseMinor,
    incomeMinor,
    balanceMinor,
    byCategory,
    isEmpty,
    setPeriod,
    setPeriodKind,
    setInterval,
    goToDay,
    step,
    toggleView,
    toggleSort,
  }
})

function uncategorised(id: string): Row {
  return {
    id: id || 'uncategorised',
    lamport: 0,
    deviceId: '',
    updatedAt: 0,
    deleted: 0,
    name: 'Uncategorised',
    kind: 'expense',
    icon: 'CircleEllipsis',
    color: 'gray',
  }
}

/** Convenience for templates that only need the refs. */
export const dashboardRefs = () => storeToRefs(useDashboardStore())
