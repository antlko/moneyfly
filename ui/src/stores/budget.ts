import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { Budget, BudgetReport, Money } from '@/api/types'

export const useBudgetStore = defineStore('budget', () => {
  const report = ref<BudgetReport | null>(null)
  const plans = ref<Budget[]>([])
  const loading = ref(false)

  /**
   * Loads plan versus actual for one month. Everything shown — including the
   * threshold state — is computed server-side, so every client colours identically.
   */
  async function loadReport(period: string): Promise<void> {
    loading.value = true
    try {
      report.value = await api.get<BudgetReport>(`/api/v1/reports/budget?period=${period}`)
    } finally {
      loading.value = false
    }
  }

  async function loadPlans(period: string): Promise<void> {
    plans.value = await api.get<Budget[]>(`/api/v1/budgets?period=${period}`)
  }

  async function setPlan(categoryID: number, period: string, planned: Money): Promise<void> {
    await api.put(`/api/v1/budgets/${categoryID}/${period}`, { planned })
    await Promise.all([loadPlans(period), loadReport(period)])
  }

  async function removePlan(categoryID: number, period: string): Promise<void> {
    await api.delete(`/api/v1/budgets/${categoryID}/${period}`)
    await Promise.all([loadPlans(period), loadReport(period)])
  }

  /** Seeds a range from one figure per category, so nobody types 216 values. */
  async function bulkSeed(
    fromPeriod: string,
    toPeriod: string,
    items: { category_id: number; planned: Money }[],
  ): Promise<{ periods_written: number; rows_written: number }> {
    return api.post('/api/v1/budgets/bulk', {
      from_period: fromPeriod,
      to_period: toPeriod,
      items,
    })
  }

  return { report, plans, loading, loadReport, loadPlans, setPlan, removePlan, bulkSeed }
})
