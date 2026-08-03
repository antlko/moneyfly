<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import { formatMoney, money, parseAmount } from '@/lib/money'
import { currentPeriod, formatPeriod, shiftPeriod } from '@/lib/period'
import { pushToast } from '@/lib/toast'
import { useBudgetStore } from '@/stores/budget'
import { useTaxonomyStore } from '@/stores/taxonomy'

/**
 * Budget planning. The workbook holds one annual figure per category, so the
 * default action seeds a whole range from one number each — the UI never asks for
 * 216 values (docs/03-data-model.md §3.4).
 */
const taxonomy = useTaxonomyStore()
const budget = useBudgetStore()

const fromPeriod = ref(currentPeriod())
const toPeriod = ref(shiftPeriod(currentPeriod(), 11))
const drafts = ref<Record<number, string>>({})
const busy = ref(false)
const error = ref('')

/**
 * The workbook's column B, for a starting point. These are reference figures, not
 * budget rows: nothing is written until the button is pressed.
 */
const workbookPlan: Record<string, number> = {
  House: 75000,
  Appliances: 15000,
  'Hotel/Trip': 25000,
  Food: 30000,
  'Eating out': 10000,
  Family: 6000,
  Gifts: 8000,
  Entertainment: 3000,
  Toiletry: 1500,
  Studying: 1500,
  Hobby: 2000,
  Clothes: 3000,
  Transport: 15000,
  Health: 22000,
  Communications: 2000,
  Sport: 3000,
  Bills: 3000,
  Services: 10000,
}

const total = computed(() => {
  let sum = 0
  for (const value of Object.values(drafts.value)) {
    sum += parseAmount(value, 2) ?? 0
  }
  return money(sum, 'EUR', 2)
})

const essentialTotal = computed(() => {
  let sum = 0
  for (const c of taxonomy.expenseCategories) {
    if (!c.is_essential) continue
    sum += parseAmount(drafts.value[c.id] ?? '', 2) ?? 0
  }
  return money(sum, 'EUR', 2)
})

function prefillFromWorkbook() {
  const next: Record<number, string> = {}
  for (const c of taxonomy.expenseCategories) {
    const minor = workbookPlan[c.name]
    if (minor !== undefined) next[c.id] = (minor / 100).toFixed(2)
  }
  drafts.value = next
}

async function loadExisting() {
  await budget.loadPlans(fromPeriod.value)
  const next: Record<number, string> = {}
  for (const plan of budget.plans) {
    next[plan.category_id] = (plan.planned.amount_minor / 10 ** plan.planned.exponent).toFixed(
      plan.planned.exponent,
    )
  }
  drafts.value = next
}

async function seed() {
  error.value = ''
  busy.value = true
  try {
    const items = []
    for (const c of taxonomy.expenseCategories) {
      const raw = drafts.value[c.id]
      if (raw === undefined || raw === '') continue
      const minor = parseAmount(raw, 2)
      if (minor === null) {
        error.value = `“${raw}” is not a valid amount for ${c.name}.`
        return
      }
      items.push({ category_id: c.id, planned: money(minor, 'EUR', 2) })
    }
    if (items.length === 0) {
      error.value = 'Nothing to seed: fill in at least one category.'
      return
    }
    const result = await budget.bulkSeed(fromPeriod.value, toPeriod.value, items)
    pushToast(
      `Wrote ${result.rows_written} plan rows across ${result.periods_written} months.`,
      'success',
    )
  } catch (err) {
    error.value =
      err instanceof ApiError
        ? Object.values(err.fieldErrors)[0] || err.message
        : 'Could not reach the server.'
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  await taxonomy.load()
  await loadExisting()
})
</script>

<template>
  <section>
    <h1 class="mb-1 text-lg font-semibold">Plan</h1>
    <p class="mb-4 text-xs text-slate-500">
      One figure per category, applied to every month in the range. Leave a category blank to leave
      it unplanned — that is not the same as planning zero.
    </p>

    <div class="mb-4 grid grid-cols-2 gap-2">
      <div>
        <label for="p-from" class="mb-1 block text-sm font-medium">From</label>
        <input
          id="p-from"
          v-model="fromPeriod"
          type="month"
          class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
          @change="loadExisting"
        />
      </div>
      <div>
        <label for="p-to" class="mb-1 block text-sm font-medium">To</label>
        <input
          id="p-to"
          v-model="toPeriod"
          type="month"
          class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
        />
      </div>
    </div>

    <p class="mb-3 text-xs text-slate-500">
      {{ formatPeriod(fromPeriod) }} → {{ formatPeriod(toPeriod) }}
    </p>

    <button
      type="button"
      class="tap-target mb-4 rounded-xl border border-slate-300 px-3 py-2 text-sm dark:border-slate-700"
      @click="prefillFromWorkbook"
    >
      Prefill from the workbook's figures
    </button>

    <ul class="space-y-1">
      <li
        v-for="c in taxonomy.expenseCategories"
        :key="c.id"
        class="flex items-center gap-2 rounded-xl border border-slate-200 bg-white p-2 dark:border-slate-800 dark:bg-slate-900"
      >
        <span aria-hidden="true">{{ c.icon ?? '•' }}</span>
        <label :for="'plan-' + c.id" class="flex-1 truncate text-sm">
          {{ c.name }}
          <span v-if="c.is_essential" class="text-xs text-slate-400">essential</span>
        </label>
        <input
          :id="'plan-' + c.id"
          v-model="drafts[c.id]"
          inputmode="decimal"
          placeholder="—"
          class="money w-24 rounded-lg border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
        />
      </li>
    </ul>

    <dl class="mt-4 space-y-1 text-sm">
      <div class="flex justify-between">
        <dt>Planned total</dt>
        <dd class="money font-semibold">{{ formatMoney(total) }}</dd>
      </div>
      <div class="flex justify-between text-slate-500">
        <dt>Possible minimum (essential only)</dt>
        <dd class="money">{{ formatMoney(essentialTotal) }}</dd>
      </div>
    </dl>

    <p v-if="error" class="mt-3 text-sm text-state-severe" role="alert">{{ error }}</p>

    <button
      type="button"
      class="tap-target mt-4 w-full rounded-xl bg-brand-600 px-4 py-3 font-semibold text-white disabled:opacity-50"
      :disabled="busy"
      @click="seed"
    >
      {{ busy ? 'Writing…' : 'Apply to every month in the range' }}
    </button>
  </section>
</template>
