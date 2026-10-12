<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useNotifyStore } from '@/stores/notify'

import BudgetSheet from '@/components/monefy/BudgetSheet.vue'
import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { toMajor, toMinor } from '@/lib/money'
import { currentMonth, monthLabelWithYear } from '@/lib/period'
import { useBudgetsStore, type BudgetProgress } from '@/stores/budgets'
import { useDashboardStore } from '@/stores/dashboard'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'

const budgets = useBudgetsStore()
const notify = useNotifyStore()
const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()

const showCreate = ref(false)
const editing = ref<BudgetProgress | null>(null)

const categoryOf = (entry: BudgetProgress) =>
  entry.budget.categoryId ? taxonomy.byId.get(String(entry.budget.categoryId)) : undefined

// The overall cap first — it applies to everything below it — then each
// category's budget alphabetically, so the list order does not reshuffle
// itself as spending changes.
const sorted = computed(() =>
  [...budgets.progress].sort((a, b) => {
    const an = String(categoryOf(a)?.name ?? '')
    const bn = String(categoryOf(b)?.name ?? '')
    return an.localeCompare(bn)
  }),
)

/**
 * Where in the month we are, for judging pace.
 *
 * A budget at 60% means nothing on its own: on the 25th it is going well, on
 * the 5th it is not. The bar carries a tick at "how much of the month has gone"
 * and the card says whether spending is ahead of it.
 */
const month = computed(() => {
  const now = new Date()
  const days = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate()
  const day = now.getDate()
  return {
    label: monthLabelWithYear(currentMonth()),
    pace: day / days,
    // Today still counts — it can still be spent in.
    daysLeft: days - day + 1,
  }
})

const remaining = (entry: BudgetProgress) => Number(entry.budget.limitMinor ?? 0) - entry.spentMinor

/** What is left to spend each remaining day, to stay inside the budget. */
const perDay = (entry: BudgetProgress) => Math.floor(Math.max(0, remaining(entry)) / month.value.daysLeft)

type Standing = 'over' | 'ahead' | 'ok'
function standing(entry: BudgetProgress): Standing {
  if (entry.share > 1) return 'over'
  return entry.share > month.value.pace ? 'ahead' : 'ok'
}

/**
 * The month as a whole: the overall cap when there is one; otherwise every
 * category budget in the base currency, added up.
 */
const summary = computed(() => {
  const overall = budgets.progress.find((p) => !p.budget.categoryId)
  if (overall) return { label: 'Overall', limit: Number(overall.budget.limitMinor ?? 0), spent: overall.spentMinor }
  const inBase = budgets.progress.filter((p) => p.budget.currency === dashboard.baseCurrency)
  if (!inBase.length) return null
  return {
    label: 'All category budgets',
    limit: inBase.reduce((sum, p) => sum + Number(p.budget.limitMinor ?? 0), 0),
    spent: inBase.reduce((sum, p) => sum + p.spentMinor, 0),
  }
})

async function create(input: { categoryId?: string; amount: string }) {
  showCreate.value = false
  await budgets.create({
    limitMinor: toMinor(Number(input.amount), dashboard.baseCurrency),
    currency: dashboard.baseCurrency,
    categoryId: input.categoryId,
  })
}

async function saveEdit(input: { categoryId?: string; amount: string }) {
  const entry = editing.value
  editing.value = null
  if (!entry) return
  const currency = String(entry.budget.currency ?? dashboard.baseCurrency)
  await budgets.update(entry.budget, {
    limitMinor: toMinor(Number(input.amount), currency),
    categoryId: input.categoryId,
  })
}

const editInitial = computed(() => {
  const entry = editing.value
  if (!entry) return undefined
  return {
    categoryId: entry.budget.categoryId ? String(entry.budget.categoryId) : null,
    amount: String(toMajor(Number(entry.budget.limitMinor ?? 0), String(entry.budget.currency))),
  }
})

/**
 * Delete, with an undo — the same shape as everywhere else a delete exists in
 * this app (RecordView, RecurringView), for the same reason: a confirmation
 * dialogue in front of every delete only trains people to dismiss it.
 */
