<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AllocationDonut from '@/components/AllocationDonut.vue'
import type { BurnMode, Money, SnapshotRow } from '@/api/types'
import { formatMoney, parseAmount, toDisplayNumber } from '@/lib/money'
import { currentPeriod, formatPeriod, shiftPeriod } from '@/lib/period'
import { pushToast } from '@/lib/toast'
import { useCapitalStore } from '@/stores/capital'

/**
 * The capital screen from docs/08-ux.md §8.5.
 *
 * The hero answers the question the workbook could not: how much of this
 * month's change was saving and how much was the currency moving.
 */
const capital = useCapitalStore()

const period = ref(currentPeriod())
const selectedClass = ref<string | null>(null)
const entering = ref(false)
const drafts = ref<Record<number, string>>({})
const saving = ref(false)

const report = computed(() => capital.report)

const burnModes: { value: BurnMode; label: string; hint: string }[] = [
  { value: 'actual_trailing_3', label: 'Last 3 months', hint: 'mean actual spend' },
  { value: 'actual_trailing_12', label: 'Last 12 months', hint: 'mean actual spend' },
  { value: 'essential_planned', label: 'Essentials only', hint: 'the planned minimum' },
  { value: 'legacy_blend', label: 'Spreadsheet', hint: 'the old three-way mean — legacy' },
]

const changeTone = computed(() => {
  const total = report.value?.change.total
  if (!total) return 'text-brand-200'
  return total.amount_minor < 0 ? 'text-state-severe' : 'text-state-within'
})

function move(months: number) {
  period.value = shiftPeriod(period.value, months)
}

/** Seeds the entry form from this month's figure, else last month's. */
function beginEntry() {
  drafts.value = {}
  for (const row of capital.editable) {
    const source = row.amount ?? row.previous_amount
    drafts.value[row.account_id] = source ? String(toDisplayNumber(source)) : ''
  }
  entering.value = true
}

function draftMoney(row: SnapshotRow): Money | null {
  const raw = drafts.value[row.account_id]
  if (raw === undefined || raw.trim() === '') return null
  const exponent =
    row.amount?.exponent ?? row.previous_amount?.exponent ?? exponentFor(row.currency)
  const minor = parseAmount(raw, exponent)
  if (minor === null) return null
  return { amount_minor: minor, currency: row.currency, exponent }
}

/** HUF is 0-decimal; everything else in the seeded set is 2. */
function exponentFor(code: string): number {
  return code === 'HUF' ? 0 : 2
}

const invalidDrafts = computed(() =>
  capital.editable.filter((row) => {
    const raw = drafts.value[row.account_id]
    return raw !== undefined && raw.trim() !== '' && draftMoney(row) === null
  }),
)

async function saveEntry() {
  const items = capital.editable
    .map((row) => ({ account_id: row.account_id, amount: draftMoney(row) }))
    .filter((item): item is { account_id: number; amount: Money } => item.amount !== null)

  saving.value = true
  try {
    await capital.saveSnapshots(period.value, items)
    await load()
    entering.value = false
    pushToast(`Recorded ${items.length} balances for ${formatPeriod(period.value)}.`, 'success')
  } catch (e) {
    pushToast(e instanceof Error ? e.message : 'The balances could not be saved.', 'error')
  } finally {
    saving.value = false
  }
}

async function pickBurnMode(mode: BurnMode) {
  await capital.setBurnMode(mode, period.value)
}

async function load() {
  await Promise.all([
    capital.loadReport(period.value),
    capital.loadSnapshots(period.value),
    capital.loadDrift(period.value),
  ])
}

watch(period, () => {
  entering.value = false
  selectedClass.value = null
  return load()
})
onMounted(load)
</script>

