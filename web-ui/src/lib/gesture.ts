import { useEventListener } from '@vueuse/core'
import type { MaybeRefOrGetter } from 'vue'

/**
 * Claim the `touchmove`s of a gesture the app drags itself, so the browser
 * never treats it as a scroll — **including the fling at its end**.
 *
 * `touch-action: none` is not enough on Android Chrome, and the difference is
 * the bug this exists for. It stops the page scrolling, but Chrome's gesture
 * detector still turns a quick swipe into a fling, and a fling in progress
 * spends the next tap on stopping itself: the tap arrives as
 * `pointerdown`/`pointerup` with **no `click`**. On a phone that read as
 * "after swiping, everything needs two taps" — worse the faster the swipe,
 * because a faster swipe is a longer fling. Measured on Android Chrome: a tap
 * 120–350ms after a fast swipe on the balance row lost its click; the same tap
 * after a slow swipe, or 900ms later, did not.
 *
 * A touch sequence whose moves the page has consumed (`preventDefault` on
 * `touchmove`) is never handed to the scroller at all, so no fling starts and
 * the next tap is an ordinary tap. Taps themselves are unaffected: the click
 * comes from the `touchend`, which is left alone.
 *
 * `claim` decides per event: an element whose drags are always the app's
 * (`touch-action: none`) claims every move of a press; a pager inside a
 * vertically scrolling list claims only once the gesture is clearly sideways.
 * The listener is non-passive by necessity — a passive one may not cancel.
 */
export function useClaimTouchMoves(
  target: MaybeRefOrGetter<HTMLElement | null | undefined>,
  claim: () => boolean,
) {
  useEventListener(
    target,
    'touchmove',
    (e: TouchEvent) => {
      if (e.cancelable && claim()) e.preventDefault()
    },
    { passive: false },
  )
}
