<script setup lang="ts">
import { computed } from 'vue'
import type { BudgetState } from '@/api/types'

/**
 * Colour is never the only signal: every state carries an icon and a label
 * (docs/08-ux.md §8.1). The label text comes from the server so all clients agree.
 */
const props = defineProps<{ state: BudgetState; label: string; compact?: boolean }>()

const icons: Record<BudgetState, string> = {
  severely_over: '!!',
  over: '!',
  approaching: '~',
  within: '✓',
  zero: '–',
  not_recorded: '?',
}

const tones: Record<BudgetState, string> = {
  severely_over: 'bg-state-severe/15 text-state-severe',
  over: 'bg-state-over/15 text-state-over',
  approaching: 'bg-state-approach/15 text-state-approach',
  within: 'bg-state-within/15 text-state-within',
  zero: 'bg-state-zero/15 text-state-zero',
  not_recorded: 'bg-state-absent/15 text-state-absent',
}

const icon = computed(() => icons[props.state] ?? '?')
const tone = computed(() => tones[props.state] ?? tones.not_recorded)
</script>

<template>
  <span
    class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-semibold"
    :class="tone"
  >
    <span aria-hidden="true" class="font-mono">{{ icon }}</span>
    <span :class="compact ? 'sr-only' : ''">{{ label }}</span>
  </span>
</template>
