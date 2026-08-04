<script setup lang="ts">
import { computed } from 'vue'

import { periodLabel, shiftPeriod, type Period } from '@/lib/period'

const props = defineProps<{ period: Period }>()
const emit = defineEmits<{ select: [Period] }>()

const previous = computed(() => shiftPeriod(props.period, -1))
const next = computed(() => shiftPeriod(props.period, 1))
// "All time" has nothing on either side, so the neighbours are hidden rather
// than repeating the same label three times.
const pageable = computed(() => props.period.kind !== 'all')

const label = (p: Period) => periodLabel(p)
</script>

<template>
  <div class="flex shrink-0 items-baseline justify-between px-4 py-2 select-none">
    <button
      v-if="pageable"
      type="button"
      class="min-w-0 flex-1 truncate text-left text-base text-mf-muted"
      @click="emit('select', previous)"
    >
      {{ label(previous) }}
    </button>
    <span v-else class="flex-1" />

    <span class="flex-[1.4] truncate px-2 text-center text-lg font-medium text-mf-green-dark">
      {{ label(period) }}
    </span>

    <button
      v-if="pageable"
      type="button"
      class="min-w-0 flex-1 truncate text-right text-base text-mf-muted"
      @click="emit('select', next)"
    >
      {{ label(next) }}
    </button>
    <span v-else class="flex-1" />
  </div>
</template>
