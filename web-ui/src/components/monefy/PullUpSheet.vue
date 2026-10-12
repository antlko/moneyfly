<script setup lang="ts">
import { useEventListener } from '@vueuse/core'
import { computed, onScopeDispose, ref, useTemplateRef, watch } from 'vue'

import {
  dragSlop,
  releaseVelocity,
  settleSheet,
  VELOCITY_WINDOW_MS,
  type Sample,
} from '@/lib/sheet'

/**
 * A panel docked at the bottom of its box with only its handle showing, which
 * is pulled up to cover the box and pushed back down to dock again.
 *
 * This is the dashboard's donut ↔ list switch. The two used to be separate
 * layouts that cross-faded, with the balance row jumping from the bottom of the
 * screen to the top; now the list *is* the balance row's sheet. Docked, the
 * row sits over the record buttons with the donut above it; pulled up, it sits
 * under the carousel with the categories below it — the two layouts are the two
 * resting positions of one thing, and the motion between them is the finger's.
 *
 *   * `behind` — what the sheet covers (the donut). It recedes a little as the
 *     sheet rises, so the eye reads one layer passing over another.
 *   * `handle` — what shows when docked and what the finger holds (the balance
 *     row). Taps on it go to its buttons; a drag moves the sheet.
 *   * default — the sheet's body (the category list). A drag on it moves the
 *     sheet too, once the list has nothing left to scroll that way.
 *   * `footer` — under the box (the record buttons). Also a grip: "swipe up
 *     from the bottom" is the instinct, and the bottom is where the buttons are.
 *
 * `open` is a v-model: a drag decides it on release, and a change from outside
 * (the `≡` button, a sync from another device) animates the same travel.
 */
const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [boolean] }>()

/**
 * The handle's height, as a length rather than a measurement.
 *
 * The docked position is `100% − handle`, and both ends of the travel are
 * expressed in CSS for exactly that reason: measured pixels are zero until a
 * ResizeObserver reports, so the first frame would put the sheet in the wrong
 * place — the list flashing open on every launch. Only a drag needs pixels,
 * and by then there is a layout to read them from.
 */
const HANDLE_H = '3.5rem'

const stage = useTemplateRef<HTMLElement>('stage')
const sheet = useTemplateRef<HTMLElement>('sheet')
const handle = useTemplateRef<HTMLElement>('handle')
const body = useTemplateRef<HTMLElement>('body')

/** 0 docked, 1 open — the target while animating, the finger's while dragging. */
const progress = ref(props.open ? 1 : 0)
/** While a finger holds the sheet it follows exactly, with no easing to lag behind. */
const dragging = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!dragging.value) progress.value = open ? 1 : 0
  },
)

const easing = 'var(--mf-duration-panel) var(--mf-ease-panel)'

/**
 * Highest mid-travel, zero at either end: what the sheet looks like *while it
 * is a sheet*. Rounded top corners and an edge shadow while it moves, none of
 * either at rest — so docked is pixel-for-pixel the donut screen and open is
 * pixel-for-pixel the list.
 */
const lift = computed(() => 4 * progress.value * (1 - progress.value))

const sheetStyle = computed(() => ({
  transform: `translateY(calc((100% - ${HANDLE_H}) * ${1 - progress.value}))`,
  borderTopLeftRadius: `${16 * lift.value}px`,
  borderTopRightRadius: `${16 * lift.value}px`,
  transition: dragging.value ? 'none' : `transform ${easing}, border-radius ${easing}`,
}))

const behindStyle = computed(() => ({
  bottom: HANDLE_H,
  // `none` at rest rather than `scale(1)`: any transform makes this layer the
  // containing block for fixed-position descendants, which nothing here should
  // have to know about while nothing is moving.
  transform: progress.value === 0 ? 'none' : `scale(${1 - 0.06 * progress.value})`,
  opacity: 1 - 0.5 * progress.value,
  transition: dragging.value ? 'none' : `transform ${easing}, opacity ${easing}`,
}))

