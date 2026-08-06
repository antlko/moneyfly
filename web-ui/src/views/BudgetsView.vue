<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { computed, ref } from 'vue'
import { toast } from 'vue-sonner'

import BudgetSheet from '@/components/monefy/BudgetSheet.vue'
import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { toMinor } from '@/lib/money'
import { useBudgetsStore, type BudgetProgress } from '@/stores/budgets'
import { useDashboardStore } from '@/stores/dashboard'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'

const budgets = useBudgetsStore()
const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()

const showCreate = ref(false)

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

async function create(input: { categoryId?: string; amount: string }) {
  showCreate.value = false
  await budgets.create({
    limitMinor: toMinor(Number(input.amount), dashboard.baseCurrency),
    currency: dashboard.baseCurrency,
    categoryId: input.categoryId,
  })
}

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
  toast('Budget deleted', {
    action: { label: 'Undo', onClick: () => void sync.write('budget', body, id) },
  })
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Budgets">
      <template #actions>
        <button type="button" aria-label="New budget" class="p-2" @click="showCreate = true">
          <Plus :size="22" :stroke-width="2" />
        </button>
      </template>
    </ScreenHeader>

    <main class="flex-1 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <p v-if="sorted.length === 0" class="text-sm text-mf-muted">
        No budgets yet. A budget is a monthly cap — overall, or for one category — and it tracks
        the current calendar month automatically.
      </p>

      <ul v-else class="space-y-3">
        <li v-for="entry in sorted" :key="entry.budget.id" class="rounded-2xl bg-mf-surface p-4">
          <div class="flex items-center gap-3">
            <CategoryIcon
              v-if="categoryOf(entry)"
              :icon="categoryOf(entry)?.icon"
              :color="categoryOf(entry)?.color"
              :size="24"
            />
            <div class="min-w-0 flex-1">
              <p class="truncate font-medium">{{ categoryOf(entry)?.name ?? 'Overall' }}</p>
            </div>
            <MoneyAmount :minor="entry.spentMinor" :currency="String(entry.budget.currency)" class="text-sm" />
            <span class="text-sm text-mf-muted">
              / <MoneyAmount :minor="Number(entry.budget.limitMinor ?? 0)" :currency="String(entry.budget.currency)" />
            </span>
            <button
              type="button"
              class="grid size-8 shrink-0 place-items-center rounded-full text-mf-muted"
              aria-label="Delete budget"
              @click="remove(entry)"
            >
              <Trash2 :size="16" :stroke-width="1.8" />
            </button>
          </div>

          <div class="mt-3 h-2 overflow-hidden rounded-full bg-mf-muted/25">
            <div
              class="h-full rounded-full transition-[width]"
              :class="entry.share >= 1 ? 'bg-mf-red' : 'bg-mf-green'"
              :style="{ width: `${Math.min(entry.share, 1) * 100}%` }"
            />
          </div>

          <p v-if="entry.unconverted > 0" class="mt-2 text-xs text-mf-red-text">
            {{ entry.unconverted }} expense{{ entry.unconverted === 1 ? '' : 's' }} this month
            {{ entry.unconverted === 1 ? "isn't" : "aren't" }} counted above — no exchange rate yet.
          </p>
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
  </div>
</template>
