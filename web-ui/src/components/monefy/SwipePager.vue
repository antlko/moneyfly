<script setup lang="ts">
import { nextTick, onScopeDispose, ref } from 'vue'

const props = withDefaults(
  defineProps<{
    disabled?: boolean
    /**
     * Whether the content inside can scroll vertically.
     *
     * It decides `touch-action`, and that decision is the difference between a
     * swipe that works on a phone and one that jitters and springs back. See the
     * note on `touch-action` below.
     */
    verticalScroll?: boolean
  }>(),
  { disabled: false, verticalScroll: false },
)
const emit = defineEmits<{ prev: []; next: [] }>()

/**
 * Horizontal pager for the period carousel.
 *
 * The content follows the finger and animates the rest of the way on release,
 * or springs back when the gesture was too short. Changing the period on
 * pointerup with no movement in between technically works, but it reads as a
 * bug — the screen contents change with no indication that anything was
 * dragged.
 *
 * **`touch-action` is set to exactly what the content needs, and no more.**
 *
 * `pan-y` says "the browser may pan this vertically", and iOS takes that
 * seriously: it withholds judgement at the start of every gesture and, the
 * moment a real finger's inevitable vertical wobble looks like the beginning of
 * a scroll, it claims the gesture and sends `pointercancel`. On screen that is a
 * swipe that follows the finger for a few pixels and then lets go — which is
 * exactly how it was described, and which never happens with a mouse because a
 * mouse drag has no ambiguity to resolve.
 *
 * So `pan-y` is used only when something inside actually scrolls (the category
 * list). Over the donut, where nothing does, the value is `none`: the gesture is
 * ours from the first pixel and there is nothing for iOS to arbitrate.
 *
 * A cancellation that arrives anyway is honoured rather than discarded — see
 * `onPointerCancel`.
 *
 * **A pointer gesture must track which pointer is down, and stay unconvinced
 * until it is.** A mouse emits `pointermove` while merely hovering, so handlers
 * that only look at coordinates treat crossing the window as a drag: the content
 * follows the cursor, `setPointerCapture` runs with no button held — which then
 * steals every click from the children — and the release flips the period. That
 * bug shipped once; the `activePointer` bookkeeping below is what prevents it.
 *
 * **And a pointer gesture must not believe every `lostpointercapture` it sees.**
 * Touch pointers are implicitly captured by the element they land on, so taking
 * capture here revokes a descendant's — which reports it, and the event bubbles.
 * See `onLostCapture`: getting that wrong is invisible with a mouse and breaks
 * the gesture entirely on a phone.
 */
const SLIDE_MS = 190
/** Fraction of the width that commits the gesture. */
const COMMIT_RATIO = 0.22
/** …but never more than this, so a wide desktop window is not a workout. */
const COMMIT_MAX_PX = 110

const root = ref<HTMLElement | null>(null)
const offset = ref(0)
const transition = ref('')
const busy = ref(false)

/** The pointer whose `pointerdown` we saw — `null` means nothing is pressed. */
let activePointer: number | null = null
let startX = 0
let startY = 0
let width = 1
let axis: 'undecided' | 'x' | 'y' = 'undecided'

/** True only for events belonging to a gesture that actually started here. */
function tracking(e: PointerEvent) {
  return activePointer === e.pointerId
}

function reset() {
  activePointer = null
  axis = 'undecided'
}

/** `releasePointerCapture` throws when this element never held it. */
function releaseCapture(e: PointerEvent) {
  const el = root.value
  if (el?.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId)
}

function onPointerDown(e: PointerEvent) {
  if (!e.isPrimary || busy.value || props.disabled) return
  // `button === 0` is the primary button for mouse and pen alike; a right-click
  // or a middle-click must not arm the pager.
  if (e.pointerType !== 'touch' && e.button !== 0) return
  activePointer = e.pointerId
  startX = e.clientX
  startY = e.clientY
  width = root.value?.clientWidth ?? 1
  axis = 'undecided'
  transition.value = ''
}

function onPointerMove(e: PointerEvent) {
  if (!tracking(e) || busy.value || props.disabled || axis === 'y') return
  // A mouse that let go outside the window never delivers `pointerup`; its next
  // move arrives with no buttons held, and that is the moment to forget it.
  if (e.pointerType !== 'touch' && e.buttons === 0) {
    onPointerCancel(e)
    return
  }

  const dx = e.clientX - startX
  const dy = e.clientY - startY

  if (axis === 'undecided') {
    // Wait until the gesture has committed to a direction. Deciding on the very
    // first move would steal the start of every vertical scroll — including the
    // upward drag on the balance pill that opens the records list.
    if (Math.abs(dx) < 8 && Math.abs(dy) < 8) return
    axis = Math.abs(dx) > Math.abs(dy) ? 'x' : 'y'
    if (axis === 'y') return
    // Capture the *mouse*, never the finger.
    //
    // A mouse that leaves this element mid-drag stops delivering events, so it
    // genuinely needs capturing. A touch pointer does not: the browser already
    // captured it implicitly to whatever it landed on, and those events bubble
    // up here regardless of what ends up under the finger.
    //
    // Taking it anyway is not free. While an element holds capture, the events
    // that follow — including the next tap's — are retargeted to it rather than
    // to what was actually pressed, which is why a button pressed straight after
    // a swipe appeared to do nothing until it was pressed a second time. It is
    // the same trap that made `lostpointercapture` cancel every swipe, and the
    // same one already removed from `BalancePill`.
    if (e.pointerType !== 'touch') root.value?.setPointerCapture(e.pointerId)
  }
  offset.value = dx
}