const shadowStyle = computed(() => ({
  opacity: lift.value,
  transition: dragging.value ? 'none' : `opacity ${easing}`,
}))

/*
 * The gesture, in one place, fed from three grips: the handle, the footer
 * (pointer events) and the body (touch events — see below).
 *
 * **Why the body and the footer are grips at all.** A swipe that the browser is
 * allowed to treat as a pan, and that then has nothing to scroll, is not
 * harmless on a phone. WebKit counts it as a pan that is still settling and
 * spends the *next tap, anywhere in the app,* on stopping it — the same trap
 * CLAUDE.md records for `touch-action` on the document root. The list is a
 * `pan-y` region and the record buttons are `manipulation` (which allows
 * panning), so swiping down on an open list already at its top — the natural
 * way to close a sheet — or swiping up from the buttons, was exactly that: a
 * pan nothing consumed, and the next tap gone. Every vertical swipe in this
 * area now belongs either to a real scroll or to the sheet.
 *
 * Same rules as every other gesture in the app (SwipePager, RecordsSheet):
 * remember which pointer pressed, ignore a mouse that moves with no button
 * held, and never act on a gesture that was not ours.
 */
let startY = 0
/** Progress when the drag took hold — the sheet's *actual* position then. */
let base = 0
/** The resting state the gesture is measured against. */
let from: 0 | 1 = 0
let travel = 1
let samples: Sample[] = []
/**
 * Until when a click counts as the one a drag just ended with. A time window,
 * not a flag: a flag left set by a drag that produced no click (touch usually
 * does not) swallowed the next *honest* activation — a keyboard Enter on `≡`,
 * say — which is the "everything needs two taps" bug in another form.
 */
let swallowClicksUntil = 0
const CLICK_AFTER_DRAG_MS = 400

const clamp01 = (v: number) => Math.max(0, Math.min(1, v))

/**
 * Where the sheet is on screen right now, as progress.
 *
 * Not `progress.value`: during an animation that is the destination, and a
 * finger catching the sheet mid-flight has to take it from where it *is*, or it
 * jumps to the end of the animation and then follows from there.
 */
function visualProgress(): number {
  const el = sheet.value
  if (!el) return progress.value
  const ty = new DOMMatrixReadOnly(getComputedStyle(el).transform).m42
  return clamp01(1 - ty / travel)
}

/** A press became a drag: take the sheet from wherever it is. */
function beginDrag(pressY: number, t: number) {
  travel = Math.max(1, (stage.value?.clientHeight ?? 1) - (handle.value?.offsetHeight ?? 0))
  // From where the sheet *is*, and measured from where the press began, so the
  // slop is caught up at once rather than lagged forever.
  base = visualProgress()
  from = props.open ? 1 : 0
  startY = pressY
  samples = [{ t, y: pressY }]
  dragging.value = true
}

function moveDrag(y: number, t: number) {
  progress.value = clamp01(base - (y - startY) / travel)
  samples.push({ t, y })
  // Only the last window matters for the release speed; keep the array short.
  while (samples.length > 2 && t - samples[0]!.t > VELOCITY_WINDOW_MS) samples.shift()
}

function settle(velocity: number, endY = startY) {
  const target = settleSheet(from, progress.value, velocity, startY - endY)
  dragging.value = false
  progress.value = target
  swallowClicksUntil = performance.now() + CLICK_AFTER_DRAG_MS
  if ((target === 1) !== props.open) emit('update:open', target === 1)
}

/* --- Handle and footer: pointer events ------------------------------------ */

/*
 * The press starts on the grip and is then **followed on `window`**. A finger
 * is implicitly captured by what it landed on, so its events would arrive
 * either way — but a mouse is not, and the handle is a 56px strip: a quick drag
 * leaves it on the first move. Pointer capture would fix that and break
 * something worse — while an element holds capture, the click that ends a
 * *tap* is retargeted to it rather than to the `≡` or the pill that was pressed
 * (see SwipePager). Listening on `window` for the length of one press needs
 * neither.
 */
