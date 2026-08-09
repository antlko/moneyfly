<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useNotifyStore } from '@/stores/notify'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { freqLabel } from '@/lib/recurring'
import { shortDate } from '@/lib/period'
import { useRecurringStore } from '@/stores/recurring'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'

const recurring = useRecurringStore()
const taxonomy = useTaxonomyStore()
const notify = useNotifyStore()

const categoryOf = (rule: Row) => taxonomy.byId.get(String(rule.categoryId ?? ''))
const isEmpty = computed(() => recurring.rules.length === 0)

/**
 * Delete, with an undo — the same shape as a transaction's own delete
 * (RecordView) and for the same reason: a confirmation dialogue in front of
 * every delete only trains people to dismiss it.
 *
 * There is deliberately no edit here: changing the amount, category or
 * schedule is delete-and-recreate from the record screen, which is also
 * where creating one lives in the first place — one path instead of two that
 * have to be kept in sync with each other.
 */
async function remove(rule: Row) {
  const body = { ...rule } as Record<string, unknown>
  for (const k of ['id', 'lamport', 'deviceId', 'updatedAt', 'deleted']) delete body[k]
  const id = String(rule.id)

  await sync.remove('recurring_rule', id)
  notify.undo('Recurring record deleted', () => void sync.write('recurring_rule', body, id))
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Recurring" />

    <main class="flex-1 overflow-y-auto pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <p v-if="isEmpty" class="p-4 text-sm text-mf-muted">
        Nothing repeats yet. Mark a new expense or income as recurring with the
        <span class="mx-1 inline-grid size-5 place-items-center rounded-full bg-mf-green-soft/40 align-middle"
          >↻</span
        >
        control on its screen.
      </p>

      <ul v-else class="divide-y divide-mf-muted/25">
        <li v-for="rule in recurring.bySoonest" :key="rule.id" class="flex items-center gap-3 px-4 py-3">
          <CategoryIcon :icon="categoryOf(rule)?.icon" :color="categoryOf(rule)?.color" :size="28" />
          <div class="min-w-0 flex-1">
            <p class="truncate">{{ categoryOf(rule)?.name ?? 'Uncategorised' }}</p>
            <p class="text-xs text-mf-muted">
              {{ freqLabel(rule.freq) }} · next {{ shortDate(String(rule.nextOn)) }}
            </p>
          </div>
          <MoneyAmount
            :minor="Math.abs(Number(rule.amountMinor ?? 0))"
            :currency="String(rule.currency)"
            class="font-medium"
            :class="rule.kind === 'expense' ? 'text-mf-red-text' : 'text-mf-green-dark'"
          />
          <button
            type="button"
            class="grid size-8 shrink-0 place-items-center rounded-full text-mf-muted"
            aria-label="Delete recurring record"
            @click="remove(rule)"
          >
            <Trash2 :size="18" :stroke-width="1.8" />
          </button>
        </li>
      </ul>
    </main>
  </div>
</template>
