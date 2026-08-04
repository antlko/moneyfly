<script setup lang="ts">
import { AlignJustify, ArrowDownWideNarrow } from '@lucide/vue'
import { computed, ref, useTemplateRef } from 'vue'

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
 * The whole row is a handle: swiping it up opens the period's records, which is
 * the gesture the reference uses. A tap on the pill does the same, so the
 * affordance is discoverable without knowing about it.
 *
 * The drag is tracked, not merely detected, so the pill lifts with the finger —
 * a control that responds only after you let go gives no sign it heard you.
 */
const row = useTemplateRef<HTMLElement>('row')
const lift = ref(0)
let startY = 0
let tracking = false
let captured = false
let dragged = false

/** Upward travel that counts as "open". */
const OPEN_THRESHOLD = 20

function onDown(e: PointerEvent) {
  if (!e.isPrimary) return
  startY = e.clientY
  tracking = true
  captured = false
  dragged = false
}

function onMove(e: PointerEvent) {
  if (!tracking) return
  // Downward movement is ignored; there is nothing below to reveal.
  const up = startY - e.clientY
  if (!captured && up > 6) {
    // Capture is what makes this work at all on a phone: a swipe up leaves the
    // row within a few pixels, and without it every later event goes to
    // whatever is now under the finger — the gesture was dropped silently.
    // Taking it only after real movement keeps taps on the buttons working,
    // because a captured pointer delivers its click to the capturing element.
    row.value?.setPointerCapture(e.pointerId)
    captured = true
    dragged = true
  }
  lift.value = Math.max(0, Math.min(56, up))
}

function onUp(e: PointerEvent) {
  if (!tracking) return
  tracking = false
  if (captured) row.value?.releasePointerCapture?.(e.pointerId)
  const opened = lift.value >= OPEN_THRESHOLD
  lift.value = 0
  if (opened) emit('open')
}

/** A tap opens it too — but a drag has already decided, so do not fire twice. */
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
    ref="row"
    class="flex shrink-0 touch-pan-x items-center gap-2 px-4"
    @pointerdown="onDown"
    @pointermove="onMove"
    @pointerup="onUp"
    @pointercancel="onUp"
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
      aria-label="Show records for this period"
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
