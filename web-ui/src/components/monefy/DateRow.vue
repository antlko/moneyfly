<script setup lang="ts">
import { CalendarDays, ChevronDown } from '@lucide/vue'
import { useTemplateRef } from 'vue'

import { longDate, type DayKey } from '@/lib/period'

defineProps<{ day: DayKey }>()
const emit = defineEmits<{ 'update:day': [DayKey] }>()

const input = useTemplateRef<HTMLInputElement>('input')

/**
 * Open the platform's own date picker.
 *
 * The input used to be laid transparently over the whole row, on the assumption
 * that a click anywhere would open it. On a phone that holds; on desktop Chrome
 * and Firefox it does not — only the calendar indicator opens the picker, and
 * that indicator was precisely the part `opacity-0` hid. So the row read as
 * dead. `showPicker()` asks directly, and it throws rather than failing quietly
 * (no user activation, or a browser without it), which is what the fallback is
 * for.
 */
function open() {
  const el = input.value
  if (!el) return
  try {
    el.showPicker()
  } catch {
    el.click()
  }
}
</script>

<template>
  <button
    type="button"
    class="relative flex h-11 shrink-0 items-center justify-center gap-2 text-mf-ink"
    @click="open"
  >
    <CalendarDays :size="20" :stroke-width="1.7" class="text-mf-green-dark" />
    <span class="text-base">{{ longDate(day) }}</span>
    <ChevronDown :size="16" :stroke-width="2" class="text-mf-muted" />
    <!--
      Still a native input: it brings the iOS wheel and the Android dialog, both
      better than anything we would build and already familiar. Sized to nothing
      rather than hidden, because `showPicker()` refuses on a `display: none`
      element.
    -->
    <input
      ref="input"
      type="date"
      :value="day"
      class="pointer-events-none absolute size-0 opacity-0"
      tabindex="-1"
      aria-label="Date"
      @input="emit('update:day', ($event.target as HTMLInputElement).value || day)"
    />
  </button>
</template>
