<script setup lang="ts">
import { computed, ref } from 'vue'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import { colorVar } from '@/lib/categories'
import type { CategoryTotal } from '@/stores/dashboard'

const props = defineProps<{
  totals: CategoryTotal[]
  expenseMinor: number
  currency: string
  /** The category whose records are listed below, if any. */
  selected: string | null
}>()
const emit = defineEmits<{ select: [string] }>()

/**
 * The desktop dashboard's pie: a donut with the period's total in the hole and
 * a legend beside it.
 *
 * Drawn from the same `byCategory` the bars and the phone's donut use — a
 * second picture of one calculation, never a second calculation. Hovering a
 * slice or a legend row puts that category in the hole; clicking either one
 * selects it, which lists its records under the chart.
 */
const SIZE = 240
const CENTER = SIZE / 2
const OUTER = 112
const INNER = 72
/** How far a hovered or selected slice slides out from the centre. */
const LIFT = 6
/** Degrees of empty space between neighbouring slices. */
const GAP = 0.8

const hovered = ref<string | null>(null)
const focus = computed(() => hovered.value ?? props.selected)

interface Slice {
  id: string
  total: CategoryTotal
  path: string
  /** Mid-angle in radians, clockwise from twelve o'clock: the direction a slice lifts in. */
  mid: number
}

const point = (r: number, a: number) =>
  `${(CENTER + r * Math.sin(a)).toFixed(2)} ${(CENTER - r * Math.cos(a)).toFixed(2)}`

/** One ring segment from angle a0 to a1, in radians. */
function arc(a0: number, a1: number): string {
  // A lone category is a whole ring, which one arc command cannot draw: its
  // start and end points coincide and the arc collapses to nothing. Two halves.
  if (a1 - a0 >= 2 * Math.PI - 1e-6) {
    const m = a0 + Math.PI
    return `${arc(a0, m)} ${arc(m, a1)}`
  }
  const large = a1 - a0 > Math.PI ? 1 : 0
  return [
    `M ${point(OUTER, a0)}`,
    `A ${OUTER} ${OUTER} 0 ${large} 1 ${point(OUTER, a1)}`,
    `L ${point(INNER, a1)}`,
    `A ${INNER} ${INNER} 0 ${large} 0 ${point(INNER, a0)}`,
    'Z',
  ].join(' ')
}

const slices = computed<Slice[]>(() => {
  const out: Slice[] = []
  const gap = props.totals.length > 1 ? (GAP * Math.PI) / 180 : 0
  let start = 0
  for (const total of props.totals) {
    const sweep = total.share * 2 * Math.PI
    if (sweep <= 0) continue
    const end = start + sweep
    // A sliver thinner than the gap would invert; draw it at a hairline instead.
    const a0 = start + Math.min(gap / 2, sweep / 4)
    const a1 = end - Math.min(gap / 2, sweep / 4)
    out.push({ id: String(total.category.id), total, path: arc(a0, a1), mid: (start + end) / 2 })
    start = end
  }
  return out
})

function sliceTransform(s: Slice) {
  if (focus.value !== s.id) return undefined
  return `translate(${(LIFT * Math.sin(s.mid)).toFixed(2)} ${(-LIFT * Math.cos(s.mid)).toFixed(2)})`
}

const focused = computed(() => props.totals.find((t) => String(t.category.id) === focus.value) ?? null)

const percent = (share: number) => {
  const p = share * 100
  return p > 0 && p < 1 ? '<1%' : `${Math.round(p)}%`
}
</script>

<template>
  <div class="flex flex-col items-center gap-6 xl:flex-row xl:items-start">
    <div class="relative shrink-0" :style="{ width: `${SIZE}px`, height: `${SIZE}px` }">
      <svg
        :viewBox="`${-LIFT} ${-LIFT} ${SIZE + 2 * LIFT} ${SIZE + 2 * LIFT}`"
        class="size-full overflow-visible"
        role="img"
        aria-label="Spending by category"
        @pointerleave="hovered = null"
      >
        <path
          v-for="s in slices"
          :key="s.id"
          :d="s.path"
          :fill="colorVar(s.total.category.color)"
          :transform="sliceTransform(s)"
          class="cursor-pointer transition-[opacity,transform] duration-200"
          :class="focus && focus !== s.id ? 'opacity-35' : 'opacity-100'"
          @pointerenter="hovered = s.id"
          @click="emit('select', s.id)"
        >
          <title>{{ s.total.category.name }} — {{ percent(s.total.share) }}</title>
        </path>
      </svg>

      <!-- The hole: the focused category, or the period's total when nothing is. -->
      <div class="pointer-events-none absolute inset-0 grid place-items-center text-center">
        <div v-if="focused" class="flex max-w-32 flex-col items-center gap-0.5">
          <CategoryIcon :icon="focused.category.icon" :color="focused.category.color" :size="22" />
          <span class="max-w-full truncate text-sm text-mf-ink">{{ focused.category.name }}</span>
          <MoneyAmount :minor="Math.abs(focused.totalMinor)" :currency="currency" class="text-lg font-semibold text-mf-red-text" />
          <span class="text-xs text-mf-muted">{{ percent(focused.share) }} · {{ focused.count }} record{{ focused.count === 1 ? '' : 's' }}</span>
        </div>
        <div v-else class="flex flex-col items-center">
          <span class="text-xs text-mf-muted">Expenses</span>
          <MoneyAmount :minor="Math.abs(expenseMinor)" :currency="currency" class="text-xl font-semibold text-mf-red-text" />
          <span class="text-xs text-mf-muted">{{ totals.length }} categor{{ totals.length === 1 ? 'y' : 'ies' }}</span>
        </div>
      </div>
    </div>

    <ul class="w-full min-w-0 flex-1 space-y-0.5" @pointerleave="hovered = null">
      <li v-for="t in totals" :key="String(t.category.id)">
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm transition-colors"
          :class="selected === String(t.category.id) ? 'bg-mf-green-soft/40' : 'hover:bg-mf-bg'"
          @pointerenter="hovered = String(t.category.id)"
          @click="emit('select', String(t.category.id))"
        >
          <span class="size-2.5 shrink-0 rounded-full" :style="{ backgroundColor: colorVar(t.category.color) }" />
          <span class="min-w-0 flex-1 truncate">{{ t.category.name }}</span>
          <span class="w-10 shrink-0 text-right text-xs text-mf-muted tabular-nums">{{ percent(t.share) }}</span>
          <MoneyAmount :minor="Math.abs(t.totalMinor)" :currency="currency" class="shrink-0" />
        </button>
      </li>
    </ul>
  </div>
</template>
