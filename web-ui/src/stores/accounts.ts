import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { today, type DayKey } from '@/lib/period'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'
import { useAuthStore } from './auth'
import { useFxStore } from './fx'
import { useTaxonomyStore } from './taxonomy'

export interface AccountInput {
  name: string
  currency: string
  icon: string
  color: string
  initialBalanceMinor: number
  /**
   * Where the money sits, as the person groups it: "Cash", "Banks", "Gold",
   * "Deposits"… Free text, because nobody's list is the same, and the groups
   * are simply the distinct values. Absent means ungrouped.
   */
  group?: string
  /**
   * Ready to use: money that can be spent this week (cash, a current account),
   * as opposed to money that exists but is not to hand (a term deposit, gold,
   * investments). Absent means yes — most accounts people record spending
   * against are exactly that.
   */
  liquid?: boolean
}

/** The heading accounts without a group are listed under. */
export const UNGROUPED = 'Other'

export const groupOf = (account: Row) => String(account.group ?? '').trim() || UNGROUPED
export const isLiquid = (account: Row) => account.liquid !== false

/** The groups people reach for first — offered as suggestions, never imposed. */
export const SUGGESTED_GROUPS = ['Cash', 'Banks', 'Savings', 'Deposits', 'Investments', 'Gold', 'Crypto']

/** Accounts under one heading, in the order they are listed. */
export interface AccountGroup {
  name: string
  accounts: Row[]
}

/** Every live balance in the base currency, added up the way the capital table adds a month. */
export interface LiveTotals {
  totalMinor: number
  readyMinor: number
  byGroup: Map<string, number>
  /** Accounts left out because their currency has no rate yet. */
  unconverted: number
}

/**
 * Accounts, and what is in them.
 *
 * Balances are **derived**, never stored — see docs/SYNC.md §2. That is not a
 * style choice: a stored running total is a counter, and merging counters across
 * devices needs a CRDT rather than the last-write-wins this app is built on.
 */
