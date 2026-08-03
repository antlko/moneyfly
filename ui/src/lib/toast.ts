import { ref } from 'vue'

/**
 * Toasts carry save outcomes, including the Undo affordance behind an optimistic
 * save. They are announced with aria-live, per docs/08-ux.md §8.11.
 */
export interface Toast {
  id: number
  message: string
  tone: 'success' | 'error' | 'info'
  action?: { label: string; run: () => void | Promise<void> }
}

const toasts = ref<Toast[]>([])
let nextID = 1

export function useToasts() {
  return { toasts }
}

export function pushToast(
  message: string,
  tone: Toast['tone'] = 'info',
  action?: Toast['action'],
  ttlMs = 6000,
): number {
  const id = nextID++
  toasts.value = [...toasts.value, { id, message, tone, action }]
  window.setTimeout(() => dismissToast(id), ttlMs)
  return id
}

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}
