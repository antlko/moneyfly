<script setup lang="ts">
import { ClipboardCheck } from '@lucide/vue'
import { computed, ref } from 'vue'

import CapitalTable from '@/components/capital/CapitalTable.vue'
import CheckInSheet from '@/components/capital/CheckInSheet.vue'
import ShareBar from '@/components/charts/ShareBar.vue'
import TimeChart, { type ChartSeries } from '@/components/charts/TimeChart.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { compactMoney, percent, wholeMoney } from '@/lib/chart'
import { formatMoney } from '@/lib/money'
import { currentMonth, monthLabel, monthLabelWithYear, type MonthKey } from '@/lib/period'
import { AVERAGE_WINDOW, useCapitalStore } from '@/stores/capital'
import { useDashboardStore } from '@/stores/dashboard'

/**
 * Capital: what you have, month by month, and what that says about how you
 * live — the "Capital Grow" spreadsheet, rebuilt on top of the ledger.
 *
 * The month figures are check-ins (CheckInSheet), typed or confirmed once a
 * month; income and spending come from the records. Laid side by side they
 * answer the questions the spreadsheet existed for: how much of the salary was
 * kept, how long what is ready to use would last, whether spending is
 * creeping up (personal inflation), and how much money moved that nobody wrote
 * down.
 */
const capital = useCapitalStore()
const dashboard = useDashboardStore()

const RANGES = [
  { months: 6, label: '6M' },
  { months: 12, label: '1Y' },
  { months: 24, label: '2Y' },
  { months: 0, label: 'All' },
] as const
const range = ref<number>(12)

/** The months in view — the same slice for every chart and the table, so they always agree. */
const columns = computed(() =>
  range.value === 0 ? capital.table : capital.table.slice(-range.value),
)
const months = computed(() => columns.value.map((c) => c.month))

const checkingIn = ref<MonthKey | null>(null)

const base = computed(() => dashboard.baseCurrency)
const money = (v: number) => formatMoney(Math.round(v), base.value)
const tick = (v: number) => compactMoney(Math.round(v), base.value)
const whole = (v: number) => wholeMoney(Math.round(v), base.value)
const share = (v: number) => percent(v)
const shareTick = (v: number) => percent(v, false, 0)
const runway = (v: number) => `${v.toFixed(1)} months`
const runwayTick = (v: number) => `${Math.round(v)}`

const latest = computed(() => capital.latest)
const checked = computed(() => capital.latestChecked)
const thisMonth = computed(() => capital.table.at(-1) ?? null)
const needsCheckIn = computed(() => capital.loaded && !thisMonth.value?.checkedIn)
const hasAnything = computed(() => capital.snapshots.length > 0)

/** The last complete month, for "saved last month". */
const lastComplete = computed(() => [...capital.table].reverse().find((c) => c.month !== currentMonth()) ?? null)

/* --- charts --------------------------------------------------------------- */

const worthSeries = computed<ChartSeries[]>(() => [
  {
    key: 'total',
    label: 'Net worth',
    type: 'area',
    color: 'var(--color-chart-1)',
    values: columns.value.map((c) => c.totalMinor),
  },
  {
    key: 'ready',
    label: 'Ready to use',
    type: 'line',
    color: 'var(--color-chart-7)',
    values: columns.value.map((c) => c.readyMinor),
  },
])

const flowSeries = computed<ChartSeries[]>(() => [
  {
    key: 'income',
    label: 'Income',
    type: 'column',
    color: 'var(--color-chart-3)',
    values: columns.value.map((c) => c.incomeMinor || null),
  },
  {
    key: 'spent',
    label: 'Spending',
    type: 'column',
    color: 'var(--color-chart-2)',
    values: columns.value.map((c) => c.spentMinor || null),
  },
])

/**
 * Saved share, the sign doing the work: kept money in the income colour,
 * overspending in the spending colour — the same two meanings as the chart
 * beside it, so the page has one vocabulary.
 */
const savedSeries = computed<ChartSeries[]>(() => [
  {
    key: 'saved',
    label: 'Saved',
    type: 'column',
    color: 'var(--color-chart-3)',
    colorOf: (v) => (v < 0 ? 'var(--color-chart-2)' : 'var(--color-chart-3)'),
    values: columns.value.map((c) => c.savedShare),
  },
])

const trendSeries = computed<ChartSeries[]>(() => [
  {
    key: 'spent',
    label: 'Spending',
    type: 'column',
    color: 'var(--color-chart-2)',
    values: columns.value.map((c) => (c.month === currentMonth() ? null : c.spentMinor || null)),
  },
  {
    key: 'avg',
    label: `${AVERAGE_WINDOW}-month average`,
    type: 'line',
    color: 'var(--color-chart-1)',
    values: columns.value.map((c) => (c.month === currentMonth() ? null : c.avgSpentMinor)),
  },
])

