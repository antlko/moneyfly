import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { categoryId, DEFAULT_ACCOUNT_ID, DEFAULT_CATEGORIES } from '@/lib/categories'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'

/** Categories and accounts, live from the replica. */
export const useTaxonomyStore = defineStore('taxonomy', () => {
  const categories = useLiveQuery<Row[]>(() => db.category.where('deleted').equals(0).toArray(), [])
  const accounts = useLiveQuery<Row[]>(() => db.account.where('deleted').equals(0).toArray(), [])

  const bySortOrder = (a: Row, b: Row) =>
    Number(a.sortOrder ?? 0) - Number(b.sortOrder ?? 0) ||
    String(a.name).localeCompare(String(b.name))

  const expenseCategories = computed(() =>
    categories.value.filter((c) => c.kind === 'expense' && !c.archived).sort(bySortOrder),
  )
  const incomeCategories = computed(() =>
    categories.value.filter((c) => c.kind === 'income' && !c.archived).sort(bySortOrder),
  )
  const byId = computed(() => new Map(categories.value.map((c) => [c.id, c])))

  const activeAccounts = computed(() => accounts.value.filter((a) => !a.archived).sort(bySortOrder))

  /**
   * Give a brand-new account its starting categories.
   *
   * Runs after the first sync, and only when the replica is genuinely empty. The
   * ids are derived from a fixed key, so two devices seeding at the same moment
   * write identical rows and last-write-wins collapses them — no coordination,
   * no duplicates. See DEFAULT_CATEGORIES.
   */
  let seeded = false

  async function ensureSeeded(baseCurrency: string): Promise<void> {
    // Called again after every sync revision, in case the first attempt ran
    // before the replica arrived. Once there is anything to see, stop asking.
    if (seeded) return
    // Tombstones count here: someone who deleted every category on purpose
    // has not got an empty replica, and must not have the defaults pushed back.
    await seed(baseCurrency, (table) => table.count())
    seeded = true
  }

  /**
   * Put the starting categories and Cash account back after "erase
   * everything" (Settings → Data), which leaves nothing but tombstones.
   *
   * Counts live rows only — the one difference from `ensureSeeded`. Rewriting
   * the fixed default ids is safe: the local clock has already passed the
   * erase's tombstones by the time this runs (the caller syncs first), so these
   * writes win, and two devices doing it at once still collapse by id.
   */
  async function restoreDefaults(baseCurrency: string): Promise<void> {
    await seed(baseCurrency, (table) => table.where('deleted').equals(0).count())
  }

  async function seed(
    baseCurrency: string,
    count: (table: typeof db.category) => Promise<number>,
  ): Promise<void> {
    if ((await count(db.category)) === 0) {
      for (const [index, seed] of DEFAULT_CATEGORIES.entries()) {
        await sync.write(
          'category',
          {
            name: seed.name,
            kind: seed.kind,
            icon: seed.icon,
            color: seed.color,
            sortOrder: index,
            archived: 0,
          },
          categoryId(seed.key),
        )
      }
    }

    if ((await count(db.account)) === 0) {
      await sync.write(
        'account',
        {
          name: 'Cash',
          currency: baseCurrency,
          icon: 'Wallet',
          color: 'green',
          initialBalanceMinor: 0,
          sortOrder: 0,
          archived: 0,
        },
        DEFAULT_ACCOUNT_ID,
      )
    }
  }

  return {
    categories,
    accounts,
    expenseCategories,
    incomeCategories,
    activeAccounts,
    byId,
    ensureSeeded,
    restoreDefaults,
  }
})
