<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import StateBadge from '@/components/StateBadge.vue'
import { formatMoney, formatPercent, toDisplayNumber } from '@/lib/money'
import { currentPeriod, formatPeriod, shiftPeriod } from '@/lib/period'
import { useBudgetStore } from '@/stores/budget'

const budget = useBudgetStore()
const period = ref(currentPeriod())
const touchStartX = ref<number | null>(null)

const report = computed(() => budget.report)

/** The hero progress ring: month-to-date spend against the planned total. */
const heroRatio = computed(() => {
  const r = report.value
  if (!r || !r.spend_total || r.planned_total.amount_minor === 0) return null
  return r.spend_total.amount_minor / r.planned_total.amount_minor
})

const recorded = computed(() => !!report.value?.spend_total)

async function load() {
  await budget.loadReport(period.value)
}

function move(months: number) {
  period.value = shiftPeriod(period.value, months)
}

// Swipe left and right changes month, per docs/08-ux.md §8.4.
function onTouchStart(event: TouchEvent) {
  touchStartX.value = event.changedTouches[0]?.clientX ?? null
}

function onTouchEnd(event: TouchEvent) {
  const start = touchStartX.value
  const end = event.changedTouches[0]?.clientX
  touchStartX.value = null
  if (start === null || end === undefined) return
  const delta = end - start
  if (Math.abs(delta) < 60) return
  move(delta > 0 ? -1 : 1)
}

watch(period, load)
onMounted(load)
</script>

<template>
  <section @touchstart.passive="onTouchStart" @touchend.passive="onTouchEnd">
    <header class="mb-4 flex items-center justify-between">
      <button
        type="button"
        class="tap-target rounded-lg px-3 text-xl text-slate-500"
        aria-label="Previous month"
        @click="move(-1)"
      >
        ‹
      </button>
      <div class="text-center">
        <h1 class="text-lg font-semibold">{{ formatPeriod(period) }}</h1>
        <RouterLink to="/year" class="text-xs text-brand-600 underline dark:text-brand-300">
          year grid
        </RouterLink>
      </div>
      <button
        type="button"
        class="tap-target rounded-lg px-3 text-xl text-slate-500"
        aria-label="Next month"
        @click="move(1)"
      >
        ›
      </button>
    </header>

    <!-- Hero: month-to-date spend with the plan beneath it. -->
    <div
      class="mb-4 rounded-2xl bg-gradient-to-br from-brand-700 to-brand-900 p-5 text-white shadow-lg"
    >
      <p class="text-xs uppercase tracking-wide text-brand-200">Spent this month</p>
      <p class="money mt-1 text-left text-4xl font-bold tabular-nums">
        {{ report?.spend_total ? formatMoney(report.spend_total) : '—' }}
      </p>
      <p class="mt-1 text-sm text-brand-200">
        of {{ formatMoney(report?.planned_total) }} planned
        <span v-if="heroRatio !== null"> · {{ formatPercent(heroRatio) }}</span>
      </p>

      <div v-if="heroRatio !== null" class="mt-4 h-2 overflow-hidden rounded-full bg-white/20">
        <div
          class="h-full rounded-full bg-white transition-[width]"
          :style="{ width: Math.min(100, heroRatio * 100) + '%' }"
        />
      </div>

      <dl class="mt-4 grid grid-cols-2 gap-3 text-sm">
        <div>
          <dt class="text-brand-200">Diff</dt>
          <dd class="money text-left font-semibold">
            {{ report?.diff ? formatMoney(report.diff) : '—' }}
          </dd>
        </div>
        <div>
          <dt class="text-brand-200">Saved</dt>
          <dd class="money text-left font-semibold">
            {{ formatPercent(report?.saved_percent ?? null, 1) }}
          </dd>
        </div>
      </dl>
    </div>

    <p
      v-if="!recorded && !budget.loading"
      class="mb-4 rounded-xl border border-dashed border-slate-300 p-4 text-sm text-slate-500 dark:border-slate-700"
    >
      Nothing recorded for {{ formatPeriod(period) }}. This month is blank, not zero.
      <RouterLink to="/entry" class="font-semibold text-brand-600 underline dark:text-brand-300">
        Log something
      </RouterLink>
    </p>

    <p
      v-if="report && report.unconverted > 0"
      class="mb-4 rounded-xl bg-state-approach/10 px-3 py-2 text-sm text-state-approach"
    >
      {{ report.unconverted }} transaction(s) have no exchange rate for their date and are excluded
      from these totals.
    </p>

    <ul class="space-y-2">
      <li
        v-for="row in report?.categories ?? []"
        :key="row.category_id"
        class="rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
      >
        <RouterLink
          :to="{ path: '/history', query: { category: row.category_id, period } }"
          class="block"
        >
          <div class="flex items-center gap-3">
            <span
              class="flex h-9 w-9 flex-none items-center justify-center rounded-full text-lg"
              :style="{ backgroundColor: (row.color ?? '#64748b') + '22' }"
              aria-hidden="true"
              >{{ row.icon ?? '•' }}</span
            >
            <span class="flex-1 truncate font-medium">
              {{ row.name }}
              <span v-if="row.is_essential" class="ml-1 text-xs text-slate-400">essential</span>
            </span>
            <span class="money text-sm font-semibold">
              {{ row.actual ? formatMoney(row.actual) : '—' }}
            </span>
          </div>

          <div class="mt-2 flex items-center gap-2">
            <div class="h-1.5 flex-1 overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
              <div
                v-if="row.actual && row.planned && row.planned.amount_minor > 0"
                class="h-full rounded-full"
                :class="{
                  'bg-state-severe': row.state === 'severely_over',
                  'bg-state-over': row.state === 'over',
                  'bg-state-approach': row.state === 'approaching',
                  'bg-state-within': row.state === 'within',
                  'bg-state-zero': row.state === 'zero',
                }"
                :style="{
                  width:
                    Math.min(100, (row.actual.amount_minor / row.planned.amount_minor) * 100) + '%',
                }"
              />
            </div>
            <span class="w-12 text-right text-xs text-slate-400">
              {{ formatPercent(row.ratio) }}
            </span>
            <StateBadge :state="row.state" :label="row.state_label" compact />
          </div>

          <p class="mt-1 text-xs text-slate-400">
            <template v-if="row.planned">
              plan {{ formatMoney(row.planned) }}
              <span v-if="row.actual">
                ·
                {{ toDisplayNumber(row.actual) <= toDisplayNumber(row.planned) ? 'left' : 'over' }}
                {{
                  formatMoney({
                    amount_minor: Math.abs(
                      row.planned.amount_minor - (row.actual?.amount_minor ?? 0),
                    ),
                    currency: row.planned.currency,
                    exponent: row.planned.exponent,
                  })
                }}
              </span>
            </template>
            <template v-else>no plan set</template>
          </p>
        </RouterLink>
      </li>
    </ul>

    <p v-if="report" class="mt-4 text-xs text-slate-400">
      Possible minimum (essential plans): {{ formatMoney(report.possible_minimum) }}
    </p>
  </section>
</template>
