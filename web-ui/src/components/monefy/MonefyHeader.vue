<script setup lang="ts">
import { ArrowLeftRight, EllipsisVertical, Funnel, Search } from '@lucide/vue'

defineProps<{ subtitle: string }>()
defineEmits<{ filter: []; search: []; transfer: []; menu: [] }>()
</script>

<template>
  <!--
    The reference header: account filter on the left, the wordmark over the
    active filter, then search, transfer and the menu. Fixed 56px row under the
    status-bar inset, per docs/MONEFY-PARITY.md §1.
  -->
  <header class="bg-mf-green px-2 pt-safe-t text-white">
    <div class="flex h-14 items-center gap-1">
      <button type="button" class="p-2" aria-label="Filter accounts" @click="$emit('filter')">
        <Funnel :size="22" :stroke-width="1.8" />
      </button>

      <div class="min-w-0 flex-1 px-1 text-center">
        <p class="truncate text-lg leading-tight font-semibold tracking-tight">moneyfly</p>
        <p class="truncate text-xs leading-tight text-white/80">{{ subtitle }}</p>
      </div>

      <button type="button" class="p-2" aria-label="Search" @click="$emit('search')">
        <Search :size="22" :stroke-width="1.8" />
      </button>
      <button type="button" class="p-2" aria-label="Transfer" @click="$emit('transfer')">
        <ArrowLeftRight :size="22" :stroke-width="1.8" />
      </button>
      <button type="button" class="p-2" aria-label="Menu" @click="$emit('menu')">
        <EllipsisVertical :size="22" :stroke-width="1.8" />
      </button>
    </div>
  </header>
</template>
