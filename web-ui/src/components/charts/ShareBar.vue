<script setup lang="ts">
import { computed } from 'vue'

import { percent } from '@/lib/chart'

/**
 * Part-to-whole as one horizontal bar, with the parts listed under it.
 *
 * A bar rather than the spreadsheet's donut: comparing slices by angle is the
 * thing people are worst at, and the list underneath carries every figure
 * anyway — the bar is there to show the proportions at a glance, the list to be
 * read. Segments are separated by a 2px surface gap, never by an outline.
 *
 * Only positive parts make up a whole; a negative one (a credit card, a loan)
 * is listed below the bar with its figure but takes no width in it.
 */
const props = defineProps<{
  items: { key: string; label: string; value: number; color: string }[]
  format: (value: number) => string
}>()

const positive = computed(() => props.items.filter((i) => i.value > 0))
const whole = computed(() => positive.value.reduce((sum, i) => sum + i.value, 0))
</script>

<template>
  <div>
    <div v-if="whole > 0" class="flex h-4 w-full gap-0.5 overflow-hidden rounded-md" role="presentation">
      <div
        v-for="item in positive"
        :key="item.key"
        class="h-full first:rounded-l-md last:rounded-r-md"
        :style="{ width: `${(item.value / whole) * 100}%`, backgroundColor: item.color }"
        :title="`${item.label}: ${format(item.value)}`"
      />
    </div>
    <p v-else class="text-sm text-mf-muted">Nothing to show yet.</p>

    <ul class="mt-3 space-y-1.5 text-sm">
      <li v-for="item in items" :key="item.key" class="flex items-center gap-2">
        <span class="inline-block size-2.5 shrink-0 rounded-sm" :style="{ backgroundColor: item.color }" />
        <span class="min-w-0 flex-1 truncate">{{ item.label }}</span>
        <span class="font-medium tabular-nums">{{ format(item.value) }}</span>
        <span class="w-12 text-right text-xs text-mf-muted tabular-nums">
          {{ item.value > 0 && whole > 0 ? percent(item.value / whole, false, 0) : '' }}
        </span>
      </li>
    </ul>
  </div>
</template>