<template>
  <section>
    <header class="mb-4 flex items-center justify-between">
      <button
        type="button"
        class="tap-target rounded-lg px-3 text-xl text-slate-500"
        aria-label="Previous month"
        @click="move(-1)"
      >
        ‹
      </button>
      <h1 class="text-lg font-semibold">{{ formatPeriod(period) }}</h1>
      <button
        type="button"
        class="tap-target rounded-lg px-3 text-xl text-slate-500"
        aria-label="Next month"
        @click="move(1)"
      >
        ›
      </button>
    </header>

    <!-- Hero: net worth, and the change split into what the workbook conflated. -->
    <div
      class="mb-4 rounded-2xl bg-gradient-to-br from-brand-700 to-brand-900 p-5 text-white shadow-lg"
    >
      <p class="text-xs uppercase tracking-wide text-brand-200">Net worth</p>
      <p class="money mt-1 text-left text-4xl font-bold tabular-nums">
        {{ report?.general ? formatMoney(report.general) : '—' }}
      </p>
      <p v-if="!report?.general" class="mt-1 text-sm text-brand-200">
        No balances recorded for this month.
      </p>

      <dl v-if="report?.change.recorded" class="mt-4 grid grid-cols-3 gap-3 text-sm">
        <div>
          <dt class="text-brand-200">Change</dt>
          <dd class="money text-left font-semibold" :class="changeTone">
            {{ formatMoney(report.change.total) }}
          </dd>
        </div>
        <div>
          <dt class="text-brand-200">Saved</dt>
          <dd class="money text-left font-semibold">{{ formatMoney(report.change.real) }}</dd>
        </div>
        <div>
          <dt class="text-brand-200">Currency</dt>
          <dd class="money text-left font-semibold">{{ formatMoney(report.change.fx) }}</dd>
        </div>
      </dl>
      <p v-else class="mt-3 text-xs text-brand-200">
        No previous month to compare against, so there is no change to report.
      </p>
    </div>

    <!-- Runway, with the burn definition named rather than assumed. -->
    <div
      class="mb-4 rounded-2xl border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900"
    >
      <div class="flex items-baseline justify-between gap-3">
        <div>
          <p class="text-xs uppercase tracking-wide text-slate-500">Runway</p>
          <p class="money text-left text-2xl font-semibold">
            {{
              report?.runway_months !== null && report?.runway_months !== undefined
                ? `${report.runway_months.toFixed(1)} months`
                : '—'
            }}
          </p>
        </div>
        <p class="text-right text-xs text-slate-500">
          <span class="block">{{ formatMoney(report?.ready_for_usage) }} liquid</span>
          <span class="block">{{ formatMoney(report?.burn_rate) }} a month</span>
        </p>
      </div>

      <div class="mt-3 flex flex-wrap gap-1" role="group" aria-label="Burn rate definition">
        <button
          v-for="mode in burnModes"
          :key="mode.value"
          type="button"
          class="tap-target rounded-lg px-2.5 py-1 text-xs"
          :class="
            capital.burnMode === mode.value
              ? 'bg-brand-600 font-semibold text-white'
              : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300'
          "
          :aria-pressed="capital.burnMode === mode.value"
          :title="mode.hint"
          @click="pickBurnMode(mode.value)"
        >
          {{ mode.label }}
        </button>
      </div>
      <p v-if="capital.burnMode === 'legacy_blend'" class="mt-2 text-xs text-state-approach">
        The spreadsheet's own definition: the mean of three different measures, kept only so the old
        numbers can be reproduced.
      </p>
      <p v-else-if="report?.runway_months === null" class="mt-2 text-xs text-slate-500">
        Nothing was spent in this window, so there is no burn rate to divide by.
      </p>
    </div>

    <!-- Allocation, over leaf accounts, converted first. -->
    <div
      v-if="report?.allocation.length"
      class="mb-4 rounded-2xl border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900"
    >
      <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500">Allocation</h2>
      <AllocationDonut
        :shares="report.allocation"
        :selected="selectedClass"
        @select="selectedClass = $event"
      />
    </div>

    <!-- Drift: reported, never corrected. -->
    <details
      v-if="capital.drifted.length"
      class="mb-4 rounded-xl border border-state-approach/40 bg-state-approach/10 p-3"
    >
      <summary class="cursor-pointer text-sm font-medium text-state-approach">
        {{ capital.drifted.length }} account{{ capital.drifted.length === 1 ? '' : 's' }} where the
        transactions imply a different balance
      </summary>
      <p class="mt-2 text-xs text-slate-600 dark:text-slate-300">
        The snapshot is what counts. This is informational — nothing is corrected for you.
      </p>
      <ul class="mt-2 space-y-1 text-xs">
        <li v-for="row in capital.drifted" :key="row.account_id" class="flex justify-between gap-2">
          <span class="truncate">{{ row.name }}</span>
          <span class="money">{{ formatMoney(row.difference) }}</span>
        </li>
      </ul>
    </details>

    <!-- Accounts, grouped by asset class, native and base side by side. -->
    <div class="mb-4 flex items-center justify-between">
      <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Accounts</h2>
      <button
        v-if="!entering"
        type="button"
        class="tap-target rounded-lg px-3 py-1 text-sm font-semibold text-brand-600 dark:text-brand-300"
        @click="beginEntry"
      >
        Record balances
      </button>
    </div>

    <form v-if="entering" class="mb-6 space-y-2" @submit.prevent="saveEntry">
      <p class="text-xs text-slate-500">
        Pre-filled from last month. Confirm or adjust; leave a field blank to record nothing.
      </p>
      <label
        v-for="row in capital.editable"
        :key="row.account_id"
        class="flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
      >
        <span class="flex-1">
          <span class="block text-sm font-medium">{{ row.account_name }}</span>
          <span class="block text-xs text-slate-400">
            {{ row.currency }}
            <template v-if="row.previous_amount">
              · last month {{ formatMoney(row.previous_amount) }}
            </template>
          </span>
        </span>
        <input
          v-model="drafts[row.account_id]"
          inputmode="decimal"
          class="money tap-target w-32 rounded-lg border border-slate-300 px-3 py-2 dark:border-slate-700 dark:bg-slate-800"
          :aria-label="`${row.account_name} balance in ${row.currency}`"
        />
      </label>
      <p v-if="invalidDrafts.length" class="text-xs text-state-severe" role="alert">
        {{ invalidDrafts.map((r) => r.account_name).join(', ') }}: not an amount this currency can
        hold.
      </p>
      <div class="flex gap-2">
        <button
          type="button"
          class="tap-target flex-1 rounded-xl border border-slate-300 px-4 py-2.5 text-sm dark:border-slate-700"
          @click="entering = false"
        >
          Cancel
        </button>
        <button
          type="submit"
          class="tap-target flex-1 rounded-xl bg-brand-600 px-4 py-2.5 text-sm font-semibold text-white disabled:opacity-50"
          :disabled="saving || invalidDrafts.length > 0"
        >
          Save the month
        </button>
      </div>
    </form>

    <ul v-else class="space-y-1">
      <li
        v-for="row in capital.snapshots"
        :key="row.account_id"
        class="flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-3 text-sm dark:border-slate-800 dark:bg-slate-900"
        :class="row.computed ? 'border-dashed' : ''"
      >
        <span class="min-w-0 flex-1">
          <span class="block truncate font-medium">
            {{ row.account_name }}
            <span v-if="row.computed" class="text-xs font-normal text-slate-400">computed</span>
            <span v-if="row.is_liquid" class="text-xs font-normal text-state-within">liquid</span>
          </span>
          <span v-if="row.amount && !row.computed" class="block text-xs text-slate-400">
            {{ formatMoney(row.amount) }}
          </span>
        </span>
        <span class="money">{{ row.value ? formatMoney(row.value) : '—' }}</span>
      </li>
    </ul>
  </section>
</template>
