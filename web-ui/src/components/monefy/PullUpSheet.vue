<script setup lang="ts">
import { computed, onScopeDispose, ref, useTemplateRef, watch } from 'vue'

import { releaseVelocity, settleSheet, VELOCITY_WINDOW_MS, type Sample } from '@/lib/sheet'

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
 *   * default — the sheet's body (the category list).
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
/** Movement that turns a press into a drag. Below it, it is still a tap. */
const SLOP_PX = 6

const stage = useTemplateRef<HTMLElement>('stage')
const sheet = useTemplateRef<HTMLElement>('sheet')
const handle = useTemplateRef<HTMLElement>('handle')

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
 * The gesture. Same rules as every other one in the app (SwipePager,
 * RecordsSheet): remember which pointer pressed, ignore a mouse that moves
 * with no button held, and never act on a gesture that was not ours.
 *
 * It starts on the handle and is then **followed on `window`**, not on the
 * handle. A finger is implicitly captured by what it landed on, so its events
 * would arrive either way — but a mouse is not, and the handle is a 56px strip:
 * a quick drag leaves it on the first move and the handle never hears another
 * thing. Pointer capture would fix that and break something worse — while an
 * element holds capture, the click that ends a *tap* is retargeted to it rather
 * than to the `≡` or the pill that was pressed (see SwipePager). Listening on
 * `window` for the length of one press needs neither.
 */
let pointer: number | null = null
let startY = 0
/** Progress when the drag took hold — the sheet's *actual* position then. */
let base = 0
/** The resting state the gesture is measured against. */
let from: 0 | 1 = 0
let travel = 1
let samples: Sample[] = []
/** Set once a press became a drag, so the click that follows can be swallowed. */
let moved = false

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

/** Window listeners for the press in progress, removed when it ends. */
let unlisten: (() => void) | null = null

function onDown(e: PointerEvent) {
  if (!e.isPrimary) return
  if (e.pointerType !== 'touch' && e.button !== 0) return
  // A press whose release never arrived (focus lost mid-drag) must not leave
  // the handle deaf: the new press replaces it.
  if (unlisten) {
    unlisten()
    unlisten = null
    if (dragging.value) settle(0)
  }
  pointer = e.pointerId
  startY = e.clientY
  moved = false
  samples = [{ t: e.timeStamp, y: e.clientY }]
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
    if (Math.abs(e.clientY - startY) < SLOP_PX) return
    travel = Math.max(1, (stage.value?.clientHeight ?? 1) - (handle.value?.offsetHeight ?? 0))
    // From where the sheet *is*, and measured from where the press began, so
    // the few pixels of slop are caught up at once rather than lagged forever.
    base = visualProgress()
    from = props.open ? 1 : 0
    moved = true
    dragging.value = true
  }

  progress.value = clamp01(base - (e.clientY - startY) / travel)
  samples.push({ t: e.timeStamp, y: e.clientY })
  // Only the last window matters for the release speed; keep the array short.
  while (samples.length > 2 && e.timeStamp - samples[0]!.t > VELOCITY_WINDOW_MS) samples.shift()
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
  if (endGesture(e)) settle(releaseVelocity(samples, e.timeStamp))
}

/** A cancelled drag still lands somewhere sensible: decided on distance alone. */
function onCancel(e: PointerEvent) {
  if (endGesture(e)) settle(0)
}

function settle(velocity: number) {
  const target = settleSheet(from, progress.value, velocity)
  dragging.value = false
  progress.value = target
  if ((target === 1) !== props.open) emit('update:open', target === 1)
}

/**
 * Swallow the click a drag ends with.
 *
 * In the capture phase, so it reaches no button in the handle: a drag that
 * happens to end over `≡` must not then toggle the view straight back. Reset on
 * every press, so a drag that produced *no* click (touch usually does not)
 * cannot eat the next honest tap — the bug the balance pill once had.
 */
function onClickCapture(e: MouseEvent) {
  if (!moved) return
  moved = false
  e.stopPropagation()
  e.preventDefault()
}
</script>

<template>
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

      <div class="flex min-h-0 flex-1 flex-col" :inert="progress === 0 && !dragging">
        <slot />
      </div>
    </section>
  </div>
</template>
