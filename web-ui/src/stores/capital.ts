import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import {
  byAccount,
  heldAt,
  monthRange,
  runwayMonths,
  savedShare,
  snapshotId,
  spendingChange,
  trailingMean,
  type Held,
  type SnapshotValue,
} from '@/lib/capital'
import { currentMonth, monthBounds, monthOf, today, type MonthKey } from '@/lib/period'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'
import { groupOf, isLiquid, UNGROUPED, useAccountsStore, type AccountGroup } from './accounts'
import { useDashboardStore } from './dashboard'
import { useFxStore } from './fx'
import { useTaxonomyStore } from './taxonomy'

/** One account in one month of the capital table. */
export interface AccountMonth {
  held: Held
  /** The same figure in the base currency, priced at the end of the month. */
  baseMinor: number | null
}

/** One column of the capital table. Every money figure is in the base currency. */
export interface CapitalMonth {
  month: MonthKey
  /** Per account, for accounts that hold anything this month. */
  accounts: Map<string, AccountMonth>
  byGroup: Map<string, number>
  /** null when no account holds anything yet this month. */
  totalMinor: number | null
  readyMinor: number | null
  /** At least one figure was typed in for this month itself, not carried over. */
  checkedIn: boolean
  /** Figures left out of the totals for want of an exchange rate. */
  unconverted: number

  /** Recorded income and spending (spending positive), from the ledger. */
  incomeMinor: number
  spentMinor: number
  /** Total against the month before; null unless both exist. */
  changeMinor: number | null
  /**
   * Change not explained by what was recorded: change − (income − spending).
   * Spending nobody wrote down, fees, and value moving on its own (gold,
   * investments) all end up here.
   */
  otherMinor: number | null
  savedShare: number | null
  /** Average monthly spending this month's runway is measured against. */
  avgSpentMinor: number | null
  runwayMonths: number | null
}

/** How many months back the "average month" reaches. */
export const AVERAGE_WINDOW = 6

/**
 * Capital, month by month: the check-ins people type in, laid against the
 * income and spending the ledger recorded. See lib/capital.ts for the model and
 * the arithmetic; this store only feeds it from the replica.
 */
