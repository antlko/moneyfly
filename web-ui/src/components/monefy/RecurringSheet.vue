<script setup lang="ts">
import { freqLabel, RECURRING_FREQS } from '@/lib/recurring'
import type { RecurringFreq } from '@/lib/period'

import ModalSheet from './ModalSheet.vue'

defineEmits<{ select: [RecurringFreq]; close: [] }>()
</script>

<template>
  <ModalSheet size="sm" label="Repeat" @close="$emit('close')">
    <section class="pb-4">
      <header class="px-4 pt-3 pb-2 sm:pt-5">
        <div class="mx-auto mb-3 h-1 w-10 rounded-full bg-mf-muted sm:hidden" />
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
  </ModalSheet>
</template>
