<script setup lang="ts">
import { CloudAlert, CloudCheck, CloudOff, CloudUpload, RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
import { toast } from 'vue-sonner'

import { useSyncStore } from '@/stores/sync'

/**
 * What sync is doing, in the header, always visible.
 *
 * It replaced a line of text under the chart that appeared and disappeared as
 * the state changed — which moved the layout on every write and told you
 * "Offline" with no way to ask what that meant. A control that is always in the
 * same place and only changes its icon says the same thing without ever moving
 * anything, and gives the answer a place to live: tapping it syncs now.
 */
const sync = useSyncStore()

const view = computed(() => {
  if (sync.state === 'syncing') {
    return { icon: RefreshCw, spin: true, label: 'Syncing…', tone: 'text-white' }
  }
  if (sync.state === 'error') {
    return { icon: CloudAlert, spin: false, label: sync.error ?? 'Sync error', tone: 'text-white' }
  }
  if (sync.state === 'offline') {
    return {
      icon: CloudOff,
      spin: false,
      // The reassurance matters more than the fact. Offline costs nothing here:
      // the record is already on the device and the queue drains by itself.
      label:
        sync.pending > 0
          ? `Offline — ${sync.pending} waiting, safe on this device`
          : 'Offline — your records are safe on this device',
      tone: 'text-white/70',
    }
  }
  if (sync.pending > 0) {
    return {
      icon: CloudUpload,
      spin: false,
      label: `${sync.pending} waiting to send`,
      tone: 'text-white',
    }
  }
  return { icon: CloudCheck, spin: false, label: 'Up to date', tone: 'text-white/70' }
})

/** Tapping syncs now, and says what happened — the point is the answer. */
async function syncNow() {
  await sync.syncNow()
  toast(view.value.label)
}
</script>

<template>
  <button
    type="button"
    class="p-2"
    :class="view.tone"
    :aria-label="view.label"
    :title="view.label"
    @click="syncNow"
  >
    <component
      :is="view.icon"
      :size="22"
      :stroke-width="1.8"
      :class="view.spin && 'animate-spin'"
    />
  </button>
</template>
