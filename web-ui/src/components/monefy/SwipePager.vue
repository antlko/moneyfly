<script setup lang="ts">
import { nextTick, onScopeDispose, ref } from 'vue'

const props = withDefaults(defineProps<{ disabled?: boolean }>(), { disabled: false })
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
 * `touch-action: pan-y` is what makes this coexist with the scrolling list: the
 * browser keeps vertical scrolling to itself and only horizontal gestures reach
 * these handlers.
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

let startX = 0
let startY = 0
let width = 1
let axis: 'undecided' | 'x' | 'y' = 'undecided'

function onPointerDown(e: PointerEvent) {
  if (!e.isPrimary || busy.value || props.disabled) return
  startX = e.clientX
  startY = e.clientY
  width = root.value?.clientWidth ?? 1
  axis = 'undecided'
  transition.value = ''
}

function onPointerMove(e: PointerEvent) {
  if (!e.isPrimary || busy.value || axis === 'y') return
  const dx = e.clientX - startX
  const dy = e.clientY - startY

  if (axis === 'undecided') {
    // Wait until the gesture has committed to a direction. Deciding on the very
    // first move would steal the start of every vertical scroll.
    if (Math.abs(dx) < 8 && Math.abs(dy) < 8) return
    axis = Math.abs(dx) > Math.abs(dy) ? 'x' : 'y'
    if (axis === 'y') return
    // Capture only once this is definitely a drag. Capturing on pointerdown
    // would swallow the click on every category icon inside the pager, because
    // the click is then delivered to the capturing element instead.
    root.value?.setPointerCapture(e.pointerId)
  }
  offset.value = dx
}

async function onPointerUp(e: PointerEvent) {
  root.value?.releasePointerCapture?.(e.pointerId)
  if (axis !== 'x' || busy.value) {
    axis = 'undecided'
    return
  }
  axis = 'undecided'

  const dx = e.clientX - startX
  const threshold = Math.min(width * COMMIT_RATIO, COMMIT_MAX_PX)
  if (Math.abs(dx) < threshold) {
    await animateTo(0)
    transition.value = ''
    return
  }
  await commit(dx < 0 ? 1 : -1)
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

function animateTo(target: number): Promise<void> {
  transition.value = `transform ${SLIDE_MS}ms cubic-bezier(0.22, 0.61, 0.36, 1)`
  offset.value = target
  return new Promise((resolve) => {
    // A little longer than the transition: clearing it early would cut the
    // animation short and show as a jump at the end.
    const timer = setTimeout(resolve, SLIDE_MS + 20)
    onScopeDispose(() => clearTimeout(timer))
  })
}
</script>

<template>
  <div
    ref="root"
    class="min-h-0 flex-1 overflow-hidden touch-pan-y"
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
    @pointercancel="axis = 'undecided'"
  >
    <div
      class="flex h-full flex-col"
      :style="{ transform: `translate3d(${offset}px, 0, 0)`, transition }"
    >
      <slot />
    </div>
  </div>
</template>
