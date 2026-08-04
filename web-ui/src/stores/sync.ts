import { defineStore } from 'pinia'
import { computed } from 'vue'

import { deviceId } from '@/lib/device'
import { sync } from '@/sync/engine'

/** Sync status for the UI. Read-only — writes go through the engine directly. */
export const useSyncStore = defineStore('sync', () => {
  const label = computed(() => {
    switch (sync.state.value) {
      case 'syncing':
        return 'Syncing…'
      case 'offline':
        return sync.pending.value > 0 ? `Offline · ${sync.pending.value} to send` : 'Offline'
      case 'error':
        return sync.error.value ?? 'Sync error'
      default:
        return sync.pending.value > 0 ? `${sync.pending.value} to send` : 'Up to date'
    }
  })

  return {
    state: sync.state,
    pending: sync.pending,
    lastSyncAt: sync.lastSyncAt,
    streamConnected: sync.streamConnected,
    error: sync.error,
    revision: sync.revision,
    deviceId: deviceId(),
    label,
    syncNow: () => sync.sync(),
  }
})
