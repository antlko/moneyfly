<script setup lang="ts">
import { useElementSize } from '@vueuse/core'
import { computed, useTemplateRef } from 'vue'

import { colorVar } from '@/lib/categories'
import type { CategoryTotal } from '@/stores/dashboard'
import type { Row } from '@/sync/types'
import CategoryIcon from './CategoryIcon.vue'
import MoneyAmount from './MoneyAmount.vue'

const props = defineProps<{
  totals: CategoryTotal[]
  allCategories: Row[]
  incomeMinor: number
  expenseMinor: number
  currency: string
}>()
const emit = defineEmits<{ select: [Row] }>()

/**
 * The icons sit on the **border cells of a grid that fills the whole area**, and
 * the donut lives in the hole.
 *
 * Two things about that are load-bearing, and both were got wrong first time:
 *
 *   * **A grid, not a curve.** Any curve — circle, superellipse, rounded
 *     rectangle — spaces icons by *angle*, so the gaps stretch and squeeze as it
 *     turns and the result looks scattered no matter how it is tuned. Only a
 *     grid gives equal spacing along each edge.
 *   * **A rectangle, not a square.** The frame takes all the height it is given:
 *     the top row sits under the carousel, the bottom row above the balance, and
 *     the side columns are stretched evenly between them. A square frame centred
 *     in a taller area leaves dead margins above and below and pulls everything
 *     into the middle of the screen.
 *
 * All of that needs real pixels, so the box is measured rather than expressed in
 * CSS: a grid whose columns are a share of the width and whose rows are a share
 * of the height cannot size a *circular* hole from CSS alone.
 */
const root = useTemplateRef<HTMLElement>('root')
const { width, height } = useElementSize(root)

/**
 * Target cell size.
 *
 * The grid is derived from these rather than from the number of categories, and
 * that is the important part. Sizing the frame to *fit every category* is what
 * produced nine thin rows and a shrunken chart: with four columns, nineteen
 * icons need eight rows, and the donut then has to survive on whatever is left.
 *
 * The reference does the opposite — it keeps the frame and the chart at a
 * readable size and simply does not draw the categories that do not fit (its own
 * screenshot shows fourteen of nineteen). They are never out of reach: the
 * record screen's grid is complete by construction.
 */
const TARGET_CELL_W = 150
const TARGET_CELL_H = 155
const MIN_COLS = 3
const MAX_COLS = 6
const MIN_ROWS = 3
const MAX_ROWS = 7

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))

const grid = computed(() => {
  const w = width.value || 360
  const h = height.value || 360
  const cols = clamp(Math.round(w / TARGET_CELL_W), MIN_COLS, MAX_COLS)
  const rows = clamp(Math.round(h / TARGET_CELL_H), MIN_ROWS, MAX_ROWS)
  return { cols, rows, cellW: w / cols, cellH: h / rows, width: w, height: h }
})

interface Cell {
  x: number
  y: number
  /** Degrees clockwise from twelve o'clock, used to match a cell to a slice. */
  angle: number
}

/**
 * Border cells, walked clockwise from the top-left corner.
 *
 * The order matters: unused categories are laid into whatever is left over, and
 * doing that in perimeter order keeps them in a sensible reading sequence
 * instead of jumping about the frame.
 */
const cells = computed<Cell[]>(() => {
  const { cols, rows, cellW, cellH, width: w, height: h } = grid.value

  const coords: { col: number; row: number }[] = []
  for (let col = 0; col < cols; col++) coords.push({ col, row: 0 })
  for (let row = 1; row < rows; row++) coords.push({ col: cols - 1, row })
  for (let col = cols - 2; col >= 0; col--) coords.push({ col, row: rows - 1 })
  for (let row = rows - 2; row >= 1; row--) coords.push({ col: 0, row })

  return coords.map(({ col, row }) => {
    const x = (col + 0.5) * cellW
    const y = (row + 0.5) * cellH
    const angle = (Math.atan2(x - w / 2, h / 2 - y) * 180) / Math.PI
    return { x, y, angle: (angle + 360) % 360 }
  })
})

/** Room between an icon's centre and the ring: the glyph, plus a gap. */
const ICON_CLEARANCE = 56

/** The donut fills the hole in the frame, staying circular. */
const donut = computed(() => {
  const { cellW, cellH, width: w, height: h } = grid.value
  // Measured between the icon *centres*, not the cell edges. The icons sit at
  // the middle of their cells, so sizing to the cell boundary would leave half a
  // cell of dead space all the way round and a needlessly small chart.
  const spanX = w - cellW
  const spanY = h - cellH
  const diameter = Math.max(80, Math.min(spanX, spanY) - 2 * ICON_CLEARANCE)
  return {
    diameter,
    left: (w - diameter) / 2,
    top: (h - diameter) / 2,
    cx: w / 2,
    cy: h / 2,
    // Proportions taken off the reference: the ring's outer edge *is* the
    // diameter, and the hole is 60% of it. The previous 0.36/0.12 drew a thin
    // ring whose outer edge fell well inside the space allotted, which is why
    // the chart looked small even when the space was right.
    radius: diameter * 0.4,
    stroke: diameter * 0.2,
  }
})

const circumference = computed(() => 2 * Math.PI * donut.value.radius)

/** A slice below this is too thin to label without the text colliding. */
const LABEL_THRESHOLD = 0.02

interface Placed {
  category: Row
  cell: Cell
  share: number
  /** Undefined for a category with nothing this period. */
  slice?: { angle: number; dash: string; offset: number; color: string }
}

/** Shortest way round the circle, in degrees. */
const separation = (a: number, b: number) => Math.abs(((a - b + 540) % 360) - 180)

