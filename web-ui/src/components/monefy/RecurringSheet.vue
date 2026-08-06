<script setup lang="ts">
import { freqLabel, RECURRING_FREQS } from '@/lib/recurring'
import type { RecurringFreq } from '@/lib/period'

defineEmits<{ select: [RecurringFreq]; close: [] }>()
</script>

<template>
  <div class="fixed inset-0 z-50 flex flex-col justify-end mf-scrim" @click.self="$emit('close')">
    <section
      class="max-h-[70%] overflow-y-auto rounded-t-2xl bg-mf-bg pb-[calc(1rem+var(--spacing-safe-b))]"
    >
      <header class="px-4 pt-3 pb-2">
        <div class="mx-auto mb-3 h-1 w-10 rounded-full bg-mf-muted" />
        <h2 class="text-base font-medium">Repeat</h2>
        <p class="mt-1 text-sm text-mf-muted">
          Saves this record now, then again automatically on this schedule.
        </p>
      </header>

      <ul class="divide-y divide-mf-muted/20">
        <li v-for="freq in RECURRING_FREQS" :key="freq">
          <button
            type="button"
            class="flex w-full items-center px-4 py-3 text-left"
            @click="$emit('select', freq)"
          >
            {{ freqLabel(freq) }}
          </button>
        </li>
      </ul>
    </section>
  </div>
</template>