export const useCapitalStore = defineStore('capital', () => {
  const taxonomy = useTaxonomyStore()
  const accountsStore = useAccountsStore()
  const dashboard = useDashboardStore()
  const fx = useFxStore()

  // `null` until the first read lands, so "no check-ins yet" can be told apart
  // from "not loaded yet" — the empty state must not flash on every visit.
  const rows = useLiveQuery<Row[] | null>(
    () => db.account_snapshot.where('deleted').equals(0).toArray(),
    null,
  )
  const loaded = computed(() => rows.value !== null)
  const txns = useLiveQuery<Row[]>(() => db.txn.where('deleted').equals(0).toArray(), [])

  const snapshots = computed<SnapshotValue[]>(() =>
    (rows.value ?? []).map((r) => ({
      accountId: String(r.accountId),
      month: String(r.month),
      amountMinor: Number(r.amountMinor ?? 0),
      currency: String(r.currency ?? ''),
    })),
  )
  const index = computed(() => byAccount(snapshots.value))

  /** Recorded income and spending per month, in the base currency. */
  const flows = computed(() => {
    const out = new Map<MonthKey, { income: number; spent: number }>()
    for (const row of txns.value) {
      if (row.kind !== 'income' && row.kind !== 'expense') continue
      const minor = dashboard.inBase(row)
      if (minor === null) continue
      const month = monthOf(String(row.occurredOn ?? ''))
      const entry = out.get(month) ?? { income: 0, spent: 0 }
      if (row.kind === 'income') entry.income += minor
      else entry.spent += -minor
      out.set(month, entry)
    }
    return out
  })

  /**
   * Every month there is anything to say about, oldest first: from the first
   * check-in or the first record, whichever is earlier, to this month.
   */
  const months = computed<MonthKey[]>(() => {
    let first = currentMonth()
    for (const s of snapshots.value) if (s.month < first) first = s.month
    for (const m of flows.value.keys()) if (m && m < first) first = m
    return monthRange(first, currentMonth())
  })

  /**
   * Accounts that belong in the table: every active one, plus any archived one
   * that was checked in — closing an account must not rewrite last year's
   * net worth.
   */
  const accounts = computed<Row[]>(() => {
    const withHistory = new Set(index.value.keys())
    return taxonomy.accounts.filter((a) => !a.archived || withHistory.has(String(a.id)))
  })

  /**
   * The table's accounts under their group headings — the accounts screen's
   * grouping and order, plus archived accounts that still have history.
   */
  const groups = computed<AccountGroup[]>(() => {
    const sorted = [...accounts.value].sort(
      (a, b) =>
        Number(a.sortOrder ?? 0) - Number(b.sortOrder ?? 0) ||
        String(a.name).localeCompare(String(b.name)),
    )
    const out = new Map<string, Row[]>()
    for (const account of sorted) {
      const name = groupOf(account)
      const list = out.get(name)
      if (list) list.push(account)
      else out.set(name, [account])
    }
    return [...out.entries()]
      .map(([name, list]) => ({ name, accounts: list }))
      .sort((a, b) => Number(a.name === UNGROUPED) - Number(b.name === UNGROUPED))
  })

  /** What an account held in a month — typed in, or carried from an earlier one. */
  const heldOf = (accountId: string, month: MonthKey) => heldAt(index.value.get(accountId), month)

  /** The day a month's figures are priced on: its last day, or today while it is running. */
  const pricingDay = (month: MonthKey) =>
    month === currentMonth() ? today() : monthBounds(month).to

  const table = computed<CapitalMonth[]>(() => {
    const base = dashboard.baseCurrency
    const list = months.value
    const current = currentMonth()

    // The average month is taken over *complete* months: the one still running
    // would drag it down every time it is consulted early in the month.
    const spentSeries = list.map((m) => {
      if (m === current) return null
      const spent = flows.value.get(m)?.spent ?? 0
      return spent > 0 ? spent : null
    })
    const averages = trailingMean(spentSeries, AVERAGE_WINDOW)

    const out: CapitalMonth[] = []
    list.forEach((month, i) => {
      const day = pricingDay(month)
      const perAccount = new Map<string, AccountMonth>()
      const byGroup = new Map<string, number>()
      let total = 0
      let ready = 0
      let any = false
      let checkedIn = false
      let unconverted = 0

      for (const account of accounts.value) {
        const held = heldAt(index.value.get(String(account.id)), month)
        if (!held) continue
        const baseMinor =
          held.currency === base ? held.amountMinor : fx.convert(held.amountMinor, held.currency, base, day)
        perAccount.set(String(account.id), { held, baseMinor })
        if (!held.carried) checkedIn = true
        if (baseMinor === null) {
          unconverted++
          continue
        }
        any = true
        total += baseMinor
        if (isLiquid(account)) ready += baseMinor
        const group = groupOf(account)
        byGroup.set(group, (byGroup.get(group) ?? 0) + baseMinor)
      }

      const flow = flows.value.get(month) ?? { income: 0, spent: 0 }
      const previous = out[i - 1]
      const totalMinor = any ? total : null
      const changeMinor =
        totalMinor !== null && previous?.totalMinor != null ? totalMinor - previous.totalMinor : null
      // The running month borrows the average of the months before it.
      const avg = month === current ? (i > 0 ? averages[i - 1] : null) : averages[i]

      out.push({
        month,
        accounts: perAccount,
        byGroup,
        totalMinor,
        readyMinor: any ? ready : null,
        checkedIn,
        unconverted,
        incomeMinor: flow.income,
        spentMinor: flow.spent,
        changeMinor,
        otherMinor: changeMinor === null ? null : changeMinor - (flow.income - flow.spent),
        savedShare: savedShare(flow.income, flow.spent),
        avgSpentMinor: avg ?? null,
        runwayMonths: any && avg ? runwayMonths(ready, avg) : null,
      })
    })
    return out
  })

  /** The latest month with any figure in it, carried or not — "what you have now". */
  const latest = computed(() => [...table.value].reverse().find((m) => m.totalMinor !== null) ?? null)

  /**
   * The latest month somebody actually checked in. Changes and composition are
   * reported from this one: a month that only carries the previous one's
   * figures always "changed by €0", which says nothing.
   */
  const latestChecked = computed(() => [...table.value].reverse().find((m) => m.checkedIn) ?? null)

  /**
   * Personal inflation: average monthly spending recently against before,
   * over complete months from the first one with any spending.
   */
  const inflation = computed(() => {
    const current = currentMonth()
    const spent = months.value
      .filter((m) => m !== current)
      .map((m) => flows.value.get(m)?.spent ?? 0)
    const first = spent.findIndex((v) => v > 0)
    return first < 0 ? null : spendingChange(spent.slice(first))
  })

  /** The check-in typed for exactly this account and month, if any. */
  const entered = (accountId: string, month: MonthKey) =>
    index.value.get(accountId)?.find((s) => s.month === month) ?? null

  /**
   * Save a month's check-ins.
   *
   * An empty figure removes that month's check-in, so the cell falls back to
   * carrying the previous one — typing a number and clearing it again must leave
   * the table as it was. With `setBalances`, each account's live balance is also
   * moved to match (accounts.setBalance), which is what checking in the
   * *current* month means: "this is what I have now".
   */
  async function saveMonth(
    month: MonthKey,
    entries: { accountId: string; amountMinor: number | null; currency: string }[],
    setBalances = false,
  ) {
    for (const entry of entries) {
      const id = snapshotId(entry.accountId, month)
      const existing = entered(entry.accountId, month)
      if (entry.amountMinor === null) {
        if (existing) await sync.remove('account_snapshot', id)
        continue
      }
      if (existing && existing.amountMinor === entry.amountMinor && existing.currency === entry.currency) {
        continue
      }
      await sync.write(
        'account_snapshot',
        { accountId: entry.accountId, month, amountMinor: entry.amountMinor, currency: entry.currency },
        id,
      )
    }
    if (setBalances) {
      for (const entry of entries) {
        if (entry.amountMinor !== null) await accountsStore.setBalance(entry.accountId, entry.amountMinor)
      }
    }
  }

  /** Record one account's figure for the current month — what "set balance" also does. */
  const checkInNow = (accountId: string, amountMinor: number, currency: string) =>
    saveMonth(currentMonth(), [{ accountId, amountMinor, currency }])

  return {
    loaded,
    snapshots,
    months,
    accounts,
    groups,
    heldOf,
    table,
    latest,
    latestChecked,
    inflation,
    flows,
    entered,
    pricingDay,
    saveMonth,
    checkInNow,
  }
})
