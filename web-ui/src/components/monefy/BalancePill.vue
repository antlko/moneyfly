<script setup lang="ts">
import { AlignJustify, ArrowDownWideNarrow } from '@lucide/vue'
import { computed } from 'vue'

import MoneyAmount from './MoneyAmount.vue'

const props = defineProps<{
  balanceMinor: number
  currency: string
  /** In donut mode the reference shows the toggle on both sides of the pill. */
  showTrailingToggle?: boolean
}>()
const emit = defineEmits<{ toggleView: []; toggleSort: []; open: [] }>()

const negative = computed(() => props.balanceMinor < 0)

/*
 * Three taps, no gestures.
 *
 * The row is also the handle of the dashboard's pull-up sheet, and dragging it
 * is the sheet's business, not this component's — PullUpSheet owns the pointer,
 * moves the whole list with it, and swallows the click a drag ends with before
 * it can reach any of the buttons below. What is left here is what a tap does:
 * `≡` switches view (the same travel, animated), the pill opens the period's
 * records, and the arrows change the sort.
 */
</script>

<template>
  <div class="flex w-full shrink-0 items-center gap-2 px-4">
    <button
      type="button"
      class="p-2 text-mf-green-dark"
      aria-label="Switch view"
      @click="emit('toggleView')"
    >
      <AlignJustify :size="24" :stroke-width="2" />
    </button>

    <button
      type="button"
      class="flex flex-1 items-center justify-center gap-3 rounded-md px-4 py-2.5 text-white shadow-sm"
      :class="negative ? 'bg-mf-red' : 'bg-mf-green'"
      aria-label="Show records for this period. Drag up or down to switch view."
      @click="emit('open')"
    >
      <span class="text-base font-medium">Balance</span>
      <MoneyAmount :minor="balanceMinor" :currency="currency" class="text-lg font-semibold" />
    </button>

    <button
      v-if="showTrailingToggle"
      type="button"
      class="p-2 text-mf-green-dark"
      aria-label="Switch view"
      @click="emit('toggleView')"
    >
      <AlignJustify :size="24" :stroke-width="2" />
    </button>
    <button
      v-else
      type="button"
      class="p-2 text-mf-green-dark"
      aria-label="Change sorting"
      @click="emit('toggleSort')"
    >
      <ArrowDownWideNarrow :size="24" :stroke-width="2" />
    </button>
  </div>
</template>