let pointer: number | null = null
let pressY = 0
let pressType = 'mouse'
/** Window listeners for the press in progress, removed when it ends. */
let unlisten: (() => void) | null = null

function onDown(e: PointerEvent) {
  if (!e.isPrimary) return
  if (e.pointerType !== 'touch' && e.button !== 0) return
  // A press whose release never arrived (focus lost mid-drag) must not leave
  // the grip deaf: the new press replaces it.
  if (unlisten) {
    unlisten()
    unlisten = null
    if (dragging.value) settle(0)
  }
  pointer = e.pointerId
  pressY = e.clientY
  pressType = e.pointerType
  window.addEventListener('pointermove', onMove)
  window.addEventListener('pointerup', onUp)
  window.addEventListener('pointercancel', onCancel)
  unlisten = () => {
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    window.removeEventListener('pointercancel', onCancel)
  }
}
onScopeDispose(() => unlisten?.())

function onMove(e: PointerEvent) {
  if (pointer !== e.pointerId) return
  // A mouse button let go outside the window never sends `pointerup`.
  if (e.pointerType !== 'touch' && e.buttons === 0) {
    onCancel(e)
    return
  }
  if (!dragging.value) {
    if (Math.abs(e.clientY - pressY) < dragSlop(pressType)) return
    beginDrag(pressY, e.timeStamp)
  }
  moveDrag(e.clientY, e.timeStamp)
}

/** End the press, whatever it was; true when it had become a drag. */
function endGesture(e: PointerEvent): boolean {
  if (pointer !== e.pointerId) return false
  pointer = null
  unlisten?.()
  unlisten = null
  return dragging.value
}

function onUp(e: PointerEvent) {
  if (endGesture(e)) settle(releaseVelocity(samples, e.timeStamp), e.clientY)
}

/** A cancelled drag still lands somewhere sensible: decided on distance alone. */
function onCancel(e: PointerEvent) {
  if (endGesture(e)) settle(0)
}

/* --- Body: touch events ---------------------------------------------------- */

/*
 * Touch, not pointer, events — because only a `touchmove` can be cancelled.
 * The list is `pan-y` so that it scrolls; once the browser starts a pan,
 * pointer events just get a `pointercancel`. Calling `preventDefault` on the
 * first `touchmove` is the one way to say "this vertical move is mine" before
 * the browser has decided otherwise. Which side gets the move:
 *
 *   * the list, while it can still scroll that way — a real scroll, which
 *     consumes the pan;
 *   * otherwise the sheet — down at the top closes it; up at the bottom has
 *     nowhere to go, and is taken anyway so it is never a pan with nothing
 *     under it;
 *   * horizontal moves are the month pager's, untouched.
 */
let touchId: number | null = null
let touchX = 0
let touchY = 0
let touchMode: 'undecided' | 'sheet' | 'native' = 'undecided'
let scroller: HTMLElement | null = null

/** The nearest element between the touch and the body that actually scrolls. */
function scrollerFrom(target: EventTarget | null): HTMLElement | null {
  for (let el = target as HTMLElement | null; el && el !== body.value; el = el.parentElement) {
    const overflow = getComputedStyle(el).overflowY
    if ((overflow === 'auto' || overflow === 'scroll') && el.scrollHeight > el.clientHeight + 1) {
      return el
    }
  }
  return null
}

const ownTouch = (e: TouchEvent) => [...e.changedTouches].find((t) => t.identifier === touchId)

function onBodyTouchStart(e: TouchEvent) {
  const t = e.touches[0]
  if (e.touches.length !== 1 || !t || dragging.value) {
    touchId = null
    return
  }
  touchId = t.identifier
  touchX = t.clientX
  touchY = t.clientY
  touchMode = 'undecided'
  scroller = scrollerFrom(e.target)
}

