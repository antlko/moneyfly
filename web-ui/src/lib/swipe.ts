import { onScopeDispose, type Ref, watch } from 'vue'

interface SwipeOptions {
  onLeft: () => void
  onRight: () => void
  /** Horizontal travel, in px, before it counts. */
  threshold?: number
}

/**
 * Horizontal swipe on an element.
 *
 * Pointer events rather than touch events, so it works with a mouse drag on
 * desktop and a trackpad too — the month carousel should not be phone-only.
 *
 * A gesture is rejected unless it is clearly horizontal (`|dx| > |dy|`);
 * otherwise every attempt to scroll the category list past the donut would
 * change the month instead, which is exactly the kind of thing that makes an app
 * feel hostile.
 */
export function useHorizontalSwipe(target: Ref<HTMLElement | null>, options: SwipeOptions) {
  const threshold = options.threshold ?? 50
  let startX = 0
  let startY = 0
  let tracking = false

  const onPointerDown = (e: PointerEvent) => {
    if (!e.isPrimary) return
    startX = e.clientX
    startY = e.clientY
    tracking = true
  }

  const onPointerUp = (e: PointerEvent) => {
    if (!tracking) return
    tracking = false
    const dx = e.clientX - startX
    const dy = e.clientY - startY
    if (Math.abs(dx) < threshold || Math.abs(dx) <= Math.abs(dy)) return
    if (dx < 0) options.onLeft()
    else options.onRight()
  }

  const attach = (el: HTMLElement | null) => {
    if (!el) return
    el.addEventListener('pointerdown', onPointerDown)
    el.addEventListener('pointerup', onPointerUp)
    el.addEventListener('pointercancel', () => (tracking = false))
  }

  const detach = (el: HTMLElement | null) => {
    if (!el) return
    el.removeEventListener('pointerdown', onPointerDown)
    el.removeEventListener('pointerup', onPointerUp)
  }

  watch(target, (el, previous) => {
    detach(previous ?? null)
    attach(el)
  })
  onScopeDispose(() => detach(target.value))
}
