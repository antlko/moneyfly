<script setup lang="ts">
import { AlignJustify, ArrowDownWideNarrow } from '@lucide/vue'
import { computed, ref } from 'vue'

import MoneyAmount from './MoneyAmount.vue'

const props = defineProps<{
  balanceMinor: number
  currency: string
  /** In donut mode the reference shows the toggle on both sides of the pill. */
  showTrailingToggle?: boolean
}>()
const emit = defineEmits<{ toggleView: []; toggleSort: []; open: [] }>()

const negative = computed(() => props.balanceMinor < 0)

/*
 * The whole row is a handle, and the two gestures on it do the two things the
 * row offers:
 *
 *   * **swipe up → switch view**, exactly what the `≡` button beside it does.
 *     One action, two ways to reach it: the button for people who look, the
 *     gesture for people who don't. Having the swipe do something the buttons
 *     could not is what made it feel like a third, hidden feature.
 *   * **tap → the period's records**, grouped by day.
 *
 * The drag is tracked, not merely detected, so the pill lifts with the finger —
 * a control that responds only after you let go gives no sign it heard you.
 */
const lift = ref(0)
let startY = 0
/** The pointer whose `pointerdown` we saw — `null` means nothing is pressed. */
let activePointer: number | null = null
/** Set once this gesture has travelled far enough to be a swipe, not a tap. */
let dragged = false

/** Upward travel that counts as a deliberate swipe. */
const SWITCH_THRESHOLD = 20

/**
 * No `setPointerCapture` here, deliberately.
 *
 * It looks necessary — the finger leaves the row within a few pixels — but for
 * touch it is not: the browser *already* captures a touch pointer implicitly to
 * the element it landed on, so the events keep arriving whatever ends up under
 * the finger. Taking capture on top of that bought nothing and cost three
 * things: it revoked the descendant's implicit capture (whose
 * `lostpointercapture` bubbles, which is what broke the pager), it made the
 * release order matter, and it moved the click to the row so the button below
 * had to be told to ignore its own.
 *
 * The row is `touch-action: none` instead. That is the honest way to say "this
 * row's gestures are mine": iOS then never claims the drag half-way through and
 * cancels it, which is what left the pill dead to both swipes and the tap after
 * one.
 */
function onDown(e: PointerEvent) {
  if (!e.isPrimary) return
  // A mouse emits `pointermove` while hovering; without a recorded pointer and a
  // held button the pill would follow the cursor around the screen.
  if (e.pointerType !== 'touch' && e.button !== 0) return
  startY = e.clientY
  activePointer = e.pointerId
  dragged = false
  lift.value = 0
}

function onMove(e: PointerEvent) {
  if (activePointer !== e.pointerId) return
  // A button let go outside the window never delivers `pointerup`.
  if (e.pointerType !== 'touch' && e.buttons === 0) {
    onCancel()
    return
  }
  // Downward movement is ignored; there is nothing below to reveal.
  const up = startY - e.clientY
  if (up > 6) dragged = true
  lift.value = Math.max(0, Math.min(56, up))
}

function onUp(e: PointerEvent) {
  if (activePointer !== e.pointerId) return
  activePointer = null
  const swiped = lift.value >= SWITCH_THRESHOLD
  lift.value = 0
  if (swiped) emit('toggleView')
}

/**
 * A cancelled gesture does nothing at all — it must not act on the distance it
 * happened to have travelled.
 *
 * `pointercancel` used to be wired to `onUp`, so a gesture the browser took away
 * mid-drag still counted as a deliberate swipe. It also left `dragged` latched,
 * and `dragged` is what suppresses the click — so the *next* honest tap was
 * swallowed too, and the pill looked dead.
 */
function onCancel() {
  activePointer = null
  dragged = false
  lift.value = 0
}

/** A tap opens the records. A swipe has already acted, so it must not fire too. */
function onPillClick() {
  if (dragged) {
    dragged = false
    return
  }
  emit('open')
}
</script>

<template>
  <div
    class="flex shrink-0 touch-none items-center gap-2 px-4"
    @pointerdown="onDown"
    @pointermove="onMove"
    @pointerup="onUp"
    @pointercancel="onCancel"
  >
    <button
      type="button"
      class="p-2 text-mf-green-dark"
      aria-label="Switch view"
      @click="emit('toggleView')"
    >
      <AlignJustify :size="24" :stroke-width="2" />
    </button>

    <button
      type="button"
      class="flex flex-1 items-center justify-center gap-3 rounded-md px-4 py-2.5 text-white shadow-sm"
      :class="negative ? 'bg-mf-red' : 'bg-mf-green'"
      :style="{
        transform: `translateY(${-lift}px)`,
        transition: lift ? '' : 'transform 160ms ease',
      }"
      aria-label="Show records for this period. Swipe up to switch view."
      @click="onPillClick"
    >
      <span class="text-base font-medium">Balance</span>
      <MoneyAmount :minor="balanceMinor" :currency="currency" class="text-lg font-semibold" />
    </button>

    <button
      v-if="showTrailingToggle"
      type="button"
      class="p-2 text-mf-green-dark"
      aria-label="Switch view"
      @click="emit('toggleView')"
    >
      <AlignJustify :size="24" :stroke-width="2" />
    </button>
    <button
      v-else
      type="button"
      class="p-2 text-mf-green-dark"
      aria-label="Change sorting"
      @click="emit('toggleSort')"
    >
      <ArrowDownWideNarrow :size="24" :stroke-width="2" />
    </button>
  </div>
</template>
