<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed, ref } from 'vue'

import { longDate } from '@/lib/period'
import type { Row } from '@/sync/types'
import TransactionRow from './TransactionRow.vue'

const props = defineProps<{ rows: Row[]; title: string }>()
const emit = defineEmits<{ close: []; open: [Row] }>()

/**
 * Every record in the period, newest first, grouped by day.
 *
 * The dashboard's list mode groups by category; this groups by day, which is the
 * order you look in for something you just entered. What it must *not* do is
 * look like a different application — it used to be a short bottom sheet with
 * its own row design and a text "Close", so pulling the balance up landed you
 * somewhere unrecognisable. Same rows, same background, near-full height.
 */
const days = computed(() => {
  const groups = new Map<string, Row[]>()
  for (const row of [...props.rows].sort((a, b) =>
    String(b.occurredOn).localeCompare(String(a.occurredOn)),
  )) {
    const day = String(row.occurredOn)
    const list = groups.get(day)
    if (list) list.push(row)
    else groups.set(day, [row])
  }
  return [...groups.entries()]
})

/*
 * Dragging back down closes it: the gesture that opened it, run backwards.
 * Without it the only way out is a small target, and a panel you have to aim at
 * to dismiss feels stuck.
 */
const drag = ref(0)
let activePointer: number | null = null
let startY = 0

function onDown(e: PointerEvent) {
  if (!e.isPrimary) return
  if (e.pointerType !== 'touch' && e.button !== 0) return
  startY = e.clientY
  activePointer = e.pointerId
}
function onMove(e: PointerEvent) {
  if (activePointer !== e.pointerId) return
  // Same rule as every other gesture here: a mouse moving with no button held
  // is not a drag, and a button let go off-window never sends pointerup.
  if (e.pointerType !== 'touch' && e.buttons === 0) {
    onUp(e)
    return
  }
  drag.value = Math.max(0, e.clientY - startY)
}
function onUp(e: PointerEvent) {
  if (activePointer !== e.pointerId) return
  activePointer = null
  if (drag.value > 90) emit('close')
  drag.value = 0
}
</script>

<template>
  <div class="fixed inset-0 z-40 flex flex-col justify-end bg-black/30" @click.self="emit('close')">
    <section
      class="flex h-[92%] flex-col rounded-t-2xl bg-mf-bg shadow-2xl"
      :style="{
        transform: `translateY(${drag}px)`,
        transition: drag ? '' : 'transform 180ms ease',
      }"
    >
      <header
        class="shrink-0 cursor-grab touch-none px-4 pt-2 pb-3"
        @pointerdown="onDown"
        @pointermove="onMove"
        @pointerup="onUp"
        @pointercancel="onUp"
      >
        <div class="mx-auto mb-3 h-1 w-10 rounded-full bg-mf-muted" />
        <div class="flex items-center gap-2">
          <h2 class="flex-1 text-lg font-medium text-mf-green-dark">{{ title }}</h2>
          <button
            type="button"
            class="grid size-9 place-items-center rounded-full text-mf-green-dark"
            aria-label="Close"
            @click="emit('close')"
          >
            <ChevronDown :size="22" :stroke-width="2" />
          </button>
        </div>
      </header>

      <div class="min-h-0 flex-1 overflow-y-auto pb-[calc(1rem+var(--spacing-safe-b))]">
        <template v-for="[day, records] in days" :key="day">
          <p class="bg-mf-green-soft/25 px-4 py-1 text-xs text-mf-ink/70">{{ longDate(day) }}</p>
          <ul class="divide-y divide-mf-muted/20 px-4">
            <li v-for="row in records" :key="row.id">
              <TransactionRow :row="row" @select="emit('open', $event)" />
            </li>
          </ul>
        </template>

        <p v-if="!days.length" class="px-4 py-10 text-center text-sm text-mf-muted">
          Nothing recorded in this period.
        </p>
      </div>
    </section>
  </div>
</template>