function onBodyTouchMove(e: TouchEvent) {
  const t = touchId === null ? undefined : ownTouch(e)
  if (!t || touchMode === 'native') return
  const dx = t.clientX - touchX
  const dy = t.clientY - touchY

  if (touchMode === 'undecided') {
    if (Math.abs(dx) > Math.abs(dy)) {
      if (Math.abs(dx) >= dragSlop('touch')) touchMode = 'native'
      return
    }
    const atTop = !scroller || scroller.scrollTop <= 0
    const atBottom =
      !scroller || scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 1
    if ((dy > 0 && !atTop) || (dy < 0 && !atBottom)) {
      if (Math.abs(dy) >= dragSlop('touch')) touchMode = 'native'
      return
    }
    // Ours — and claimed now, before the browser starts a pan of its own.
    if (e.cancelable) e.preventDefault()
    if (Math.abs(dy) < dragSlop('touch')) return
    touchMode = 'sheet'
    beginDrag(touchY, e.timeStamp)
  }

  if (e.cancelable) e.preventDefault()
  moveDrag(t.clientY, e.timeStamp)
}

function onBodyTouchEnd(e: TouchEvent) {
  const t = touchId === null ? undefined : ownTouch(e)
  if (!t) return
  touchId = null
  if (touchMode !== 'sheet') return
  if (e.type === 'touchcancel') settle(0)
  else settle(releaseVelocity(samples, e.timeStamp), t.clientY)
}

// Non-passive, explicitly: a passive listener may not cancel the move.
useEventListener(body, 'touchstart', onBodyTouchStart, { passive: true })
useEventListener(body, 'touchmove', onBodyTouchMove, { passive: false })
useEventListener(body, ['touchend', 'touchcancel'], onBodyTouchEnd, { passive: true })

/**
 * Swallow the click a drag ends with.
 *
 * In the capture phase, so it reaches no button in the grip: a drag that
 * happens to end over `≡` must not then toggle the view straight back. Only
 * within a moment of the release (see `swallowClicksUntil`), so nothing later
 * is ever eaten.
 */
function onClickCapture(e: MouseEvent) {
  if (performance.now() > swallowClicksUntil) return
  swallowClicksUntil = 0
  e.stopPropagation()
  e.preventDefault()
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div ref="stage" class="relative min-h-0 flex-1 overflow-hidden">
      <div
        class="absolute inset-x-0 top-0 flex origin-[50%_40%] flex-col"
        :style="behindStyle"
        :inert="progress === 1 && !dragging"
      >
        <slot name="behind" />
      </div>

      <section ref="sheet" class="absolute inset-0 flex flex-col bg-mf-bg" :style="sheetStyle">
        <!-- The edge of the sheet, visible only while it travels over the donut. -->
        <div
          aria-hidden="true"
          class="pointer-events-none absolute inset-x-0 bottom-full h-6 bg-linear-to-t from-mf-ink/12 to-transparent"
          :style="shadowStyle"
        />

        <div
          ref="handle"
          class="flex shrink-0 touch-none items-center"
          :style="{ height: HANDLE_H }"
          @pointerdown="onDown"
          @click.capture="onClickCapture"
        >
          <slot name="handle" />
        </div>

        <div
          ref="body"
          class="flex min-h-0 flex-1 flex-col"
          :inert="progress === 0 && !dragging"
          @click.capture="onClickCapture"
        >
          <slot />
        </div>
      </section>
    </div>

    <!--
      `touch-none` here too, deliberately: the buttons below are
      `manipulation`, which lets a vertical swipe become a pan with nothing to
      scroll — the tap-eating kind (see the gesture notes above). As a grip, the
      swipe opens or closes the sheet instead. Taps are untouched.
    -->
    <div class="shrink-0 touch-none" @pointerdown="onDown" @click.capture="onClickCapture">
      <slot name="footer" />
    </div>
  </div>
</template>