const runwaySeries = computed<ChartSeries[]>(() => [
  {
    key: 'runway',
    label: 'Months of runway',
    type: 'area',
    color: 'var(--color-chart-1)',
    values: columns.value.map((c) => c.runwayMonths),
  },
])

/** Where the money is, by group, in the latest month — slots in fixed group order. */
const SLOTS = [1, 2, 3, 4, 5, 6, 7, 8]
const composition = computed(() => {
  const at = checked.value ?? latest.value
  if (!at) return []
  return capital.groups
    .map((g, i) => ({
      key: g.name,
      label: g.name,
      value: at.byGroup.get(g.name) ?? 0,
      color: `var(--color-chart-${SLOTS[i % SLOTS.length]})`,
    }))
    .filter((item) => item.value !== 0)
})

const inflationLabel = computed(() => {
  const inf = capital.inflation
  if (!inf) return null
  return inf.months === 12
    ? 'Last 12 months against the 12 before'
    : `Last ${inf.months} months against the ${inf.months} before — a trend, not yet a year`
})
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Capital">
      <template #actions>
        <button
          type="button"
          class="flex items-center gap-2 rounded-full px-3 py-2 text-sm sm:bg-mf-green sm:text-white"
          @click="checkingIn = currentMonth()"
        >
          <ClipboardCheck :size="18" :stroke-width="2" />
          <span class="hidden sm:inline">Check in {{ monthLabel(currentMonth()) }}</span>
        </button>
      </template>
    </ScreenHeader>

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:p-0">
      <!-- First visit: what this is, and the one thing to do. -->
      <section v-if="capital.loaded && !hasAnything" class="rounded-2xl bg-mf-surface p-5">
        <h2 class="text-lg font-medium">Track what you have, month by month</h2>
        <p class="mt-2 max-w-2xl text-sm text-mf-muted">
          Once a month, write down what each account holds — cash, banks, deposits, gold. Laid
          against your records, that shows how much of your income you kept, how long your savings
          would last, whether your spending is creeping up, and how much went out that nobody wrote
          down. Group accounts and mark what is ready to use on the
          <RouterLink to="/accounts" class="text-mf-green-dark underline">Accounts</RouterLink> screen.
        </p>
        <button
          type="button"
          class="mt-4 rounded-full bg-mf-green px-4 py-2 text-sm font-medium text-white"
          @click="checkingIn = currentMonth()"
        >
          Check in {{ monthLabel(currentMonth()) }}
        </button>
      </section>

      <p
        v-else-if="needsCheckIn"
        class="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-2xl bg-mf-green-soft/30 px-4 py-3 text-sm"
      >
        {{ monthLabel(currentMonth()) }} isn't checked in yet — the figures below carry last month's.
        <button type="button" class="font-medium text-mf-green-dark underline" @click="checkingIn = currentMonth()">
          Check in now
        </button>
      </p>

      <!-- KPI row: the five numbers the spreadsheet existed to produce. -->
      <section v-if="hasAnything" class="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <div class="rounded-2xl bg-mf-surface p-4">
          <p class="text-xs text-mf-muted">Net worth</p>
          <MoneyAmount
            v-if="latest?.totalMinor != null"
            :minor="latest.totalMinor"
            :currency="base"
            class="text-xl font-semibold"
          />
          <p
            v-if="checked?.changeMinor != null"
            class="mt-1 text-xs"
            :class="checked.changeMinor < 0 ? 'text-mf-red-text' : 'text-mf-green-dark'"
          >
            {{ checked.changeMinor > 0 ? '+' : '' }}{{ whole(checked.changeMinor) }} in {{ monthLabel(checked.month) }}
          </p>
        </div>
        <div class="rounded-2xl bg-mf-surface p-4">
          <p class="text-xs text-mf-muted">Ready to use</p>
          <MoneyAmount
            v-if="latest?.readyMinor != null"
            :minor="latest.readyMinor"
            :currency="base"
            class="text-xl font-semibold"
          />
          <p v-if="latest?.readyMinor != null && latest.totalMinor" class="mt-1 text-xs text-mf-muted">
            {{ percent(latest.readyMinor / latest.totalMinor, false, 0) }} of everything
          </p>
        </div>
        <div class="rounded-2xl bg-mf-surface p-4">
          <p class="text-xs text-mf-muted">Saved {{ lastComplete ? `in ${monthLabel(lastComplete.month)}` : '' }}</p>
          <p class="text-xl font-semibold">
            {{ lastComplete?.savedShare != null ? percent(lastComplete.savedShare) : '—' }}
          </p>
          <p class="mt-1 text-xs text-mf-muted">of recorded income</p>
        </div>
        <div class="rounded-2xl bg-mf-surface p-4">
          <p class="text-xs text-mf-muted">Runway</p>
          <p class="text-xl font-semibold">
            {{ latest?.runwayMonths != null ? `${latest.runwayMonths.toFixed(1)} months` : '—' }}
          </p>
          <p v-if="latest?.avgSpentMinor" class="mt-1 text-xs text-mf-muted">
            at {{ whole(latest.avgSpentMinor) }} a month
          </p>
        </div>
        <div class="col-span-2 rounded-2xl bg-mf-surface p-4 lg:col-span-1">
          <p class="text-xs text-mf-muted" :title="inflationLabel ?? undefined">
            {{ capital.inflation?.months === 12 ? 'Personal inflation' : 'Spending trend' }}
          </p>
          <p class="text-xl font-semibold">
            {{ capital.inflation ? percent(capital.inflation.change, true) : '—' }}
          </p>
          <p class="mt-1 text-xs text-mf-muted">
            <template v-if="capital.inflation">
              {{ whole(capital.inflation.recentAvg) }} a month over the last {{ capital.inflation.months }},
              {{ whole(capital.inflation.previousAvg) }} before
            </template>
            <template v-else>Needs six months of records.</template>
          </p>
        </div>
      </section>

      <!-- Range: one row of filters, above everything it scopes. -->
      <div v-if="hasAnything || capital.table.length > 1" class="flex items-center gap-2">
        <div class="flex gap-0.5 rounded-full bg-mf-surface p-0.5" role="group" aria-label="Range">
          <button
            v-for="r in RANGES"
            :key="r.label"
            type="button"
            :aria-pressed="range === r.months"
            class="rounded-full px-3 py-1 text-xs"
            :class="range === r.months ? 'bg-mf-green text-white' : 'text-mf-muted hover:text-mf-ink'"
            @click="range = r.months"
          >
            {{ r.label }}
          </button>
        </div>
      </div>

      <div class="grid gap-4 lg:grid-cols-2">
        <section v-if="hasAnything" class="rounded-2xl bg-mf-surface p-4 sm:p-5">
          <h2 class="mb-3 font-medium">Net worth</h2>
          <TimeChart
            :months="months"
            :series="worthSeries"
            :format="money"
            :format-tick="tick"
            end-label
            label="Net worth and the part ready to use, by month"
          />
        </section>

        <section v-if="hasAnything" class="rounded-2xl bg-mf-surface p-4 sm:p-5">
          <h2 class="mb-1 font-medium">Where it is</h2>
          <p class="mb-4 text-xs text-mf-muted">
            {{ (checked ?? latest) ? monthLabelWithYear((checked ?? latest)!.month) : '' }}, by group
          </p>
          <ShareBar :items="composition" :format="money" />
        </section>

        <section class="rounded-2xl bg-mf-surface p-4 sm:p-5">
          <h2 class="mb-3 font-medium">Income and spending</h2>
          <TimeChart
            :months="months"
            :series="flowSeries"
            :format="money"
            :format-tick="tick"
            label="Recorded income and spending, by month"
          />
        </section>

        <section class="rounded-2xl bg-mf-surface p-4 sm:p-5">
          <h2 class="mb-1 font-medium">Saved</h2>
          <p class="mb-3 text-xs text-mf-muted">Share of recorded income not spent</p>
          <TimeChart
            :months="months"
            :series="savedSeries"
            :format="share"
            :format-tick="shareTick"
            label="Share of income saved, by month"
          />
        </section>

        <section class="rounded-2xl bg-mf-surface p-4 sm:p-5">
          <h2 class="mb-1 font-medium">Spending trend</h2>
          <p class="mb-3 text-xs text-mf-muted">Complete months, with the {{ AVERAGE_WINDOW }}-month average</p>
          <TimeChart
            :months="months"
            :series="trendSeries"
            :format="money"
            :format-tick="tick"
            label="Monthly spending and its moving average"
          />
        </section>

        <section v-if="hasAnything" class="rounded-2xl bg-mf-surface p-4 sm:p-5">
          <h2 class="mb-1 font-medium">Runway</h2>
          <p class="mb-3 text-xs text-mf-muted">Months that what is ready to use would last</p>
          <TimeChart
            :months="months"
            :series="runwaySeries"
            :format="runway"
            :format-tick="runwayTick"
            end-label
            label="Months of runway, by month"
          />
        </section>
      </div>

      <section class="rounded-2xl bg-mf-surface p-2 sm:p-3">
        <div class="flex items-baseline justify-between px-2 pt-1 pb-2">
          <h2 class="font-medium">By month</h2>
          <p class="text-xs text-mf-muted">Click a month to check it in</p>
        </div>
        <CapitalTable :columns="columns" :groups="capital.groups" :currency="base" @pick="checkingIn = $event" />
      </section>
    </main>

    <Transition name="mf-sheet">
      <CheckInSheet v-if="checkingIn" :key="checkingIn" :month="checkingIn" @close="checkingIn = null" />
    </Transition>
  </div>
</template>