async function remove(entry: BudgetProgress) {
  const id = String(entry.budget.id)
  const body = { ...entry.budget } as Record<string, unknown>
  for (const k of ['id', 'lamport', 'deviceId', 'updatedAt', 'deleted']) delete body[k]

  await sync.remove('budget', id)
  notify.undo('Budget deleted', () => void sync.write('budget', body, id))
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Budgets">
      <template #actions>
        <button type="button" aria-label="New budget" class="grid size-11 place-items-center" @click="showCreate = true">
          <Plus :size="22" :stroke-width="2" />
        </button>
      </template>
    </ScreenHeader>

    <main
      class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-5xl sm:p-0"
    >
      <!-- The month, and how far into it we are: the frame every bar below is read against. -->
      <section class="rounded-2xl bg-mf-surface p-4 sm:p-5">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <h2 class="text-lg font-medium">{{ month.label }}</h2>
          <p class="text-sm text-mf-muted">
            {{ month.daysLeft }} day{{ month.daysLeft === 1 ? '' : 's' }} left ·
            {{ Math.round(month.pace * 100) }}% of the month gone
          </p>
        </div>
        <template v-if="summary">
          <p class="mt-3 flex flex-wrap items-baseline gap-x-2 text-sm">
            <span class="text-mf-muted">{{ summary.label }}:</span>
            <MoneyAmount :minor="summary.spent" :currency="dashboard.baseCurrency" class="text-xl font-semibold" />
            <span class="text-mf-muted">of</span>
            <MoneyAmount :minor="summary.limit" :currency="dashboard.baseCurrency" />
          </p>
          <div class="relative mt-2 h-2.5 overflow-hidden rounded-full bg-mf-muted/25">
            <div
              class="h-full rounded-full transition-[width]"
              :class="summary.spent > summary.limit ? 'bg-mf-red' : 'bg-mf-green'"
              :style="{ width: `${Math.min(summary.limit ? summary.spent / summary.limit : 0, 1) * 100}%` }"
            />
            <div class="absolute inset-y-0 w-0.5 bg-mf-ink/50" :style="{ left: `${month.pace * 100}%` }" />
          </div>
        </template>
      </section>

      <p v-if="sorted.length === 0" class="rounded-2xl bg-mf-surface p-4 text-sm text-mf-muted">
        No budgets yet. A budget is a monthly cap — overall, or for one category — and it tracks
        the current calendar month automatically.
        <button type="button" class="ml-1 text-mf-green-dark underline" @click="showCreate = true">
          Create one
        </button>
      </p>

      <ul v-else class="grid gap-3 sm:grid-cols-2">
        <li v-for="entry in sorted" :key="entry.budget.id">
          <!-- The card is the edit control; the bin is the only other thing on it. -->
          <div
            role="button"
            tabindex="0"
            class="rounded-2xl bg-mf-surface p-4 text-left transition-shadow hover:shadow-md"
            :aria-label="`Edit the ${categoryOf(entry)?.name ?? 'overall'} budget`"
            @click="editing = entry"
            @keydown.enter="editing = entry"
          >
            <div class="flex items-center gap-3">
              <CategoryIcon
                v-if="categoryOf(entry)"
                :icon="categoryOf(entry)?.icon"
                :color="categoryOf(entry)?.color"
                :size="24"
              />
              <p class="min-w-0 flex-1 truncate font-medium">{{ categoryOf(entry)?.name ?? 'Overall' }}</p>
              <button
                type="button"
                class="grid size-8 shrink-0 place-items-center rounded-full text-mf-muted hover:bg-mf-bg hover:text-mf-red-text"
                aria-label="Delete budget"
                @click.stop="remove(entry)"
              >
                <Trash2 :size="16" :stroke-width="1.8" />
              </button>
            </div>

            <p class="mt-3 flex items-baseline gap-1.5">
              <MoneyAmount :minor="entry.spentMinor" :currency="String(entry.budget.currency)" class="text-lg font-semibold" />
              <span class="text-sm text-mf-muted">
                of <MoneyAmount :minor="Number(entry.budget.limitMinor ?? 0)" :currency="String(entry.budget.currency)" />
              </span>
              <span class="ml-auto text-sm text-mf-muted tabular-nums">{{ Math.round(entry.share * 100) }}%</span>
            </p>

            <!-- The tick is where spending "should" be by today. -->
            <div class="relative mt-2 h-2 overflow-hidden rounded-full bg-mf-muted/25">
              <div
                class="h-full rounded-full transition-[width]"
                :class="entry.share > 1 ? 'bg-mf-red' : 'bg-mf-green'"
                :style="{ width: `${Math.min(entry.share, 1) * 100}%` }"
              />
              <div class="absolute inset-y-0 w-0.5 bg-mf-ink/50" :style="{ left: `${month.pace * 100}%` }" />
            </div>

            <p class="mt-2 flex flex-wrap gap-x-3 text-xs">
              <span v-if="standing(entry) === 'over'" class="text-mf-red-text">
                Over by
                <MoneyAmount :minor="-remaining(entry)" :currency="String(entry.budget.currency)" />
              </span>
              <template v-else>
                <span class="text-mf-ink">
                  <MoneyAmount :minor="remaining(entry)" :currency="String(entry.budget.currency)" /> left ·
                  <MoneyAmount :minor="perDay(entry)" :currency="String(entry.budget.currency)" />/day
                </span>
                <span v-if="standing(entry) === 'ahead'" class="text-mf-red-text">ahead of pace</span>
                <span v-else class="text-mf-green-dark">on track</span>
              </template>
              <span class="ml-auto text-mf-muted">
                Last month <MoneyAmount :minor="entry.lastMonthMinor" :currency="String(entry.budget.currency)" />
              </span>
            </p>

            <p v-if="entry.unconverted > 0" class="mt-2 text-xs text-mf-red-text">
              {{ entry.unconverted }} expense{{ entry.unconverted === 1 ? '' : 's' }} this month
              {{ entry.unconverted === 1 ? "isn't" : "aren't" }} counted above — no exchange rate yet.
            </p>
          </div>
        </li>
      </ul>
    </main>

    <Transition name="mf-sheet">
      <BudgetSheet
        v-if="showCreate"
        :currency="dashboard.baseCurrency"
        @cancel="showCreate = false"
        @create="create"
      />
    </Transition>

    <Transition name="mf-sheet">
      <BudgetSheet
        v-if="editing"
        :key="String(editing.budget.id)"
        :currency="String(editing.budget.currency)"
        :initial="editInitial"
        @cancel="editing = null"
        @create="saveEdit"
      />
    </Transition>
  </div>
</template>
