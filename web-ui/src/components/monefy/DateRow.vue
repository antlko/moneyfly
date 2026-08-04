<script setup lang="ts">
import { CalendarDays } from '@lucide/vue'

import { longDate, type DayKey } from '@/lib/period'

defineProps<{ day: DayKey }>()
const emit = defineEmits<{ 'update:day': [DayKey] }>()
</script>

<template>
  <!--
    A native <input type="date"> laid transparently over the row. It gets the
    platform's own picker — the iOS wheel, the Android dialog — which is both
    better than anything we would build and already familiar.
  -->
  <label class="relative flex items-center justify-center gap-2 py-3 text-mf-ink">
    <CalendarDays :size="20" :stroke-width="1.7" class="text-mf-green-dark" />
    <span class="text-base">{{ longDate(day) }}</span>
    <input
      type="date"
      :value="day"
      class="absolute inset-0 cursor-pointer opacity-0"
      aria-label="Date"
      @input="emit('update:day', ($event.target as HTMLInputElement).value || day)"
    />
  </label>
</template>
