import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'
import { useTaxonomyStore } from './taxonomy'

export interface AccountInput {
  name: string
  currency: string
  icon: string
  color: string
  initialBalanceMinor: number
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

  // Every transaction, not just the visible period: a balance is the whole
  // history by definition.
  const all = useLiveQuery<Row[]>(() => db.txn.where('deleted').equals(0).toArray(), [])

  const balances = computed(() => {
    const totals = new Map<string, number>()
    const currencies = new Map<string, string>()
    for (const account of taxonomy.accounts) {
      totals.set(String(account.id), Number(account.initialBalanceMinor ?? 0))
      currencies.set(String(account.id), String(account.currency ?? ''))
    }

    for (const row of all.value) {
      const from = String(row.accountId ?? '')
      // Only amounts in the account's own currency. Converting would need a rate
      // for the day of the transaction, which arrives with FX in phase 5;
      // summing regardless would produce a confident, wrong number.
      if (totals.has(from) && row.currency === currencies.get(from)) {
        totals.set(from, totals.get(from)! + Number(row.amountMinor ?? 0))
      }

      const to = String(row.toAccountId ?? '')
      if (!to || !totals.has(to)) continue
      const toCurrency = String(row.toCurrency ?? row.currency ?? '')
      if (toCurrency !== currencies.get(to)) continue
      // A transfer stores what left the source as a negative amount and what
      // arrived as a separate positive one, because a cross-currency transfer's
      // rate is not recoverable afterwards.
      const credited = Number(row.toAmountMinor ?? Math.abs(Number(row.amountMinor ?? 0)))
      totals.set(to, totals.get(to)! + credited)
    }
    return totals
  })

  const balanceOf = (id: unknown) => balances.value.get(String(id)) ?? 0

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

  return { balances, balanceOf, create, update, archive, restore }
})
