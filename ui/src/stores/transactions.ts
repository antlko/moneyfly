import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, idempotencyKey } from '@/api/client'
import type { Transaction, TransactionInput, TransactionPage } from '@/api/types'

export interface TransactionFilter {
  from?: string
  to?: string
  categoryID?: number
  accountID?: number
  kind?: string
  query?: string
}

function toQuery(filter: TransactionFilter, cursor?: string, limit = 50): string {
  const params = new URLSearchParams()
  if (filter.from) params.set('from', filter.from)
  if (filter.to) params.set('to', filter.to)
  if (filter.categoryID) params.set('category_id', String(filter.categoryID))
  if (filter.accountID) params.set('account_id', String(filter.accountID))
  if (filter.kind) params.set('kind', filter.kind)
  if (filter.query) params.set('q', filter.query)
  if (cursor) params.set('cursor', cursor)
  params.set('limit', String(limit))
  return params.toString()
}

export const useTransactionsStore = defineStore('transactions', () => {
  const items = ref<Transaction[]>([])
  const cursor = ref<string | null>(null)
  const hasMore = ref(false)
  const loading = ref(false)
  const lastCreated = ref<Transaction | null>(null)

  /** Loads the first page for a filter, replacing whatever is held. */
  async function load(filter: TransactionFilter): Promise<void> {
    loading.value = true
    try {
      const page = await api.get<TransactionPage>(`/api/v1/transactions?${toQuery(filter)}`)
      items.value = page.items
      cursor.value = page.next_cursor
      hasMore.value = page.has_more
    } finally {
      loading.value = false
    }
  }

  /** Appends the next page — the history screen's infinite scroll. */
  async function loadMore(filter: TransactionFilter): Promise<void> {
    if (!hasMore.value || !cursor.value || loading.value) return
    loading.value = true
    try {
      const page = await api.get<TransactionPage>(
        `/api/v1/transactions?${toQuery(filter, cursor.value)}`,
      )
      items.value = [...items.value, ...page.items]
      cursor.value = page.next_cursor
      hasMore.value = page.has_more
    } finally {
      loading.value = false
    }
  }

  /**
   * Creates a transaction, carrying an Idempotency-Key so a retry of a save that
   * timed out returns the original row instead of creating a second one.
   */
  async function create(input: TransactionInput): Promise<Transaction> {
    const created = await api.post<Transaction>('/api/v1/transactions', input, {
      'Idempotency-Key': idempotencyKey(),
    })
    lastCreated.value = created
    return created
  }

  async function remove(id: number): Promise<void> {
    await api.delete(`/api/v1/transactions/${id}`)
    items.value = items.value.filter((t) => t.id !== id)
  }

  async function get(id: number): Promise<Transaction> {
    return api.get<Transaction>(`/api/v1/transactions/${id}`)
  }

  return { items, cursor, hasMore, loading, lastCreated, load, loadMore, create, remove, get }
})
