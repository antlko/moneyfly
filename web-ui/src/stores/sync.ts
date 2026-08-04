import { defineStore } from 'pinia'
import { computed } from 'vue'

import { deviceId } from '@/lib/device'
import { sync } from '@/sync/engine'

/**
 * Sync status for the UI. Read-only — writes go through the engine directly.
 *
 * There is deliberately no debouncing or "settling" here any more. That existed
 * to stop a status *line* from appearing and disappearing on every write, which
 * moved the layout underneath the record buttons. The status now lives in a
 * permanent header icon (`SyncIndicator`), and an icon changing shape costs
 * nothing and hides nothing — so the state can be reported as it actually is.
 */
export const useSyncStore = defineStore('sync', () => {
  /** The full status, in words. Shown on the sync screen and in the toast. */
  const detail = computed(() => {
    switch (sync.state.value) {
      case 'syncing':
        return 'Syncing…'
      case 'offline':
        return 'Offline — retrying in the background'
      case 'error':
        return sync.error.value ?? 'Sync error'
      default:
        return sync.pending.value > 0 ? `${sync.pending.value} waiting to send` : 'Up to date'
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
    detail,
    syncNow: () => sync.sync(),
  }
})
