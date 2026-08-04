<script setup lang="ts">
import { Banknote, Delete } from '@lucide/vue'

defineProps<{ amount: string; currency: string }>()
defineEmits<{ backspace: []; pickAccount: [] }>()
</script>

<template>
  <div class="mx-3 flex items-center gap-3 rounded-lg bg-mf-green px-3 py-4 text-white">
    <button
      type="button"
      class="flex shrink-0 flex-col items-center gap-0.5 border-r border-white/40 pr-3"
      aria-label="Choose account"
      @click="$emit('pickAccount')"
    >
      <Banknote :size="30" :stroke-width="1.5" />
      <span class="text-xs tracking-wide">{{ currency }}</span>
    </button>

    <!--
      The amount grows to the right and can get long (a chained calculation), so
      it scrolls rather than wrapping or pushing the backspace key off the row.
    -->
    <p class="min-w-0 flex-1 overflow-x-auto text-right text-4xl font-light tabular-nums">
      {{ amount }}
    </p>

    <button type="button" class="shrink-0 p-1" aria-label="Backspace" @click="$emit('backspace')">
      <Delete :size="28" :stroke-width="1.6" />
    </button>
  </div>
</template>
