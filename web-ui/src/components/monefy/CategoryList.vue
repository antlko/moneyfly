<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { ref } from 'vue'

import { longDate } from '@/lib/period'
import type { CategoryTotal } from '@/stores/dashboard'
import type { Row } from '@/sync/types'
import CategoryIcon from './CategoryIcon.vue'
import MoneyAmount from './MoneyAmount.vue'

const props = defineProps<{
  totals: CategoryTotal[]
  rows: Row[]
  currency: string
}>()
defineEmits<{ edit: [Row] }>()

const expanded = ref<string | null>(null)

const toggle = (id: string) => {
  expanded.value = expanded.value === id ? null : id
}

/** A category's own transactions, newest first. */
const transactionsIn = (categoryId: string) =>
  props.rows
    .filter((r) => String(r.categoryId ?? '') === categoryId && r.kind === 'expense')
    .sort((a, b) => String(b.occurredOn).localeCompare(String(a.occurredOn)))
</script>

<template>
  <ul class="divide-y divide-mf-muted/25">
    <li v-for="total in totals" :key="total.category.id">
      <button
        type="button"
        class="flex w-full items-center gap-3 px-4 py-3 text-left"
        @click="toggle(String(total.category.id))"
      >
        <ChevronDown
          :size="18"
          class="shrink-0 text-mf-muted transition-transform"
          :class="expanded === total.category.id && 'rotate-180'"
        />
        <CategoryIcon :icon="total.category.icon" :color="total.category.color" :size="30" />
        <span class="min-w-0 flex-1 truncate text-base">{{ total.category.name }}</span>
        <span
          class="grid h-5 min-w-5 shrink-0 place-items-center rounded-full bg-mf-green px-1 text-xs font-medium text-white"
          >{{ total.count }}</span
        >
        <MoneyAmount
          :minor="Math.abs(total.totalMinor)"
          :currency="currency"
          class="shrink-0 text-base font-medium text-mf-red-text"
        />
      </button>

      <ul v-if="expanded === total.category.id" class="bg-mf-green-soft/15 px-4 pb-2">
        <li
          v-for="row in transactionsIn(String(total.category.id))"
          :key="row.id"
          class="flex items-center gap-3 border-t border-mf-muted/20 py-2 text-sm"
        >
          <div class="min-w-0 flex-1">
            <p class="truncate">{{ row.note || total.category.name }}</p>
            <p class="text-xs text-mf-muted">{{ longDate(String(row.occurredOn)) }}</p>
          </div>
          <MoneyAmount
            :minor="Math.abs(Number(row.amountMinor ?? 0))"
            :currency="String(row.currency ?? currency)"
            class="text-mf-red-text"
          />
          <button type="button" class="text-xs text-mf-green-dark" @click="$emit('edit', row)">
            Edit
          </button>
        </li>
      </ul>
    </li>

    <li v-if="!totals.length" class="px-4 py-10 text-center text-sm text-mf-muted">
      Nothing recorded this month.
    </li>
  </ul>
</template>
