import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { Account, AccountInput, Alias, Category, CategoryInput, Currency } from '@/api/types'

/** One store per domain area: categories, accounts, aliases and currencies. */
export const useTaxonomyStore = defineStore('taxonomy', () => {
  const categories = ref<Category[]>([])
  const accounts = ref<Account[]>([])
  const currencies = ref<Currency[]>([])
  const categoryAliases = ref<Alias[]>([])
  const accountAliases = ref<Alias[]>([])
  const loading = ref(false)
  const loaded = ref(false)

  const expenseCategories = computed(() => categories.value.filter((c) => c.kind === 'expense'))
  const incomeCategories = computed(() => categories.value.filter((c) => c.kind === 'income'))
  /** Only leaf accounts can be posted to: a parent's value is the sum of its children. */
  const postableAccounts = computed(() => accounts.value.filter((a) => !a.is_computed))

  const exponentOf = computed(() => (code: string) => {
    return currencies.value.find((c) => c.code === code)?.exponent ?? 2
  })

  async function load(force = false): Promise<void> {
    if (loaded.value && !force) return
    loading.value = true
    try {
      const [cats, accs, curr] = await Promise.all([
        api.get<Category[]>('/api/v1/categories'),
        api.get<Account[]>('/api/v1/accounts'),
        api.get<Currency[]>('/api/v1/currencies'),
      ])
      categories.value = cats
      accounts.value = accs
      currencies.value = curr
      loaded.value = true
    } finally {
      loading.value = false
    }
  }

  async function loadArchived(): Promise<Category[]> {
    return api.get<Category[]>('/api/v1/categories?include_archived=true')
  }

  async function loadAliases(): Promise<void> {
    const [cat, acc] = await Promise.all([
      api.get<Alias[]>('/api/v1/categories/aliases'),
      api.get<Alias[]>('/api/v1/accounts/aliases'),
    ])
    categoryAliases.value = cat
    accountAliases.value = acc
  }

  async function createCategory(input: CategoryInput): Promise<Category> {
    const created = await api.post<Category>('/api/v1/categories', input)
    await load(true)
    return created
  }

  async function updateCategory(id: number, input: CategoryInput): Promise<Category> {
    const updated = await api.patch<Category>(`/api/v1/categories/${id}`, input)
    await load(true)
    return updated
  }

  async function archiveCategory(id: number): Promise<void> {
    await api.delete(`/api/v1/categories/${id}`)
    await load(true)
  }

  async function mergeCategory(id: number, intoID: number): Promise<void> {
    await api.post(`/api/v1/categories/${id}/merge`, { into_id: intoID })
    await Promise.all([load(true), loadAliases()])
  }

  async function reorderCategories(ordered: Category[]): Promise<void> {
    // Sent one at a time: the list is 20 rows, and a bulk endpoint for this would
    // be API surface with one caller.
    await Promise.all(
      ordered.map((c, index) =>
        api.patch(`/api/v1/categories/${c.id}`, {
          name: c.name,
          kind: c.kind,
          is_essential: c.is_essential,
          icon: c.icon ?? undefined,
          color: c.color ?? undefined,
          sort_order: index + 1,
          parent_id: c.parent_id,
        }),
      ),
    )
    await load(true)
  }

  async function createAccount(input: AccountInput): Promise<Account> {
    const created = await api.post<Account>('/api/v1/accounts', input)
    await load(true)
    return created
  }

  async function updateAccount(id: number, input: AccountInput): Promise<Account> {
    const updated = await api.patch<Account>(`/api/v1/accounts/${id}`, input)
    await load(true)
    return updated
  }

  async function archiveAccount(id: number): Promise<void> {
    await api.delete(`/api/v1/accounts/${id}`)
    await load(true)
  }

  async function createCategoryAlias(categoryID: number, sourceName: string): Promise<void> {
    await api.post('/api/v1/categories/aliases', {
      category_id: categoryID,
      source_name: sourceName,
    })
    await loadAliases()
  }

  async function deleteCategoryAlias(id: number): Promise<void> {
    await api.delete(`/api/v1/categories/aliases/${id}`)
    await loadAliases()
  }

  async function createAccountAlias(accountID: number, sourceName: string): Promise<void> {
    await api.post('/api/v1/accounts/aliases', { account_id: accountID, source_name: sourceName })
    await loadAliases()
  }

  async function deleteAccountAlias(id: number): Promise<void> {
    await api.delete(`/api/v1/accounts/aliases/${id}`)
    await loadAliases()
  }

  function categoryByID(id: number | null | undefined): Category | undefined {
    if (id === null || id === undefined) return undefined
    return categories.value.find((c) => c.id === id)
  }

  function accountByID(id: number | null | undefined): Account | undefined {
    if (id === null || id === undefined) return undefined
    return accounts.value.find((a) => a.id === id)
  }

  return {
    categories,
    accounts,
    currencies,
    categoryAliases,
    accountAliases,
    loading,
    loaded,
    expenseCategories,
    incomeCategories,
    postableAccounts,
    exponentOf,
    load,
    loadArchived,
    loadAliases,
    createCategory,
    updateCategory,
    archiveCategory,
    mergeCategory,
    reorderCategories,
    createAccount,
    updateAccount,
    archiveAccount,
    createCategoryAlias,
    deleteCategoryAlias,
    createAccountAlias,
    deleteAccountAlias,
    categoryByID,
    accountByID,
  }
})
