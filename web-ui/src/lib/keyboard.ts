import { useEventListener, useMediaQuery } from '@vueuse/core'
import { computed, ref, watch, type ComputedRef } from 'vue'

/**
 * How much shorter than its tallest the visual viewport has to get before that
 * is an on-screen keyboard rather than browser chrome moving.
 *
 * A URL bar collapsing or a tab strip appearing moves it by well under 100px;
 * the smallest phone keyboard is more than twice that.
 */
export const KEYBOARD_MIN_PX = 150

/** Whether a viewport this much shorter than its tallest is hiding a keyboard. */
export function viewportHidesKeyboard(tallest: number, current: number): boolean {
  return tallest - current >= KEYBOARD_MIN_PX
}

/** How long after a field takes focus a closing keyboard is not a reason to blur it. */
const REFOCUS_GRACE_MS = 400

/** Input types a phone answers with its own keyboard, rather than a picker. */
const TYPED = new Set(['text', 'search', 'email', 'tel', 'url', 'number', 'password'])

/**
 * Whether focusing this element raises the system keyboard.
 *
 * Duck-typed rather than `instanceof HTMLInputElement` so it can be tested
 * without a DOM. A date input or a select opens a picker, not a keyboard, and
 * must not move anything.
 */
export function opensKeyboard(
  el: { tagName?: string; type?: string; isContentEditable?: boolean } | null,
): boolean {
  if (!el) return false
  if (el.isContentEditable) return true
  const tag = el.tagName?.toUpperCase()
  if (tag === 'TEXTAREA') return true
  return tag === 'INPUT' && TYPED.has((el.type ?? 'text').toLowerCase())
}

/**
 * Whether the system keyboard is up — or about to be — on a screen that has the
 * app's own keypad.
 *
 * The record and transfer screens fill the viewport exactly: a keypad pinned to
 * the bottom of a `flex-1` column. Android (and any browser honouring
 * `interactive-widget=resizes-content`) shrinks that viewport when the keyboard
 * opens for the note, the column runs out of height, and the keypad — anchored
 * `justify-end` — overflows *upwards*, straight over the amount and the note
 * being typed. The answer is not to squeeze it: two keyboards on one screen is
 * one too many, so the app's own steps aside while the system's is up.
 *
 * Two signals, because neither is enough alone:
 *
 *   * **Focus** on a text field says the keyboard is coming, before the
 *     viewport has moved — hiding then, not a frame after the resize, is what
 *     keeps the overflow from ever being drawn.
 *   * **The viewport** says when it has actually gone. Bringing the keypad back
 *     on `blur` alone puts it into a column that is still keyboard-short, for as
 *     long as the keyboard takes to slide away — the same overlap, for 250ms.
 *
 * And one rescue: Android's back gesture and the keyboard's own hide key close
 * the keyboard **without blurring the field**. Focus would then keep the keypad
 * hidden under a screen with no keyboard on it, so a viewport that grows back
 * while a field still has focus blurs that field.
 *
 * Touch only. A desktop has a physical keyboard and room for both, and a window
 * resized by a docked devtools panel must not take the keypad with it.
 */
export function useSystemKeyboard(): ComputedRef<boolean> {
  const touch = useMediaQuery('(pointer: coarse)')
  const typing = ref(opensKeyboard(document.activeElement as HTMLElement | null))
  const shrunk = ref(false)

  /** When a field last took focus — see the rescue below. */
  let focusedAt = -Infinity

  const syncFocus = () => {
    typing.value = opensKeyboard(document.activeElement as HTMLElement | null)
  }
  useEventListener(document, 'focusin', () => {
    focusedAt = performance.now()
    syncFocus()
  })
  // `focusout` fires while the old element still holds focus; read the result
  // a task later, once the browser has decided where focus went.
  useEventListener(document, 'focusout', () => setTimeout(syncFocus))

  const vv = window.visualViewport
  if (vv) {
    let tallest = vv.height
    let width = vv.width
    useEventListener(vv, 'resize', () => {
      // A rotation is a different screen, not a keyboard: measure afresh.
      if (Math.abs(vv.width - width) > 1) {
        width = vv.width
        tallest = vv.height
      }
      tallest = Math.max(tallest, vv.height)
      shrunk.value = viewportHidesKeyboard(tallest, vv.height)
    })
  }

  /*
   * The rescue: the keyboard went, the field kept focus — blur it.
   *
   * Except straight after a fresh focus. Tapping the note again while the
   * keyboard is still on its way down can deliver the "it grew" resize *after*
   * the tap, and blurring then would throw away the keyboard the person just
   * asked for. A keyboard closed by hand is never that quick off the mark.
   */
  watch(shrunk, (now, before) => {
    if (!before || now || !touch.value) return
    if (performance.now() - focusedAt < REFOCUS_GRACE_MS) return
    const el = document.activeElement as HTMLElement | null
    if (opensKeyboard(el)) el?.blur()
  })

  return computed(() => touch.value && (typing.value || shrunk.value))
}

/**
 * For a field's `@keydown.enter`: the return key on a one-line field means
 * "done", and done means the keyboard goes and the keypad comes back.
 */
export function dismissKeyboard(e: Event) {
  ;(e.target as HTMLElement | null)?.blur()
}

/**
 * A `<Transition>` `@leave` hook that finishes on the spot, for the keypad
 * stepping aside.
 *
 * Without it, a `v-show` inside a `<Transition>` that has no leave CSS still
 * waits two animation frames before applying `display: none` — Vue has to look
 * for a transition before it can know there is none. That is two frames in
 * which the keypad is still in the column the keyboard is about to shrink.
 *
 * The **two parameters are load-bearing**: Vue only hands over control of
 * `done` to a hook that declares it (`hook.length > 1`).
 */
export function leaveAtOnce(_el: Element, done: () => void) {
  done()
}

/**
 * Focus a dialog's first field when it opens — with a mouse and keyboard.
 *
 * On a desktop, opening "Add a currency" and typing "HUF" should search; making
 * someone click the box first is a step nobody expects. On a phone the same
 * focus would throw the keyboard over the very list being searched, before
 * anyone asked to type, so touch is left alone. (The `autofocus` attribute is
 * no substitute: a browser honours it once per page, not once per dialog.)
 */
export function focusUnlessTouch(el: HTMLElement | null | undefined) {
  if (!el || window.matchMedia('(pointer: coarse)').matches) return
  el.focus()
}
