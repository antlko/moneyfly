import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'
import { useFxStore } from './fx'
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
  const fx = useFxStore()

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
