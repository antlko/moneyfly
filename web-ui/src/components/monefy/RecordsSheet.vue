<script setup lang="ts">
import { computed, ref } from 'vue'

import { longDate } from '@/lib/period'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'
import CategoryIcon from './CategoryIcon.vue'
import MoneyAmount from './MoneyAmount.vue'

const props = defineProps<{ rows: Row[]; currency: string; title: string }>()
const emit = defineEmits<{ close: [] }>()

const taxonomy = useTaxonomyStore()

/** Newest first, grouped by day — the order you look for a record you just made. */
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

const categoryOf = (row: Row) => taxonomy.byId.get(String(row.categoryId ?? ''))

/*
 * Dragging the sheet back down closes it, which is the gesture that opened it
 * run backwards. Without it the only way out is the header button, and a sheet
 * you can only dismiss by aiming at a small target feels stuck.
 */
const drag = ref(0)
let startY = 0
let tracking = false

function onDown(e: PointerEvent) {
  if (!e.isPrimary) return
  startY = e.clientY
  tracking = true
}
function onMove(e: PointerEvent) {
  if (!tracking) return
  drag.value = Math.max(0, e.clientY - startY)
}
function onUp() {
  if (!tracking) return
  tracking = false
  if (drag.value > 90) emit('close')
  drag.value = 0
}
</script>

<template>
  <div class="fixed inset-0 z-40 flex flex-col justify-end bg-black/30" @click.self="emit('close')">
    <section
      class="flex max-h-[80%] flex-col rounded-t-2xl bg-mf-bg shadow-2xl"
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
        <div class="flex items-center justify-between">
          <h2 class="text-base font-medium">{{ title }}</h2>
          <button type="button" class="text-sm text-mf-green-dark" @click="emit('close')">
            Close
          </button>
        </div>
      </header>

      <div class="min-h-0 flex-1 overflow-y-auto pb-[calc(1rem+var(--spacing-safe-b))]">
        <template v-for="[day, records] in days" :key="day">
          <p class="bg-mf-green-soft/25 px-4 py-1 text-xs text-mf-ink/70">{{ longDate(day) }}</p>
          <ul class="divide-y divide-mf-muted/20">
            <li v-for="row in records" :key="row.id" class="flex items-center gap-3 px-4 py-2.5">
              <CategoryIcon
                :icon="categoryOf(row)?.icon"
                :color="categoryOf(row)?.color"
                :size="26"
              />
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm">
                  {{ row.note || categoryOf(row)?.name || 'Uncategorised' }}
                </p>
                <p v-if="row.note" class="truncate text-xs text-mf-muted">
                  {{ categoryOf(row)?.name }}
                </p>
              </div>
              <MoneyAmount
                :minor="Number(row.amountMinor ?? 0)"
                :currency="String(row.currency ?? currency)"
                class="text-sm font-medium"
                :class="
                  Number(row.amountMinor ?? 0) < 0 ? 'text-mf-red-text' : 'text-mf-green-dark'
                "
              />
              <button
                type="button"
                class="text-xs text-mf-red-text"
                @click="sync.remove('txn', row.id)"
              >
                Delete
              </button>
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