export const useAccountsStore = defineStore('accounts', () => {
  const taxonomy = useTaxonomyStore()
  const fx = useFxStore()
  const auth = useAuthStore()

  // Every transaction, not just the visible period: a balance is the whole
  // history by definition.
  const all = useLiveQuery<Row[]>(() => db.txn.where('deleted').equals(0).toArray(), [])

  /**
   * Every account's balance — at the end of `until` if given, otherwise now.
   *
   * The dated form is for the capital check-in, which offers what the ledger
   * says an account held at the end of a past month as the figure to confirm.
   */
  function computeBalances(until?: DayKey): Map<string, number> {
    const totals = new Map<string, number>()
    const currencies = new Map<string, string>()
    for (const account of taxonomy.accounts) {
      totals.set(String(account.id), Number(account.initialBalanceMinor ?? 0))
      currencies.set(String(account.id), String(account.currency ?? ''))
    }

    /**
     * An amount stated in the account's own currency, priced on the day.
     *
     * A record does not have to be in its account's currency — paying a euro
     * card in forint is ordinary — so this converts rather than skipping.
     * Skipping is what it used to do, and the effect was a balance that was
     * silently short by however much was spent abroad.
     */
    const inAccount = (minor: number, currency: string, accountId: string, day: string) => {
      const want = currencies.get(accountId) ?? ''
      if (!want || want === currency) return minor
      return fx.convert(minor, currency, want, day)
    }

    for (const row of all.value) {
      const day = String(row.occurredOn ?? '')
      if (until && day > until) continue
      const currency = String(row.currency ?? '')

      const from = String(row.accountId ?? '')
      if (totals.has(from)) {
        const debited = inAccount(Number(row.amountMinor ?? 0), currency, from, day)
        // null means no rate is known. Leaving the row out is still wrong, but
        // it is the only honest option — and the dashboard says how many.
        if (debited !== null) totals.set(from, totals.get(from)! + debited)
      }

      const to = String(row.toAccountId ?? '')
      if (!to || !totals.has(to)) continue
      // A transfer stores what left the source as a negative amount and what
      // arrived as a separate positive one, because a cross-currency transfer's
      // rate is not recoverable afterwards. That recorded pair is authoritative:
      // it is what the bank actually did, so it is used in preference to any
      // rate we hold.
      const credited = inAccount(
        Number(row.toAmountMinor ?? Math.abs(Number(row.amountMinor ?? 0))),
        String(row.toCurrency ?? currency),
        to,
        day,
      )
      if (credited !== null) totals.set(to, totals.get(to)! + credited)
    }
    return totals
  }

  const balances = computed(() => computeBalances())
  const balancesAt = (day: DayKey) => computeBalances(day)

  const balanceOf = (id: unknown) => balances.value.get(String(id)) ?? 0

  /** Whether any record touches an account — i.e. whether its ledger balance means anything. */
  const hasRecords = computed(() => {
    const ids = new Set<string>()
    for (const row of all.value) {
      ids.add(String(row.accountId ?? ''))
      if (row.toAccountId) ids.add(String(row.toAccountId))
    }
    return ids
  })

  /**
   * Accounts under their group headings.
   *
   * Groups come in the order of their first account, so dragging accounts into
   * order (sortOrder) is also how the groups are ordered — one ordering to
   * manage, not two. Ungrouped ones go last.
   */
  const groups = computed<AccountGroup[]>(() => {
    const out = new Map<string, Row[]>()
    for (const account of taxonomy.activeAccounts) {
      const name = groupOf(account)
      const list = out.get(name)
      if (list) list.push(account)
      else out.set(name, [account])
    }
    return [...out.entries()]
      .map(([name, accounts]) => ({ name, accounts }))
      .sort((a, b) => Number(a.name === UNGROUPED) - Number(b.name === UNGROUPED))
  })

  /** Every group name in use, for the group field's suggestions. */
  const groupNames = computed(() => {
    const names = new Set<string>()
    for (const a of taxonomy.accounts) {
      const g = String(a.group ?? '').trim()
      if (g) names.add(g)
    }
    return [...names].sort()
  })

  /**
   * Live balances in the base currency, priced today: net worth as the ledger
   * sees it right now. The capital table shows the checked-in history; this is
   * the "now" that sits beside it on the dashboard and the accounts screen.
   */
  const liveTotals = computed<LiveTotals>(() => {
    const base = auth.user?.baseCurrency ?? 'EUR'
    const day = today()
    const out: LiveTotals = { totalMinor: 0, readyMinor: 0, byGroup: new Map(), unconverted: 0 }
    for (const account of taxonomy.activeAccounts) {
      const own = balanceOf(account.id)
      const currency = String(account.currency ?? base)
      const minor = currency === base ? own : fx.convert(own, currency, base, day)
      if (minor === null) {
        out.unconverted++
        continue
      }
      out.totalMinor += minor
      if (isLiquid(account)) out.readyMinor += minor
      const group = groupOf(account)
      out.byGroup.set(group, (out.byGroup.get(group) ?? 0) + minor)
    }
    return out
  })

  const create = (input: AccountInput) =>
    sync.write('account', { ...input, sortOrder: taxonomy.accounts.length, archived: 0 })

  const update = (id: string, input: Partial<AccountInput> & { archived?: number }) => {
    const existing = taxonomy.accounts.find((a) => a.id === id)
    if (!existing) return Promise.resolve(id)
    const { id: _id, lamport: _l, deviceId: _d, updatedAt: _u, deleted: _del, ...body } = existing
    return sync.write('account', { ...body, ...input }, id)
  }

  /**
   * Archive rather than delete.
   *
   * Transactions keep pointing at the account, and a deleted one would leave
   * them showing nothing. Archiving takes it out of the pickers and totals while
   * history stays intact.
   */
  const archive = (id: string) => update(id, { archived: 1 })
  const restore = (id: string) => update(id, { archived: 0 })

  /**
   * Make an account's balance read `targetMinor` now.
   *
   * By moving its **opening** balance by the difference — never by storing a
   * balance, which would be a counter two devices could not merge (docs/SYNC.md
   * §2). Every record stays exactly as it was; the account simply started from
   * a different place. That is the honest reading of "the app says −35,000 but
   * I have 9,944 in cash": the records are fine, the starting point was never
   * entered.
   */
  function setBalance(id: string, targetMinor: number) {
    if (targetMinor === balanceOf(id)) return Promise.resolve(id)
    return update(id, { initialBalanceMinor: openingFor(id, targetMinor) })
  }

  /**
   * The opening balance that makes an account's balance read `targetMinor`.
   *
   * Exposed so a form saving other fields at the same time can write it **in
   * the same `update`**. Two updates in a row would race: the second spreads
   * the row as the live query last saw it, which may not include the first yet.
   */
  function openingFor(id: string, targetMinor: number): number {
    const account = taxonomy.accounts.find((a) => a.id === id)
    return Number(account?.initialBalanceMinor ?? 0) + (targetMinor - balanceOf(id))
  }

  return {
    balances,
    balanceOf,
    balancesAt,
    hasRecords,
    groups,
    groupNames,
    liveTotals,
    create,
    update,
    archive,
    restore,
    setBalance,
    openingFor,
  }
})