/**
 * Match categories to cells.
 *
 * A category that has a slice takes the free cell nearest its slice's angle, so
 * its leader line is short and points outward rather than across the chart. The
 * biggest slice picks first — it is the one whose label the eye goes to.
 * Everything else fills the leftovers in perimeter order.
 */
const placed = computed<Placed[]>(() => {
  const free = [...cells.value]
  const out: Placed[] = []
  const c = circumference.value

  let cursor = 0
  const withSlices = props.totals.map(({ category, share }) => {
    const start = cursor
    cursor += share
    return {
      category,
      share,
      angle: (start + share / 2) * 360,
      dash: `${share * c} ${c}`,
      offset: -start * c,
      color: colorVar(category.color),
    }
  })

  for (const slice of [...withSlices].sort((a, b) => b.share - a.share)) {
    if (free.length === 0) break
    let best = 0
    for (let i = 1; i < free.length; i++) {
      if (separation(free[i].angle, slice.angle) < separation(free[best].angle, slice.angle)) {
        best = i
      }
    }
    const [cell] = free.splice(best, 1)
    out.push({ category: slice.category, cell, share: slice.share, slice })
  }

  const used = new Set(props.totals.map((t) => String(t.category.id)))
  // Categories beyond the frame's capacity are simply not drawn here. They stay
  // one tap away in the record screen's grid, which is complete by construction.
  for (const category of props.allCategories.filter((c2) => !used.has(String(c2.id)))) {
    const cell = free.shift()
    if (!cell) break
    out.push({ category, cell, share: 0 })
  }

  return out
})

/** The slices, in ring order rather than placement order. */
const slices = computed(() =>
  placed.value.filter((p) => p.slice).sort((a, b) => a.slice!.angle - b.slice!.angle),
)

const style = (cell: Cell) => ({ left: `${cell.x}px`, top: `${cell.y}px` })

/**
 * A straight line from the slice to its icon.
 *
 * Straight is right here, unlike on a circular layout: the icon sits at the cell
 * nearest its own slice, so the line always runs outward and never crosses the
 * hole where the totals are.
 */
function leader(item: Placed) {
  const { cx, cy, radius, stroke } = donut.value
  const rad = ((item.slice!.angle - 90) * Math.PI) / 180
  const x1 = cx + Math.cos(rad) * (radius + stroke / 2)
  const y1 = cy + Math.sin(rad) * (radius + stroke / 2)

  // Stop short of the icon so the line does not run under the glyph.
  const dx = item.cell.x - x1
  const dy = item.cell.y - y1
  const length = Math.hypot(dx, dy) || 1
  const gap = Math.min(24, length / 2)

  return {
    x1,
    y1,
    x2: item.cell.x - (dx / length) * gap,
    y2: item.cell.y - (dy / length) * gap,
  }
}

const percent = (share: number) => `${Math.round(share * 100)}%`
</script>

<template>
  <div ref="root" class="relative h-full w-full">
    <svg :viewBox="`0 0 ${grid.width} ${grid.height}`" class="absolute inset-0 h-full w-full">
      <!-- The track, so an empty period still reads as a donut rather than a void. -->
      <circle
        :cx="donut.cx"
        :cy="donut.cy"
        :r="donut.radius"
        fill="none"
        stroke="var(--color-mf-green-soft)"
        :stroke-width="donut.stroke"
        :opacity="slices.length ? 0.25 : 0.5"
      />

      <g :transform="`rotate(-90 ${donut.cx} ${donut.cy})`">
        <circle
          v-for="item in slices"
          :key="item.category.id"
          :cx="donut.cx"
          :cy="donut.cy"
          :r="donut.radius"
          fill="none"
          :stroke="item.slice!.color"
          :stroke-width="donut.stroke"
          :stroke-dasharray="item.slice!.dash"
          :stroke-dashoffset="item.slice!.offset"
          class="cursor-pointer"
          @click="emit('select', item.category)"
        />
      </g>

      <line
        v-for="item in slices.filter((s) => s.share >= LABEL_THRESHOLD)"
        :key="`leader-${item.category.id}`"
        v-bind="leader(item)"
        :stroke="item.slice!.color"
        stroke-width="1"
        opacity="0.7"
      />
    </svg>

    <!--
      Icons and percentages are HTML, not SVG: real components, real text, and
      real buttons. Every one of them starts an expense in that category — the
      two-tap path for a purchase you make every week.
    -->
    <button
      v-for="item in placed"
      :key="item.category.id"
      type="button"
      class="absolute flex -translate-x-1/2 -translate-y-1/2 flex-col items-center gap-0.5 p-1"
      :aria-label="`New expense in ${item.category.name}`"
      :style="style(item.cell)"
      @click="emit('select', item.category)"
    >
      <CategoryIcon
        :icon="item.category.icon"
        :color="item.category.color"
        :size="28"
        :dim="!item.slice"
      />
      <span
        v-if="item.share >= LABEL_THRESHOLD"
        class="text-xs font-semibold"
        :style="{ color: colorVar(item.category.color) }"
        >{{ percent(item.share) }}</span
      >
    </button>

    <div
      class="pointer-events-none absolute flex flex-col items-center justify-center gap-1"
      :style="{
        left: `${donut.left}px`,
        top: `${donut.top}px`,
        width: `${donut.diameter}px`,
        height: `${donut.diameter}px`,
      }"
    >
      <MoneyAmount
        :minor="incomeMinor"
        :currency="currency"
        class="text-xl font-semibold text-mf-green-dark"
      />
      <MoneyAmount
        :minor="expenseMinor"
        :currency="currency"
        class="text-xl font-semibold text-mf-red-text"
      />
    </div>
  </div>
</template>
