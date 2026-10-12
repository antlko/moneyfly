<script setup lang="ts">
import {
  ArrowDown,
  ArrowRight,
  ChartColumnBig,
  ChartPie,
  ChevronLeft,
  ChevronRight,
  Search,
  X,
} from '@lucide/vue'
import type { Component } from 'vue'
import { computed, ref, useTemplateRef, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import { toMajor } from '@/lib/money'
import { shortDate } from '@/lib/period'
import type { PeriodKind } from '@/lib/period'
import { useAccountsStore } from '@/stores/accounts'
import { useDashboardStore } from '@/stores/dashboard'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'
import CategoryPie from './CategoryPie.vue'

/**
 * The desktop analytics dashboard's content — everything except the sidebar,
 * which DesktopShell provides once for every route.
 *
 * Every figure here reads the same stores the mobile dashboard does
 * (dashboard.ts, accounts.ts, taxonomy.ts): this is a second *view* of the
 * same computed data, not a second implementation of it. A total that
 * disagreed between the phone and the desktop dashboard would be far worse
 * than either one looking plain.
 */
const dashboard = useDashboardStore()
const accounts = useAccountsStore()
const taxonomy = useTaxonomyStore()
const settings = useSettingsStore()
const router = useRouter()

/** Pie or bars — a synced preference, like the phone's own donut/list toggle. */
type ChartStyle = 'pie' | 'bars'
const chartStyle = computed<ChartStyle>(() => settings.get(SETTING.desktopChart, 'pie'))
const setChartStyle = (style: ChartStyle) => settings.set(SETTING.desktopChart, style)
const CHART_STYLES: { style: ChartStyle; label: string; icon: Component }[] = [
  { style: 'pie', label: 'Pie', icon: ChartPie },
  { style: 'bars', label: 'Bars', icon: ChartColumnBig },
]

const PERIOD_KINDS: { kind: PeriodKind; label: string }[] = [
  { kind: 'month', label: 'Month' },
  { kind: 'week', label: 'Week' },
  { kind: 'day', label: 'Day' },
  { kind: 'year', label: 'Year' },
  { kind: 'all', label: 'All time' },
]

/**
 * The records table: every record in the period, filtered and paged.
 *
 * It used to be "the 50 most recent", with no way past the fiftieth and no way
 * to narrow it except by clicking a slice of the chart above — so the one
 * filter that existed was hidden in a different card, and finding last month's
 * pharmacy receipt meant Search, a different screen. The filters now sit on the
 * table they filter, and the chart's click is simply one way of setting the
 * category filter.
 */
type KindFilter = 'all' | 'expense' | 'income' | 'transfer'
const KIND_FILTERS: { kind: KindFilter; label: string }[] = [
  { kind: 'all', label: 'All' },
  { kind: 'expense', label: 'Expenses' },
  { kind: 'income', label: 'Income' },
  { kind: 'transfer', label: 'Transfers' },
]
const PAGE_SIZES = [25, 50, 100] as const

const query = ref('')
const kindFilter = ref<KindFilter>('all')
const accountFilter = ref<string | null>(null)
/**
 * The category being looked at — set from the table's own filter or by
 * clicking the chart. Kept across period changes, so paging through months
 * follows one category.
 */
const selectedCategory = ref<string | null>(null)
const page = ref(1)
const pageSize = ref<(typeof PAGE_SIZES)[number]>(25)

const recordsSection = useTemplateRef<HTMLElement>('records')

type SortKey = 'date' | 'amount'
const sortKey = ref<SortKey>('date')

function selectCategory(id: string) {
  if (selectedCategory.value === id) return clearCategory()
  selectedCategory.value = id
  // Largest first: the point of opening a category is usually its biggest items.
  sortKey.value = 'amount'
  recordsSection.value?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
}

function clearCategory() {
  selectedCategory.value = null
  sortKey.value = 'date'
}

const filtersActive = computed(
  () =>
    query.value.trim() !== '' ||
    kindFilter.value !== 'all' ||
    accountFilter.value !== null ||
    selectedCategory.value !== null,
)

function clearFilters() {
  query.value = ''
  kindFilter.value = 'all'
  accountFilter.value = null
  clearCategory()
}

/** Category options, expense ones first — the order the record screen offers them in. */
const categoryOptions = computed(() => [
  { label: 'Expenses', items: taxonomy.expenseCategories },
  { label: 'Income', items: taxonomy.incomeCategories },
])

/** The selected category's row — from the taxonomy, so it exists even in a period with no spending in it. */
const selectedCategoryRow = computed(() =>
  selectedCategory.value ? (taxonomy.byId.get(selectedCategory.value) ?? null) : null,
)
const selectedName = computed(() => String(selectedCategoryRow.value?.name ?? ''))

/**
 * A row's size for sorting, in the base currency so a forint and a euro record
 * compare sensibly. An unconvertible row falls back to its own figure rather
 * than vanishing from the list.
 */
const size = (row: Row) => Math.abs(dashboard.inBase(row) ?? Number(row.amountMinor ?? 0))

const accountName = (id: unknown) => taxonomy.accounts.find((a) => a.id === id)?.name ?? ''
const categoryName = (row: Row) =>
  row.kind === 'transfer' ? 'Transfer' : String(taxonomy.byId.get(String(row.categoryId))?.name ?? '')

/**
 * Free text matches what is on the row as it reads: the note, the category,
 * the account, and the amount as typed (`12.5` finds €12.50).
 */
function matches(row: Row, q: string): boolean {
  // In the row's own currency's units — HUF has no decimals, so never `/ 100`.
  const amount = String(toMajor(Math.abs(Number(row.amountMinor ?? 0)), String(row.currency ?? '')))
  return [row.note, categoryName(row), accountName(row.accountId), accountName(row.toAccountId), amount]
    .some((field) => String(field ?? '').toLowerCase().includes(q))
}

const filteredRows = computed(() => {
  const q = query.value.trim().toLowerCase()
  const rows = dashboard.rows.filter((r) => {
    if (kindFilter.value !== 'all' && r.kind !== kindFilter.value) return false
    if (selectedCategory.value && String(r.categoryId ?? '') !== selectedCategory.value) return false
    if (accountFilter.value && r.accountId !== accountFilter.value && r.toAccountId !== accountFilter.value) {
      return false
    }
    return !q || matches(r, q)
  })
  return rows.sort((a, b) =>
    sortKey.value === 'amount'
      ? size(b) - size(a)
      : String(b.occurredOn).localeCompare(String(a.occurredOn)),
  )
})

const pageCount = computed(() => Math.max(1, Math.ceil(filteredRows.value.length / pageSize.value)))
const tableRows = computed(() =>
  filteredRows.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
const rangeLabel = computed(() => {
  const total = filteredRows.value.length
  if (total === 0) return '0 records'
  const from = (page.value - 1) * pageSize.value + 1
  const to = Math.min(total, page.value * pageSize.value)
  return `${from}–${to} of ${total}`
})

/**
 * Page numbers to offer: the first, the last, and two either side of the
 * current one, with gaps marked. Enough to jump anywhere in two clicks without
 * a row of forty buttons.
 */
const pageButtons = computed<(number | '…')[]>(() => {
  const n = pageCount.value
  const wanted = new Set([1, n, page.value - 1, page.value, page.value + 1].filter((p) => p >= 1 && p <= n))
  const sorted = [...wanted].sort((a, b) => a - b)
  const out: (number | '…')[] = []
  sorted.forEach((p, i) => {
    if (i > 0 && p - sorted[i - 1]! > 1) out.push('…')
    out.push(p)
  })
  return out
})

// Any change to what is being listed starts again from its first page — page
// 7 of a different filter is a page that may not exist.
watch([query, kindFilter, accountFilter, selectedCategory, sortKey, pageSize, () => dashboard.period], () => {
  page.value = 1
})
// …and a page that stopped existing (a record deleted elsewhere) clamps.
watch(pageCount, (n) => {
  if (page.value > n) page.value = n
})

/**
 * Total, share, largest and average for the selected category — over exactly
 * the rows listed, so a search or an account filter on top narrows the figures
 * with the table instead of leaving them describing something else.
 */
const selectedStats = computed(() => {
  if (!selectedCategory.value) return null
  const sizes = filteredRows.value
    .map((r) => dashboard.inBase(r))
    .filter((m): m is number => m !== null)
    .map(Math.abs)
  if (!sizes.length) return null
  const total = sizes.reduce((a, b) => a + b, 0)
  const spent = Math.abs(dashboard.expenseMinor)
  return {
    total,
    share: spent ? total / spent : 0,
    largest: Math.max(...sizes),
    average: Math.round(total / sizes.length),
  }
})

function openRecord(row: Row) {
  void router.push(`/edit/${row.id}`)
}
</script>

<template>
  <div class="space-y-6">
    <!-- Period -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-2">
        <button
          v-if="dashboard.pageable"
          type="button"
          aria-label="Previous period"
          class="grid size-8 place-items-center rounded-full text-mf-muted hover:bg-mf-surface"
          @click="dashboard.step(-1)"
        >
          <ChevronLeft :size="18" />
        </button>
        <h1 class="min-w-40 text-center text-xl font-semibold text-mf-ink">{{ dashboard.label }}</h1>
        <button
          v-if="dashboard.pageable"
          type="button"
          aria-label="Next period"
          class="grid size-8 place-items-center rounded-full text-mf-muted hover:bg-mf-surface"
          @click="dashboard.step(1)"
        >
          <ChevronRight :size="18" />
        </button>
      </div>

      <div class="flex gap-1 rounded-full bg-mf-surface p-1">
        <button
          v-for="p in PERIOD_KINDS"
          :key="p.kind"
          type="button"
          class="rounded-full px-3 py-1.5 text-sm"
          :class="
            dashboard.period.kind === p.kind
              ? 'bg-mf-green text-white'
              : 'text-mf-ink hover:bg-mf-bg'
          "
          @click="dashboard.setPeriodKind(p.kind)"
        >
          {{ p.label }}
        </button>
      </div>
    </div>

    <p v-if="dashboard.unconvertedCount > 0" class="rounded-lg bg-mf-red/15 px-4 py-2 text-sm text-mf-red-text">
      {{ dashboard.unconvertedCount }} record{{ dashboard.unconvertedCount === 1 ? '' : 's' }} not
      counted below — no exchange rate for that day yet.
    </p>

    <!-- Summary cards -->
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
      <div class="rounded-2xl bg-mf-surface p-5">
        <p class="text-sm text-mf-muted">Income</p>
        <MoneyAmount :minor="dashboard.incomeMinor" :currency="dashboard.baseCurrency" class="text-2xl text-mf-green-dark" />
      </div>
      <div class="rounded-2xl bg-mf-surface p-5">
        <p class="text-sm text-mf-muted">Expenses</p>
        <MoneyAmount
          :minor="Math.abs(dashboard.expenseMinor)"
          :currency="dashboard.baseCurrency"
          class="text-2xl text-mf-red-text"
        />
      </div>
      <div class="rounded-2xl bg-mf-surface p-5">
        <p class="text-sm text-mf-muted">Balance</p>
        <MoneyAmount
          :minor="dashboard.balanceMinor"
          :currency="dashboard.baseCurrency"
          class="text-2xl"
          :class="dashboard.balanceMinor < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
        />
      </div>
    </div>

    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <!-- Category breakdown -->
      <section class="rounded-2xl bg-mf-surface p-5">
        <div class="mb-4 flex items-center justify-between gap-3">
          <h2 class="font-medium">By category</h2>
          <div class="flex gap-0.5 rounded-full bg-mf-bg p-0.5" role="group" aria-label="Chart style">
            <button
              v-for="opt in CHART_STYLES"
              :key="opt.style"
              type="button"
              :aria-pressed="chartStyle === opt.style"
              :title="opt.label"
              class="flex items-center gap-1 rounded-full px-2.5 py-1 text-xs"
              :class="chartStyle === opt.style ? 'bg-mf-surface text-mf-ink shadow-sm' : 'text-mf-muted hover:text-mf-ink'"
              @click="setChartStyle(opt.style)"
            >
              <component :is="opt.icon" :size="14" />
              {{ opt.label }}
            </button>
          </div>
        </div>
        <p v-if="dashboard.byCategory.length === 0" class="text-sm text-mf-muted">
          Nothing recorded for this period.
        </p>
        <CategoryPie
          v-else-if="chartStyle === 'pie'"
          :totals="dashboard.byCategory"
          :expense-minor="dashboard.expenseMinor"
          :currency="dashboard.baseCurrency"
          :selected="selectedCategory"
          @select="selectCategory"
        />
        <ul v-else class="space-y-1">
          <li v-for="c in dashboard.byCategory" :key="String(c.category.id)">
            <button
              type="button"
              class="w-full rounded-lg px-2 py-1.5 text-left transition-colors"
              :class="selectedCategory === String(c.category.id) ? 'bg-mf-green-soft/40' : 'hover:bg-mf-bg'"
              @click="selectCategory(String(c.category.id))"
            >
              <div class="mb-1 flex items-center gap-2 text-sm">
                <CategoryIcon :icon="c.category.icon" :color="c.category.color" :size="18" />
                <span class="min-w-0 flex-1 truncate">{{ c.category.name }}</span>
                <MoneyAmount :minor="Math.abs(c.totalMinor)" :currency="dashboard.baseCurrency" />
              </div>
              <div class="h-1.5 overflow-hidden rounded-full bg-mf-muted/20">
                <div
                  class="h-full rounded-full"
                  :style="{
                    width: `${Math.round(c.share * 100)}%`,
                    backgroundColor: `var(--color-cat-${c.category.color ?? 'gray'})`,
                  }"
                />
              </div>
            </button>
          </li>
        </ul>
        <p v-if="dashboard.byCategory.length" class="mt-3 text-xs text-mf-muted">
          Click a category to list its expenses.
        </p>
      </section>

      <!--
        Accounts, and what they add up to now. The headline pair is the live
        version of the capital table's last column: net worth and the part of
        it that is ready to use, both in the base currency.
      -->
      <section class="flex flex-col rounded-2xl bg-mf-surface p-5">
        <div class="mb-4 flex items-center justify-between gap-3">
          <h2 class="font-medium">Accounts</h2>
          <RouterLink to="/capital" class="flex items-center gap-1 text-xs text-mf-green-dark hover:underline">
            Capital by month <ArrowRight :size="12" />
          </RouterLink>
        </div>

        <dl class="mb-4 grid grid-cols-2 gap-3">
          <div class="rounded-xl bg-mf-bg px-3 py-2">
            <dt class="text-xs text-mf-muted">Net worth</dt>
            <dd>
              <MoneyAmount
                :minor="accounts.liveTotals.totalMinor"
                :currency="dashboard.baseCurrency"
                class="text-lg font-semibold"
                :class="accounts.liveTotals.totalMinor < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
              />
            </dd>
          </div>
          <div class="rounded-xl bg-mf-bg px-3 py-2">
            <dt class="text-xs text-mf-muted">Ready to use</dt>
            <dd>
              <MoneyAmount
                :minor="accounts.liveTotals.readyMinor"
                :currency="dashboard.baseCurrency"
                class="text-lg font-semibold"
                :class="accounts.liveTotals.readyMinor < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
              />
            </dd>
          </div>
        </dl>

        <ul class="divide-y divide-mf-muted/20">
          <li
            v-for="a in taxonomy.activeAccounts"
            :key="String(a.id)"
            class="flex items-center gap-3 py-2 text-sm"
          >
            <CategoryIcon :icon="a.icon" :color="a.color" :size="20" />
            <span class="min-w-0 flex-1 truncate">{{ a.name }}</span>
            <MoneyAmount
              :minor="accounts.balanceOf(a.id)"
              :currency="String(a.currency)"
              :class="accounts.balanceOf(a.id) < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
            />
          </li>
        </ul>
        <RouterLink to="/accounts" class="mt-auto pt-3 text-xs text-mf-green-dark hover:underline">
          Manage accounts and balances
        </RouterLink>
      </section>
    </div>

    <!-- Records: everything in the period, filtered and paged on the table itself. -->
    <section ref="records" class="scroll-mt-4 rounded-2xl bg-mf-surface p-5">
      <div class="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
        <h2 class="flex items-center gap-2 font-medium">
          <CategoryIcon
            v-if="selectedCategoryRow"
            :icon="selectedCategoryRow.icon"
            :color="selectedCategoryRow.color"
            :size="20"
          />
          {{ selectedCategory ? selectedName : 'Records' }}
          <span class="text-sm font-normal text-mf-muted">{{ dashboard.label }}</span>
        </h2>
        <button
          v-if="filtersActive"
          type="button"
          class="flex items-center gap-1 rounded-full bg-mf-bg px-2.5 py-1 text-xs text-mf-muted hover:text-mf-ink"
          @click="clearFilters"
        >
          <X :size="12" /> Clear filters
        </button>
      </div>

      <!-- One row of filters, above the table they scope. -->
      <div class="mb-4 flex flex-wrap items-center gap-2 text-sm">
        <label
          class="flex min-w-48 flex-1 items-center gap-2 rounded-lg border border-mf-muted/50 bg-mf-surface px-3 py-1.5 focus-within:border-mf-green"
        >
          <Search :size="16" class="shrink-0 text-mf-muted" />
          <input
            v-model="query"
            type="search"
            placeholder="Search note, category, account or amount"
            class="w-full bg-transparent outline-none placeholder:text-mf-muted"
          />
        </label>

        <select
          v-model="selectedCategory"
          aria-label="Category"
          class="rounded-lg border border-mf-muted/50 bg-mf-surface px-2.5 py-1.5 outline-none focus:border-mf-green"
        >
          <option :value="null">All categories</option>
          <optgroup v-for="group in categoryOptions" :key="group.label" :label="group.label">
            <option v-for="c in group.items" :key="String(c.id)" :value="String(c.id)">{{ c.name }}</option>
          </optgroup>
        </select>

        <select
          v-model="accountFilter"
          aria-label="Account"
          class="rounded-lg border border-mf-muted/50 bg-mf-surface px-2.5 py-1.5 outline-none focus:border-mf-green"
        >
          <option :value="null">All accounts</option>
          <option v-for="a in taxonomy.accounts" :key="String(a.id)" :value="String(a.id)">
            {{ a.name }}{{ a.archived ? ' (archived)' : '' }}
          </option>
        </select>

        <div class="flex gap-0.5 rounded-full bg-mf-bg p-0.5" role="group" aria-label="Type">
          <button
            v-for="k in KIND_FILTERS"
            :key="k.kind"
            type="button"
            :aria-pressed="kindFilter === k.kind"
            class="rounded-full px-3 py-1 text-xs"
            :class="kindFilter === k.kind ? 'bg-mf-surface text-mf-ink shadow-sm' : 'text-mf-muted hover:text-mf-ink'"
            @click="kindFilter = k.kind"
          >
            {{ k.label }}
          </button>
        </div>
      </div>

      <dl v-if="selectedStats" class="mb-4 grid grid-cols-3 gap-3 text-sm">
        <div class="rounded-xl bg-mf-bg px-3 py-2">
          <dt class="text-xs text-mf-muted">Total · {{ Math.round(selectedStats.share * 100) }}% of spending</dt>
          <dd><MoneyAmount :minor="selectedStats.total" :currency="dashboard.baseCurrency" class="font-medium text-mf-red-text" /></dd>
        </div>
        <div class="rounded-xl bg-mf-bg px-3 py-2">
          <dt class="text-xs text-mf-muted">Largest</dt>
          <dd><MoneyAmount :minor="selectedStats.largest" :currency="dashboard.baseCurrency" class="font-medium" /></dd>
        </div>
        <div class="rounded-xl bg-mf-bg px-3 py-2">
          <dt class="text-xs text-mf-muted">Average</dt>
          <dd><MoneyAmount :minor="selectedStats.average" :currency="dashboard.baseCurrency" class="font-medium" /></dd>
        </div>
      </dl>

      <p v-if="filteredRows.length === 0" class="py-6 text-center text-sm text-mf-muted">
        {{ filtersActive ? 'No records match these filters in this period.' : 'Nothing recorded for this period.' }}
      </p>
      <template v-else>
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-mf-muted/20 text-left text-xs text-mf-muted">
              <th class="pb-2 font-normal">
                <button
                  type="button"
                  class="flex items-center gap-1 hover:text-mf-ink"
                  :class="sortKey === 'date' && 'text-mf-ink'"
                  @click="sortKey = 'date'"
                >
                  Date <ArrowDown v-if="sortKey === 'date'" :size="12" />
                </button>
              </th>
              <th class="pb-2 font-normal">Category</th>
              <th class="pb-2 font-normal">Account</th>
              <th class="pb-2 font-normal">Note</th>
              <th class="pb-2 font-normal">
                <button
                  type="button"
                  class="ml-auto flex items-center gap-1 hover:text-mf-ink"
                  :class="sortKey === 'amount' && 'text-mf-ink'"
                  @click="sortKey = 'amount'"
                >
                  <ArrowDown v-if="sortKey === 'amount'" :size="12" /> Amount
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in tableRows"
              :key="String(row.id)"
              class="cursor-pointer border-b border-mf-muted/10 last:border-0 hover:bg-mf-bg"
              @click="openRecord(row)"
            >
              <td class="py-2 whitespace-nowrap text-mf-muted">{{ shortDate(String(row.occurredOn)) }}</td>
              <td class="py-2">
                <span class="flex items-center gap-2">
                  <CategoryIcon
                    v-if="taxonomy.byId.get(String(row.categoryId))"
                    :icon="taxonomy.byId.get(String(row.categoryId))?.icon"
                    :color="taxonomy.byId.get(String(row.categoryId))?.color"
                    :size="16"
                  />
                  {{ categoryName(row) || 'Uncategorised' }}
                </span>
              </td>
              <td class="py-2 text-mf-muted">
                {{ accountName(row.accountId) }}
                <template v-if="row.kind === 'transfer'"> → {{ accountName(row.toAccountId) }}</template>
              </td>
              <td class="max-w-50 truncate py-2 text-mf-muted">{{ row.note }}</td>
              <td
                class="py-2 text-right font-medium whitespace-nowrap"
                :class="Number(row.amountMinor ?? 0) < 0 && row.kind !== 'transfer' ? 'text-mf-red-text' : row.kind === 'transfer' ? 'text-mf-ink' : 'text-mf-green-dark'"
              >
                <MoneyAmount :minor="Number(row.amountMinor ?? 0)" :currency="String(row.currency)" />
              </td>
            </tr>
          </tbody>
        </table>

        <!-- Paging: where you are, how many per page, and a way to every page in two clicks. -->
        <div class="mt-4 flex flex-wrap items-center gap-3 text-sm text-mf-muted">
          <span class="tabular-nums">{{ rangeLabel }}</span>
          <label class="flex items-center gap-1.5">
            <span>Per page</span>
            <select
              v-model.number="pageSize"
              class="rounded-lg border border-mf-muted/50 bg-mf-surface px-2 py-1 text-mf-ink outline-none focus:border-mf-green"
            >
              <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }}</option>
            </select>
          </label>

          <nav v-if="pageCount > 1" class="ml-auto flex items-center gap-1" aria-label="Pages">
            <button
              type="button"
              class="grid size-8 place-items-center rounded-full hover:bg-mf-bg disabled:opacity-30"
              aria-label="Previous page"
              :disabled="page === 1"
              @click="page--"
            >
              <ChevronLeft :size="16" />
            </button>
            <template v-for="(p, i) in pageButtons" :key="`${p}-${i}`">
              <span v-if="p === '…'" class="px-1">…</span>
              <button
                v-else
                type="button"
                class="min-w-8 rounded-full px-2 py-1 tabular-nums"
                :class="p === page ? 'bg-mf-green text-white' : 'text-mf-ink hover:bg-mf-bg'"
                :aria-current="p === page ? 'page' : undefined"
                @click="page = p"
              >
                {{ p }}
              </button>
            </template>
            <button
              type="button"
              class="grid size-8 place-items-center rounded-full hover:bg-mf-bg disabled:opacity-30"
              aria-label="Next page"
              :disabled="page === pageCount"
              @click="page++"
            >
              <ChevronRight :size="16" />
            </button>
          </nav>
        </div>
      </template>
    </section>
  </div>
</template>
