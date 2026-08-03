import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { API_BASE, api } from '@/api/client'
import type {
  BurnMode,
  CapitalReport,
  CapitalSeries,
  DriftRow,
  Money,
  SnapshotRow,
} from '@/api/types'

/**
 * Capital: net worth, allocation, runway and the snapshot screen.
 *
 * Every figure here is computed on the server. The client does not add up
 * accounts, does not convert currencies and does not derive a percentage — a
 * second implementation would be a second set of answers, and the whole point of
 * the parity suite is that there is one (conventions §10).
 */
export const useCapitalStore = defineStore('capital', () => {
  const report = ref<CapitalReport | null>(null)
  const series = ref<CapitalSeries | null>(null)
  const snapshots = ref<SnapshotRow[]>([])
  const drift = ref<DriftRow[]>([])
  const burnMode = ref<BurnMode>('actual_trailing_3')
  const loading = ref(false)

  /** Accounts that hold value directly; parents are shown but never edited. */
  const editable = computed(() => snapshots.value.filter((s) => !s.computed))

  /** Rows where the transactions imply a different balance. Informational only. */
  const drifted = computed(() =>
    drift.value.filter((d) => d.difference !== null && d.difference.amount_minor !== 0),
  )

  async function loadReport(period: string): Promise<void> {
    loading.value = true
    try {
      report.value = await api.get<CapitalReport>(
        `${API_BASE}/reports/capital?period=${period}&burn_mode=${burnMode.value}`,
      )
    } finally {
      loading.value = false
    }
  }

  async function loadSeries(from?: string, to?: string): Promise<void> {
    const range = from && to ? `&from=${from}&to=${to}` : ''
    series.value = await api.get<CapitalSeries>(
      `${API_BASE}/reports/capital/series?burn_mode=${burnMode.value}${range}`,
    )
  }

  async function loadSnapshots(period: string): Promise<void> {
    snapshots.value = await api.get<SnapshotRow[]>(`${API_BASE}/snapshots?period=${period}`)
  }

  async function loadDrift(period: string): Promise<void> {
    const body = await api.get<{ items: DriftRow[] }>(
      `${API_BASE}/snapshots/${period}/reconciliation`,
    )
    drift.value = body.items
  }

  /** Saves the whole month in one request, so a half-entered screen cannot exist. */
  async function saveSnapshots(
    period: string,
    items: { account_id: number; amount: Money }[],
  ): Promise<void> {
    await api.post(`${API_BASE}/snapshots/bulk`, { period, items })
    await loadSnapshots(period)
  }

  async function setBurnMode(mode: BurnMode, period: string): Promise<void> {
    burnMode.value = mode
    await loadReport(period)
  }

  return {
    report,
    series,
    snapshots,
    drift,
    burnMode,
    loading,
    editable,
    drifted,
    loadReport,
    loadSeries,
    loadSnapshots,
    loadDrift,
    saveSnapshots,
    setBurnMode,
  }
})
