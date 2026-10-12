import { defineStore } from 'pinia'
import { ref } from 'vue'

/** A record that was just saved, as the dashboard should celebrate it. */
export interface Burst {
  kind: 'expense' | 'income'
  /** The category's icon and palette key, as stored on the row. */
  icon: unknown
  color: unknown
  /** Signed minor units, as stored — negative for an expense. */
  amountMinor: number
  currency: string
}

/**
 * The flourish a new record gets, handed from the record screen to the
 * dashboard across the route change.
 *
 * Neither can do it alone. The record screen is navigating away in the same
 * breath as it saves, so anything it plays is gone with it; and the dashboard
 * cannot tell a record is *new* — it only sees its rows change, which a
 * background sync from another device does too, and that must not set anything
 * off. So the record screen leaves a note here and the dashboard takes it, once,
 * as it mounts.
 *
 * An edit leaves no note: correcting a typo is not an occasion.
 */
export const useRecordBurstStore = defineStore('recordBurst', () => {
  const pending = ref<Burst | null>(null)

  const announce = (burst: Burst) => {
    pending.value = burst
  }

  /** Read and clear — a burst plays on the next dashboard, never on a later one. */
  function take(): Burst | null {
    const burst = pending.value
    pending.value = null
    return burst
  }

  return { announce, take }
})
