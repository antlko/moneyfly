import { defineStore } from 'pinia'
import { computed } from 'vue'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { currentMonth, monthBounds } from '@/lib/period'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'
import { useDashboardStore } from './dashboard'
import { useFxStore } from './fx'

export interface BudgetInput {
  limitMinor: number
  currency: string
  /** Absent means an overall cap across every category. */
  categoryId?: string
}

export interface BudgetProgress {
  budget: Row
  spentMinor: number
  /** limitMinor's own currency, spent ÷ limit — can exceed 1. 0 for a zero limit. */
  share: number
  /** Expenses this month that could not be priced into the budget's currency. */
  unconverted: number
}

/**
 * Budgets, and how much of each has been spent this month.
 *
 * Deliberately smaller than what the schema already allows —
 * backend/internal/sync/op.go's validateBudget also accepts a fixed `period`
 * for a one-off month, and any currency. Every budget created here recurs
 * every calendar month (no `period` written, which is what an absent one
 * means) and is denominated in the base currency. Both are the common case;
 * neither forecloses the rest arriving later without a schema change.
 */
export const useBudgetsStore = defineStore('budgets', () => {
  const dashboard = useDashboardStore()
  const fx = useFxStore()

  const budgets = useLiveQuery<Row[]>(() => db.budget.where('deleted').equals(0).toArray(), [])

  // The whole ledger, exactly like dashboard.ts's own allRows: one
  // subscription, sliced by string comparison per budget rather than
  // re-queried for each one.
  const allTxns = useLiveQuery<Row[]>(
    () => db.txn.where('[deleted+occurredOn]').between([0, ''], [0, '￿']).toArray(),
    [],
  )

  /** This calendar month's expenses — a budget tracks the real month, not
   * whichever period the dashboard happens to be showing. */
  const monthExpenses = computed(() => {
    const { from, to } = monthBounds(currentMonth())
    return allTxns.value.filter((r) => {
      if (r.kind !== 'expense') return false
      const day = String(r.occurredOn ?? '')
      return day >= from && day <= to
    })
  })

  /**
   * A row priced into a budget's own currency, on the day it happened — the
   * same rule dashboard.ts's inBase applies, so a budget and the dashboard
   * never disagree about what one euro spent in forint was worth.
   *
   * null means no rate is known. Per docs/CLAUDE.md's FX invariant such a row
   * must not be silently dropped from the total with nothing to show for it,
   * so the caller counts it instead (see BudgetProgress.unconverted).
   */
  function inCurrency(row: Row, currency: string): number | null {
    const rowCurrency = String(row.currency ?? dashboard.baseCurrency)
    if (rowCurrency === currency) return Number(row.amountMinor ?? 0)
    return fx.convert(Number(row.amountMinor ?? 0), rowCurrency, currency, String(row.occurredOn ?? ''))
  }

  function spentOn(budget: Row): { spentMinor: number; unconverted: number } {
    const currency = String(budget.currency)
    const categoryId = budget.categoryId ? String(budget.categoryId) : null

    let spentMinor = 0
    let unconverted = 0
    for (const row of monthExpenses.value) {
      if (categoryId && String(row.categoryId ?? '') !== categoryId) continue
      const priced = inCurrency(row, currency)
      if (priced === null) unconverted++
      else spentMinor += Math.abs(priced)
    }
    return { spentMinor, unconverted }
  }

  const progress = computed<BudgetProgress[]>(() =>
    budgets.value.map((budget) => {
      const { spentMinor, unconverted } = spentOn(budget)
      const limitMinor = Number(budget.limitMinor ?? 0)
      return { budget, spentMinor, unconverted, share: limitMinor > 0 ? spentMinor / limitMinor : 0 }
    }),
  )

  const create = (input: BudgetInput) => sync.write('budget', { ...input })

  return { budgets, progress, create }
})
