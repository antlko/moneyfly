import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { API_BASE, api } from '@/api/client'
import type { ReportCategories, ReportSummary } from '@/api/types'

/**
 * The multi-period reports: the workbook's grid, as data.
 *
 * The store never computes a figure. Every number on the year screen — average,
 * total, spend total, possible minimum, diff, saved % — is produced by the
 * metrics engine on the server, because a second implementation on the client
 * would be a second set of answers (conventions §10).
 */
export const useReportsStore = defineStore('reports', () => {
  const grid = ref<ReportCategories | null>(null)
  const summary = ref<ReportSummary | null>(null)
  const loading = ref(false)
  const error = ref('')

  /** Months in range that hold no data at all. They render blank, not zero. */
  const blankMonths = computed(
    () => grid.value?.periods.filter((p) => !grid.value?.recorded[p]) ?? [],
  )

  function query(from?: string, to?: string): string {
    if (!from || !to) return ''
    return `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
  }

  async function load(from?: string, to?: string): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      grid.value = await api.get<ReportCategories>(
        `${API_BASE}/reports/categories${query(from, to)}`,
      )
      summary.value = {
        from: grid.value.from,
        to: grid.value.to,
        base_currency: grid.value.base_currency,
        fiscal_year: '',
        periods: grid.value.summary,
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'The report could not be loaded.'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function loadSummary(from?: string, to?: string): Promise<void> {
    summary.value = await api.get<ReportSummary>(`${API_BASE}/reports/summary${query(from, to)}`)
  }

  return { grid, summary, loading, error, blankMonths, load, loadSummary }
})
