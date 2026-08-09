import { defineStore } from 'pinia'
import { ref } from 'vue'

/**
 * What the app has to tell you, and where it is allowed to say it.
 *
 * This replaces the bottom-centre toasts. Two things were wrong with those: they
 * had no close button, so a message sat over the record buttons until it chose
 * to leave, and an *error* was shown the same fleeting way as a confirmation —
 * so the one message you needed to read and act on was the one most likely to
 * disappear before you had.
 *
 * The split is deliberate and per form factor:
 *
 *   * **Errors are shown on desktop only**, as a dismissible alert that stays
 *     until it is closed. A phone has nowhere to put one that does not cover the
 *     thing you were doing, and every screen that can fail already renders its
 *     own inline message next to the control that failed.
 *   * **Undo is shown everywhere**, because it is the only notification with a
 *     deadline — a deleted record is unrecoverable once it is gone, and that is
 *     true on a phone as much as a desktop.
 *
 * Anything that was purely informational ("Copied to clipboard", "Record
 * updated") is not here at all. The screen already shows the result.
 */
export type NoticeKind = 'error' | 'undo'

export interface Notice {
  id: number
  kind: NoticeKind
  message: string
  /** Present on `undo` notices; running it dismisses the notice. */
  action?: { label: string; run: () => void }
}

/** How long an undo stays offered. Errors never expire on their own. */
const UNDO_MS = 8000

let nextId = 1

export const useNotifyStore = defineStore('notify', () => {
  const notices = ref<Notice[]>([])

  function dismiss(id: number) {
    notices.value = notices.value.filter((n) => n.id !== id)
  }

  function push(notice: Omit<Notice, 'id'>, ttl?: number): number {
    const id = nextId++
    notices.value = [...notices.value, { ...notice, id }]
    if (ttl) setTimeout(() => dismiss(id), ttl)
    return id
  }

  /**
   * Report a failure. Stays until dismissed — an error you did not read is an
   * error you will hit again.
   */
  function error(message: string) {
    // Don't stack the same message twice; a retry loop would otherwise bury the
    // screen under identical alerts.
    if (notices.value.some((n) => n.kind === 'error' && n.message === message)) return
    push({ kind: 'error', message })
  }

  /** Offer to undo something destructive, for a few seconds. */
  function undo(message: string, run: () => void) {
    const id = push({ kind: 'undo', message, action: { label: 'Undo', run } }, UNDO_MS)
    const notice = notices.value.find((n) => n.id === id)
    if (notice?.action) {
      const inner = notice.action.run
      notice.action.run = () => {
        inner()
        dismiss(id)
      }
    }
  }

  /** Turn any thrown value into a sentence worth showing. */
  function fromError(e: unknown, fallback: string) {
    error(e instanceof Error && e.message ? e.message : fallback)
  }

  return { notices, error, undo, fromError, dismiss }
})
