<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { ref } from 'vue'

import type { CategoryTotal } from '@/stores/dashboard'
import type { Row } from '@/sync/types'
import CategoryIcon from './CategoryIcon.vue'
import MoneyAmount from './MoneyAmount.vue'
import TransactionRow from './TransactionRow.vue'

const props = defineProps<{
  totals: CategoryTotal[]
  rows: Row[]
  currency: string
}>()
defineEmits<{ open: [Row] }>()

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
        <!--
          The count badge sits against the name, not out at the right margin.
          Out there it reads as part of the amount; here it reads as part of the
          category, which is what it counts.
        -->
        <span class="flex min-w-0 items-center gap-2">
          <span class="truncate text-base">{{ total.category.name }}</span>
          <span
            class="grid h-5 min-w-5 shrink-0 place-items-center rounded-full bg-mf-green px-1 text-xs font-medium text-white"
            >{{ total.count }}</span
          >
        </span>
        <MoneyAmount
          :minor="Math.abs(total.totalMinor)"
          :currency="currency"
          class="ml-auto shrink-0 text-base font-medium text-mf-red-text"
        />
      </button>

      <ul v-if="expanded === total.category.id" class="bg-mf-green-soft/15 px-4">
        <li
          v-for="row in transactionsIn(String(total.category.id))"
          :key="row.id"
          class="border-t border-mf-muted/20"
        >
          <!-- The category is the row above; repeating it on every line is noise. -->
          <TransactionRow :row="row" hide-category @select="$emit('open', $event)" />
        </li>
      </ul>
    </li>

    <li v-if="!totals.length" class="px-4 py-10 text-center text-sm text-mf-muted">
      Nothing recorded this month.
    </li>
  </ul>
</template>
