<script setup lang="ts">
import { ArrowDown, ChartColumnBig, ChartPie, ChevronLeft, ChevronRight, X } from '@lucide/vue'
import type { Component } from 'vue'
import { computed, ref, useTemplateRef } from 'vue'
import { useRouter } from 'vue-router'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
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

// Most recent first, and capped: this is a glance at recent activity, not a
// register — Search already exists for finding a specific old record, and a
// table of everything in "All time" would make the page itself the thing
// that needs scrolling past.
const RECENT_LIMIT = 50

/**
 * The category picked in the chart, whose expenses the table then lists in
 * full — the "where did it all go" question the chart raises and cannot answer.
 * Kept across period changes, so paging through months follows one category.
 */
const selectedCategory = ref<string | null>(null)
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

const selectedTotal = computed(
  () => dashboard.byCategory.find((c) => String(c.category.id) === selectedCategory.value) ?? null,
)
const selectedName = computed(
  () => selectedTotal.value?.category.name ?? taxonomy.byId.get(selectedCategory.value ?? '')?.name ?? '',
)

/**
 * A row's size for sorting, in the base currency so a forint and a euro record
 * compare sensibly. An unconvertible row falls back to its own figure rather
 * than vanishing from the list.
 */
const size = (row: Row) => Math.abs(dashboard.inBase(row) ?? Number(row.amountMinor ?? 0))

const tableRows = computed(() => {
  const rows = selectedCategory.value
    ? dashboard.rows.filter(
        (r) => r.kind === 'expense' && String(r.categoryId ?? '') === selectedCategory.value,
      )
    : [...dashboard.rows]
  rows.sort((a, b) =>
    sortKey.value === 'amount'
      ? size(b) - size(a)
      : String(b.occurredOn).localeCompare(String(a.occurredOn)),
  )
  // A category's list is shown whole; the unfiltered one is a glance, capped.
  return selectedCategory.value ? rows : rows.slice(0, RECENT_LIMIT)
})

/** Largest and average for the selected category, in the base currency. */
const selectedStats = computed(() => {
  const t = selectedTotal.value
  if (!t || t.count === 0) return null
  const sizes = tableRows.value.map((r) => dashboard.inBase(r)).filter((m): m is number => m !== null)
  return {
    largest: sizes.length ? Math.max(...sizes.map(Math.abs)) : 0,
    average: Math.round(Math.abs(t.totalMinor) / t.count),
  }
})

const accountName = (id: unknown) => taxonomy.accounts.find((a) => a.id === id)?.name ?? ''

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

      <!-- Accounts -->
      <section class="rounded-2xl bg-mf-surface p-5">
        <h2 class="mb-4 font-medium">Accounts</h2>
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
      </section>
    </div>

    <!-- Records: recent ones, or every expense in the selected category -->
    <section ref="records" class="scroll-mt-4 rounded-2xl bg-mf-surface p-5">
      <div class="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
        <h2 v-if="!selectedCategory" class="font-medium">
          Recent records
          <span class="text-sm font-normal text-mf-muted">({{ tableRows.length }})</span>
        </h2>
        <template v-else>
          <h2 class="flex items-center gap-2 font-medium">
            <CategoryIcon
              v-if="selectedTotal"
              :icon="selectedTotal.category.icon"
              :color="selectedTotal.category.color"
              :size="20"
            />
            {{ selectedName }}
            <span class="text-sm font-normal text-mf-muted">
              {{ tableRows.length }} expense{{ tableRows.length === 1 ? '' : 's' }}
            </span>
          </h2>
          <button
            type="button"
            class="flex items-center gap-1 rounded-full bg-mf-bg px-2.5 py-1 text-xs text-mf-muted hover:text-mf-ink"
            @click="clearCategory"
          >
            <X :size="12" /> Show all records
          </button>
        </template>
      </div>

      <dl v-if="selectedTotal && selectedStats" class="mb-4 grid grid-cols-3 gap-3 text-sm">
        <div class="rounded-xl bg-mf-bg px-3 py-2">
          <dt class="text-xs text-mf-muted">Total · {{ Math.round(selectedTotal.share * 100) }}% of spending</dt>
          <dd><MoneyAmount :minor="Math.abs(selectedTotal.totalMinor)" :currency="dashboard.baseCurrency" class="font-medium text-mf-red-text" /></dd>
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

      <p v-if="tableRows.length === 0" class="text-sm text-mf-muted">
        {{ selectedCategory ? `No ${selectedName} expenses in this period.` : 'Nothing recorded for this period.' }}
      </p>
      <table v-else class="w-full text-sm">
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
                {{ row.kind === 'transfer' ? 'Transfer' : (taxonomy.byId.get(String(row.categoryId))?.name ?? 'Uncategorised') }}
              </span>
            </td>
            <td class="py-2 text-mf-muted">{{ accountName(row.accountId) }}</td>
            <td class="max-w-50 truncate py-2 text-mf-muted">{{ row.note }}</td>
            <td
              class="py-2 text-right font-medium whitespace-nowrap"
              :class="Number(row.amountMinor ?? 0) < 0 ? 'text-mf-red-text' : 'text-mf-green-dark'"
            >
              <MoneyAmount :minor="Number(row.amountMinor ?? 0)" :currency="String(row.currency)" />
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>