async function onPointerUp(e: PointerEvent) {
  if (!tracking(e)) return

  // Read and clear the gesture BEFORE releasing capture, and in that order.
  //
  // `releasePointerCapture` fires `lostpointercapture`, which Chrome dispatches
  // synchronously from inside that call — so releasing first re-entered
  // `onPointerCancel`, which reset `axis` and sprang the content back, and by
  // the time control returned here `wasDrag` was already false. The effect was a
  // pager that never changed period on a real pointer, while every synthetic
  // drag in a test still passed, because synthetic events never take capture.
  //
  // Clearing `activePointer` first makes the re-entry a no-op whichever order
  // the browser chooses, so this no longer depends on that detail at all.
  const wasDrag = axis === 'x'
  reset()
  releaseCapture(e)

  if (!wasDrag || busy.value || props.disabled) return

  await settle(e.clientX - startX)
}

/**
 * Finish a gesture from however far it got: commit past the threshold, spring
 * back below it.
 */
async function settle(dx: number) {
  const threshold = Math.min(width * COMMIT_RATIO, COMMIT_MAX_PX)
  if (Math.abs(dx) < threshold) {
    await animateTo(0)
    transition.value = ''
    return
  }
  await commit(dx < 0 ? 1 : -1)
}

/**
 * A cancelled gesture is finished on its merits, not thrown away.
 *
 * `pointercancel` does not mean "the user changed their mind" — it means the
 * browser has decided to handle the gesture itself, and it can arrive halfway
 * through a perfectly deliberate swipe. Springing back unconditionally is what
 * produced the complaint that the swipe "jitters, as if it gets released": the
 * content follows the finger, iOS claims the gesture, and everything snaps home
 * with the period unchanged.
 *
 * Past the commit threshold the intent is not in doubt, so it is honoured. Below
 * it, springing back is still right.
 */
function onPointerCancel(e: PointerEvent) {
  if (!tracking(e)) return
  const wasDrag = axis === 'x'
  const dx = offset.value
  reset()
  releaseCapture(e)
  if (wasDrag) void settle(dx)
}

/**
 * Losing capture ends the gesture — but only when it was *ours* to lose.
 *
 * This is the whole reason swiping worked with a mouse and did nothing under
 * touch. A touch pointer is **implicitly captured** by whatever element it lands
 * on, so the moment this component calls `setPointerCapture` on itself, that
 * descendant loses its implicit capture and fires `lostpointercapture` — which
 * bubbles, straight back into this handler, one event after the drag began.
 * Every swipe cancelled itself on the frame it started. A mouse takes no
 * implicit capture, so nothing fired and nothing looked wrong.
 *
 * `e.target === root` is the whole fix: an ancestor taking over says nothing
 * about our gesture, and the browser revoking *our* capture ends it.
 */
function onLostCapture(e: PointerEvent) {
  if (e.target !== root.value) return
  onPointerCancel(e)
}

/**
 * Slide the current content out, swap it, slide the new content in.
 *
 * The ordering here is the whole trick, and getting it wrong produces a very
 * specific glitch: the new period appears to fly in from the side it just left.
 *
 * Vue applies reactive changes asynchronously, so setting the offset to the
 * incoming side and then immediately animating to zero collapses into a single
 * flush — the browser never sees the jump, only a transition from wherever the
 * element already was. The content change and the reposition therefore have to
 * be flushed together (`nextTick`), and a layout read forced afterwards, before
 * the slide-in is allowed to start.
 */
async function commit(direction: 1 | -1) {
  busy.value = true
  try {
    await animateTo(direction === 1 ? -width : width)

    // Swap the content and park it on the incoming side in the same flush.
    transition.value = ''
    offset.value = direction === 1 ? width : -width
    if (direction === 1) emit('next')
    else emit('prev')
    await nextTick()

    // Force layout so the next change is a transition, not part of that flush.
    void root.value?.offsetHeight

    await animateTo(0)
  } finally {
    busy.value = false
  }
}

/** One timer, reused: registering a disposer per animation leaks the scope. */
let timer: ReturnType<typeof setTimeout> | undefined
onScopeDispose(() => clearTimeout(timer))

function animateTo(target: number): Promise<void> {
  transition.value = `transform ${SLIDE_MS}ms cubic-bezier(0.22, 0.61, 0.36, 1)`
  offset.value = target
  return new Promise((resolve) => {
    // A little longer than the transition: clearing it early would cut the
    // animation short and show as a jump at the end.
    clearTimeout(timer)
    timer = setTimeout(resolve, SLIDE_MS + 20)
  })
}
</script>

<template>
  <div
    ref="root"
    class="min-h-0 flex-1 overflow-hidden"
    :class="verticalScroll ? 'touch-pan-y' : 'touch-none'"
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
    @pointercancel="onPointerCancel"
    @lostpointercapture="onLostCapture"
  >
    <div
      class="flex h-full flex-col"
      :style="{ transform: `translate3d(${offset}px, 0, 0)`, transition }"
    >
      <slot />
    </div>
  </div>
</template>
