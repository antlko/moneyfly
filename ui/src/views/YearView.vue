<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import type { BudgetState, CategoryCell, Money, PeriodSummary } from '@/api/types'
import { formatMoney, formatPercent } from '@/lib/money'
import { shiftPeriod } from '@/lib/period'
import SeriesChart from '@/components/SeriesChart.vue'
import { useReportsStore } from '@/stores/reports'

/**
 * The workbook's grid, in the browser: 18 categories by 12 months, with AVG,
 * Results and the roll-up rows underneath (docs/implementation-plan/05 §10).
 *
 * Two rules the sheet could not keep:
 *   - an unrecorded month is blank, never `0` and never `-1`;
 *   - the table scrolls inside its own container, so the page never scrolls
 *     sideways.
 */
const reports = useReportsStore()

const from = ref<string | null>(null)
const to = ref<string | null>(null)

const grid = computed(() => reports.grid)
const periods = computed(() => grid.value?.periods ?? [])

/** Aug-2025 style labels; the sheet plotted against a bare index. */
function monthLabel(period: string): string {
  const [year, month] = period.split('-')
  const date = new Date(Number(year), Number(month) - 1, 1)
  return date.toLocaleDateString(undefined, { month: 'short' })
}

function yearLabel(period: string): string {
  return period.split('-')[0]
}

const tones: Record<BudgetState, string> = {
  severely_over: 'bg-state-severe/20 text-state-severe',
  over: 'bg-state-over/20 text-state-over',
  approaching: 'bg-state-approach/20 text-state-approach',
  within: 'bg-state-within/15 text-state-within',
  zero: 'bg-state-zero/10 text-state-zero',
  not_recorded: '',
}

/** Colour is never the only signal: the title carries the state in words. */
function cellTitle(cell: CategoryCell): string {
  if (!cell.actual) return `${cell.period}: nothing recorded`
  const planned = cell.planned ? ` of ${formatMoney(cell.planned)} planned` : ' (no plan)'
  return `${cell.period}: ${formatMoney(cell.actual)}${planned} — ${cell.state_label}`
}

/**
 * A plan is a decision. A month nobody planned for has no figure at all, so the
 * roll-up rows stay blank rather than claiming a plan of zero — the same rule
 * the cells follow for unrecorded spending.
 */
function planned(m: Money, entry: PeriodSummary): string {
  if (entry.planned_total.amount_minor === 0) return ''
  return compact(m)
}

function compact(m: Money | null): string {
  if (!m) return ''
  const value = m.amount_minor / 10 ** m.exponent
  if (Math.abs(value) >= 1000) return `${Math.round(value / 100) / 10}k`
  return Math.round(value).toString()
}

/** Spend per month, in major units, with unrecorded months left out entirely. */
const spendSeries = computed(() =>
  (grid.value?.summary ?? []).map((entry) => ({
    period: entry.period,
    value: entry.spend_total
      ? entry.spend_total.amount_minor / 10 ** entry.spend_total.exponent
      : null,
  })),
)

/** Saved %, unclamped: the axis has to accommodate -3.75. */
const savedSeries = computed(() =>
  (grid.value?.summary ?? []).map((entry) => ({
    period: entry.period,
    value: entry.saved_percent,
  })),
)

const hasSpend = computed(() => spendSeries.value.some((p) => p.value !== null))
const hasSaved = computed(() => savedSeries.value.some((p) => p.value !== null))

function shiftYear(years: number) {
  const current = grid.value
  if (!current) return
  from.value = shiftPeriod(current.from, years * 12)
  to.value = shiftPeriod(current.to, years * 12)
}

async function load() {
  await reports.load(from.value ?? undefined, to.value ?? undefined)
  if (!from.value && reports.grid) {
    from.value = reports.grid.from
    to.value = reports.grid.to
  }
}

watch([from, to], load)
onMounted(load)
</script>

