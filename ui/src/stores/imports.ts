import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { API_BASE, ApiError, api, type ProblemDetail } from '@/api/client'
import type { ImportBatch, ImportMapping, ImportPreview, ImportRow } from '@/api/types'

/**
 * The import pipeline as the UI sees it (docs/08-ux.md §8.6).
 *
 * The mapping step is deliberately blocking: while `unmapped` is non-empty the
 * commit action does not exist in this store, which mirrors the server refusing
 * the same commit with a 409. Two independent guards, because a silently
 * discarded category is the failure this whole feature exists to prevent.
 */
export const useImportStore = defineStore('imports', () => {
  const batches = ref<ImportBatch[]>([])
  const preview = ref<ImportPreview | null>(null)
  const rows = ref<ImportRow[]>([])
  const uploading = ref(false)
  const busy = ref(false)
  /** Set when an identical file had already been committed. */
  const alreadyImported = ref<ImportBatch | null>(null)

  const needsMapping = computed(
    () =>
      (preview.value?.unmapped_categories.length ?? 0) > 0 ||
      (preview.value?.unmapped_accounts.length ?? 0) > 0,
  )

  const canCommit = computed(
    () =>
      preview.value !== null &&
      preview.value.status === 'previewed' &&
      preview.value.rows_unmapped === 0,
  )

  async function loadBatches(): Promise<void> {
    batches.value = await api.get<ImportBatch[]>(`${API_BASE}/imports`)
  }

  /**
   * Uploads a file. multipart/form-data cannot go through the JSON client, so
   * this is the one place that calls fetch directly; the error shape is decoded
   * into the same ApiError every other call raises.
   */
  async function upload(file: File): Promise<ImportBatch> {
    uploading.value = true
    alreadyImported.value = null
    try {
      const body = new FormData()
      body.append('file', file)
      const response = await fetch(`${API_BASE}/imports`, {
        method: 'POST',
        body,
        credentials: 'same-origin',
      })
      const text = await response.text()
      if (!response.ok) {
        let problem: ProblemDetail | null = null
        try {
          problem = text ? (JSON.parse(text) as ProblemDetail) : null
        } catch {
          problem = null
        }
        throw new ApiError(response.status, problem)
      }
      const batch = JSON.parse(text) as ImportBatch
      if (batch.already_imported) alreadyImported.value = batch
      await Promise.all([loadPreview(batch.id), loadBatches()])
      return batch
    } finally {
      uploading.value = false
    }
  }

  async function loadPreview(batchID: number): Promise<void> {
    preview.value = await api.get<ImportPreview>(`${API_BASE}/imports/${batchID}`)
  }

  async function loadRows(batchID: number, status?: string): Promise<void> {
    const query = status ? `?status=${encodeURIComponent(status)}` : ''
    rows.value = await api.get<ImportRow[]>(`${API_BASE}/imports/${batchID}/rows${query}`)
  }

  /** Confirming a suggestion is what applies it; nothing is mapped automatically. */
  async function applyMappings(
    batchID: number,
    mappings: { categories?: ImportMapping[]; accounts?: ImportMapping[] },
  ): Promise<void> {
    busy.value = true
    try {
      preview.value = await api.post<ImportPreview>(`${API_BASE}/imports/${batchID}/mappings`, {
        categories: mappings.categories ?? [],
        accounts: mappings.accounts ?? [],
      })
    } finally {
      busy.value = false
    }
  }

  async function commit(batchID: number): Promise<ImportBatch> {
    busy.value = true
    try {
      const batch = await api.post<ImportBatch>(`${API_BASE}/imports/${batchID}/commit`)
      await Promise.all([loadPreview(batchID), loadBatches()])
      return batch
    } finally {
      busy.value = false
    }
  }

  async function revert(batchID: number): Promise<ImportBatch> {
    busy.value = true
    try {
      const batch = await api.post<ImportBatch>(`${API_BASE}/imports/${batchID}/revert`)
      await loadBatches()
      if (preview.value?.batch_id === batchID) await loadPreview(batchID)
      return batch
    } finally {
      busy.value = false
    }
  }

  function reset(): void {
    preview.value = null
    rows.value = []
    alreadyImported.value = null
  }

  return {
    batches,
    preview,
    rows,
    uploading,
    busy,
    alreadyImported,
    needsMapping,
    canCommit,
    loadBatches,
    upload,
    loadPreview,
    loadRows,
    applyMappings,
    commit,
    revert,
    reset,
  }
})
