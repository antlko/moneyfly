<script setup lang="ts">
import { useOnline } from '@/lib/offline'

/**
 * Unmistakable, and honest about what still works: the shell is cached, the data
 * is not. Nothing is queued for later (docs/adr/0012-defer-offline-entry.md).
 */
const { online } = useOnline()
</script>

<template>
  <div
    v-if="!online"
    class="sticky top-0 z-50 bg-state-severe px-4 py-2 text-center text-sm font-medium text-white"
    role="status"
    aria-live="polite"
  >
    Offline — showing the last loaded screen. Saving is unavailable until the connection is back.
  </div>
</template>