<template>
  <section>
    <header class="mb-4 flex items-center justify-between">
      <button
        type="button"
        class="tap-target rounded-lg px-3 text-xl text-slate-500"
        aria-label="Previous year"
        @click="shiftYear(-1)"
      >
        ‹
      </button>
      <div class="text-center">
        <h1 class="text-lg font-semibold">Year</h1>
        <p v-if="grid" class="text-xs text-slate-500">{{ grid.from }} → {{ grid.to }}</p>
      </div>
      <button
        type="button"
        class="tap-target rounded-lg px-3 text-xl text-slate-500"
        aria-label="Next year"
        @click="shiftYear(1)"
      >
        ›
      </button>
    </header>

    <p v-if="reports.error" class="mb-3 text-sm text-state-severe" role="alert">
      {{ reports.error }}
    </p>

    <p
      v-if="reports.blankMonths.length"
      class="mb-3 rounded-xl border border-dashed border-slate-300 p-3 text-xs text-slate-500 dark:border-slate-700"
    >
      {{ reports.blankMonths.length }} month{{ reports.blankMonths.length === 1 ? '' : 's' }} in
      this range recorded nothing. Those cells are blank, not zero.
    </p>

    <div v-if="grid && (hasSpend || hasSaved)" class="mb-4 grid gap-4 sm:grid-cols-2">
      <div
        v-if="hasSpend"
        class="rounded-2xl border border-slate-200 bg-white p-3 text-brand-600 dark:border-slate-800 dark:bg-slate-900 dark:text-brand-300"
      >
        <p class="mb-1 text-xs uppercase tracking-wide text-slate-500">Spend</p>
        <SeriesChart
          :points="spendSeries"
          label="Monthly spend"
          :format="(v: number) => v.toFixed(0)"
        />
      </div>
      <div
        v-if="hasSaved"
        class="rounded-2xl border border-slate-200 bg-white p-3 text-state-within dark:border-slate-800 dark:bg-slate-900"
      >
        <p class="mb-1 text-xs uppercase tracking-wide text-slate-500">Saved %</p>
        <SeriesChart
          :points="savedSeries"
          label="Savings rate"
          :format="(v: number) => `${(v * 100).toFixed(0)}%`"
        />
      </div>
    </div>

    <!-- The table scrolls inside its own container; the page never does. -->
    <div
      v-if="grid"
      class="-mx-4 overflow-x-auto px-4 pb-2"
      tabindex="0"
      role="region"
      aria-label="Spending by category and month"
    >
      <table class="w-max min-w-full border-separate border-spacing-0 text-xs">
        <thead>
          <tr>
            <th
              scope="col"
              class="sticky left-0 z-10 bg-slate-50 px-2 py-2 text-left font-semibold dark:bg-slate-950"
            >
              Category
            </th>
            <th scope="col" class="px-2 py-2 text-right font-semibold">AVG</th>
            <th
              v-for="period in periods"
              :key="period"
              scope="col"
              class="px-2 py-2 text-right font-semibold"
              :class="grid.recorded[period] ? '' : 'text-slate-400'"
            >
              <span class="block">{{ monthLabel(period) }}</span>
              <span class="block text-[10px] font-normal text-slate-400">
                {{ yearLabel(period) }}
              </span>
            </th>
            <th scope="col" class="px-2 py-2 text-right font-semibold">Results</th>
          </tr>
        </thead>

        <tbody>
          <tr
            v-for="row in grid.categories"
            :key="row.category_id"
            class="border-t border-slate-200 dark:border-slate-800"
          >
            <th
              scope="row"
              class="sticky left-0 z-10 whitespace-nowrap bg-slate-50 px-2 py-1.5 text-left font-medium dark:bg-slate-950"
            >
              <span aria-hidden="true">{{ row.icon }}</span>
              {{ row.name }}
              <span v-if="row.is_essential" class="text-[10px] text-slate-400">essential</span>
            </th>
            <td class="money px-2 py-1.5 text-slate-500">{{ compact(row.average) }}</td>
            <td
              v-for="cell in row.cells"
              :key="cell.period"
              class="money px-2 py-1.5"
              :class="tones[cell.state]"
              :title="cellTitle(cell)"
            >
              <!-- Blank, never 0: this is the sentinel bug the sheet had. -->
              {{ compact(cell.actual) }}
            </td>
            <td class="money px-2 py-1.5 font-semibold">{{ compact(row.total) }}</td>
          </tr>
        </tbody>

        <tfoot class="border-t-2 border-slate-300 dark:border-slate-700">
          <tr>
            <th
              scope="row"
              class="sticky left-0 z-10 bg-slate-50 px-2 py-1.5 text-left font-semibold dark:bg-slate-950"
            >
              Amount
            </th>
            <td class="money px-2 py-1.5"></td>
            <td
              v-for="entry in grid.summary"
              :key="'spend-' + entry.period"
              class="money px-2 py-1.5 font-semibold"
            >
              {{ compact(entry.spend_total) }}
            </td>
            <td class="money px-2 py-1.5"></td>
          </tr>
          <tr>
            <th
              scope="row"
              class="sticky left-0 z-10 bg-slate-50 px-2 py-1.5 text-left dark:bg-slate-950"
            >
              Possible minimum
            </th>
            <td class="money px-2 py-1.5"></td>
            <td
              v-for="entry in grid.summary"
              :key="'min-' + entry.period"
              class="money px-2 py-1.5 text-slate-500"
            >
              {{ planned(entry.possible_minimum, entry) }}
            </td>
            <td class="money px-2 py-1.5"></td>
          </tr>
          <tr>
            <th
              scope="row"
              class="sticky left-0 z-10 bg-slate-50 px-2 py-1.5 text-left dark:bg-slate-950"
            >
              Diff
            </th>
            <td class="money px-2 py-1.5"></td>
            <td
              v-for="entry in grid.summary"
              :key="'diff-' + entry.period"
              class="money px-2 py-1.5"
              :class="
                entry.diff && entry.diff.amount_minor < 0
                  ? 'text-state-severe'
                  : 'text-state-within'
              "
            >
              {{ compact(entry.diff) }}
            </td>
            <td class="money px-2 py-1.5"></td>
          </tr>
          <tr>
            <th
              scope="row"
              class="sticky left-0 z-10 bg-slate-50 px-2 py-1.5 text-left dark:bg-slate-950"
            >
              Saved %
            </th>
            <td class="money px-2 py-1.5"></td>
            <td
              v-for="entry in grid.summary"
              :key="'saved-' + entry.period"
              class="money px-2 py-1.5"
              :class="
                entry.saved_percent !== null && entry.saved_percent < 0
                  ? 'text-state-severe'
                  : 'text-slate-500'
              "
            >
              <!-- Unclamped: -375% is the truth about that month. -->
              {{ entry.saved_percent === null ? '' : formatPercent(entry.saved_percent) }}
            </td>
            <td class="money px-2 py-1.5"></td>
          </tr>
        </tfoot>
      </table>
    </div>

    <p v-else-if="!reports.loading" class="text-sm text-slate-500">
      Nothing recorded yet. Import an export or log a transaction, and the grid fills in.
    </p>
  </section>
</template>
