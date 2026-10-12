<script setup lang="ts">
import { useElementSize } from '@vueuse/core'
import { computed, ref, useTemplateRef } from 'vue'

import { labelledMonths, monthTick, niceTicks } from '@/lib/chart'
import { monthLabelWithYear, type MonthKey } from '@/lib/period'

/**
 * Months along the bottom, one value axis, any mix of lines, areas and columns.
 *
 * Hand-drawn SVG rather than a charting library: the app has exactly this one
 * chart shape (a figure per month), and the rules it has to follow are few and
 * fixed — one axis only (two measures of different scale get two charts, never
 * a second y-axis), 2px lines, columns at most 24px wide with a 4px rounded end
 * and a square base, solid hairline gridlines, a 2px surface ring on dots, and
 * a crosshair that snaps to the month rather than asking anyone to aim at a
 * line. See the dataviz notes in docs/ARCHITECTURE.md §"Capital".
 *
 * Text never takes a series colour: values and labels are ink, and identity
 * comes from the key beside them.
 */
export interface ChartSeries {
  key: string
  label: string
  type: 'line' | 'area' | 'column'
  values: (number | null)[]
  /** A CSS colour — a `var(--color-chart-*)`. */
  color: string
  /** Per-value colour for a column series whose sign carries the meaning. */
  colorOf?: (value: number) => string
}

const props = withDefaults(
  defineProps<{
    months: MonthKey[]
    series: ChartSeries[]
    /** A value as the tooltip shows it. */
    format: (value: number) => string
    /** A value as an axis tick shows it — usually shorter. */
    formatTick?: (value: number) => string
    height?: number
    /** Label the last value of the first series, at its end point. */
    endLabel?: boolean
    /** Accessible name for the chart as a whole. */
    label: string
  }>(),
  { formatTick: undefined, height: 220, endLabel: false },
)

const root = useTemplateRef<HTMLElement>('root')
const { width } = useElementSize(root)

const PAD_BASE = { top: 18, right: 14, bottom: 24, left: 56 }
/*
 * Room to the right of the last month for the end label. Placed beside the end
 * dot rather than above it, because above it the label sits on whatever the
 * line did in the months before — which is usually where the line is.
 */
const END_LABEL_ROOM = 84
const PAD = computed(() => ({ ...PAD_BASE, right: props.endLabel ? END_LABEL_ROOM : PAD_BASE.right }))
const MAX_COLUMN = 24
const GAP = 2

const w = computed(() => Math.max(width.value, 200))
const innerW = computed(() => w.value - PAD.value.left - PAD.value.right)
const innerH = computed(() => props.height - PAD.value.top - PAD.value.bottom)
const n = computed(() => props.months.length)
const band = computed(() => innerW.value / Math.max(1, n.value))
const cx = (i: number) => PAD.value.left + band.value * (i + 0.5)

const allValues = computed(() =>
  props.series.flatMap((s) => s.values.filter((v): v is number => v !== null && Number.isFinite(v))),
)
const empty = computed(() => allValues.value.length === 0)

const ticks = computed(() =>
  niceTicks(Math.min(0, ...allValues.value), Math.max(0, ...allValues.value)),
)
const lo = computed(() => ticks.value[0] ?? 0)
const hi = computed(() => ticks.value[ticks.value.length - 1] ?? 1)
const y = (v: number) =>
  PAD.value.top + innerH.value * (1 - (v - lo.value) / (hi.value - lo.value || 1))
const y0 = computed(() => y(0))

const tickText = (v: number) => (props.formatTick ?? props.format)(v)

const labelled = computed(() => labelledMonths(n.value, Math.floor(innerW.value / 46)))
const firstLabelled = computed(() => Math.min(...labelled.value))

/* --- columns -------------------------------------------------------------- */

const columnSeries = computed(() => props.series.filter((s) => s.type === 'column'))
const columnWidth = computed(() => {
  const k = columnSeries.value.length || 1
  return Math.max(2, Math.min(MAX_COLUMN, (band.value * 0.72 - GAP * (k - 1)) / k))
})

/** A column with a rounded data end and a square base, growing either way from zero. */
function columnPath(x: number, width: number, value: number): string {
  const top = y(value)
  const base = y0.value
  const h = base - top
  if (Math.abs(h) < 0.5) return ''
  const r = Math.min(4, width / 2, Math.abs(h))
  const end = h > 0 ? top + r : top - r
  return `M${x},${base} V${end} Q${x},${top} ${x + r},${top} H${x + width - r} Q${x + width},${top} ${x + width},${end} V${base} Z`
}

const columns = computed(() => {
  const k = columnSeries.value.length
  const cw = columnWidth.value
  const group = k * cw + (k - 1) * GAP
  return columnSeries.value.flatMap((s, j) =>
    s.values.flatMap((v, i) => {
      if (v === null || !Number.isFinite(v)) return []
      const x = cx(i) - group / 2 + j * (cw + GAP)
      return [{ key: `${s.key}-${i}`, d: columnPath(x, cw, v), fill: s.colorOf?.(v) ?? s.color }]
    }),
  )
})

/* --- lines and areas ------------------------------------------------------ */

/** Runs of consecutive values: a missing month breaks the line rather than bridging it. */
function runs(values: (number | null)[]): { i: number; v: number }[][] {
  const out: { i: number; v: number }[][] = []
  let current: { i: number; v: number }[] = []
  values.forEach((v, i) => {
    if (v === null || !Number.isFinite(v)) {
      if (current.length) out.push(current)
      current = []
    } else current.push({ i, v })
  })
  if (current.length) out.push(current)
  return out
}

const lines = computed(() =>
  props.series
    .filter((s) => s.type !== 'column')
    .map((s) => {
      const segments = runs(s.values)
      const paths = segments.map((seg) => seg.map((p, k) => `${k ? 'L' : 'M'}${cx(p.i)},${y(p.v)}`).join(' '))
      const areas =
        s.type === 'area'
          ? segments
              .filter((seg) => seg.length > 1)
              .map(
                (seg) =>
                  `M${cx(seg[0]!.i)},${y0.value} ` +
                  seg.map((p) => `L${cx(p.i)},${y(p.v)}`).join(' ') +
                  ` L${cx(seg[seg.length - 1]!.i)},${y0.value} Z`,
              )
          : []
      // Isolated months get a dot, or a one-point "line" would be invisible.
      const singles = segments.filter((seg) => seg.length === 1).map((seg) => seg[0]!)
      const last = segments.at(-1)?.at(-1) ?? null
      return { s, paths, areas, singles, last }
    }),
)

/** The one direct label: the latest value of the headline series. */
const end = computed(() => {
  if (!props.endLabel) return null
  const first = lines.value[0]
  if (!first?.last) return null
  return { x: cx(first.last.i), y: y(first.last.v), text: props.format(first.last.v) }
})

/* --- hover ---------------------------------------------------------------- */

const hover = ref<number | null>(null)

function onMove(e: PointerEvent) {
  const box = root.value?.getBoundingClientRect()
  if (!box) return
  const i = Math.floor((e.clientX - box.left - PAD.value.left) / band.value)
  hover.value = i >= 0 && i < n.value ? i : null
}

/** The same readout from the keyboard: arrows step through the months. */
function onKey(e: KeyboardEvent) {
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
  e.preventDefault()
  const at = hover.value ?? n.value - 1
  hover.value = Math.max(0, Math.min(n.value - 1, at + (e.key === 'ArrowLeft' ? -1 : 1)))
}

const tooltip = computed(() => {
  const i = hover.value
  if (i === null) return null
  const rows = props.series
    .map((s) => ({ s, v: s.values[i] ?? null }))
    .filter((r): r is { s: ChartSeries; v: number } => r.v !== null && Number.isFinite(r.v))
  const x = cx(i)
  return {
    x,
    // Flip to the left of the crosshair in the right-hand third.
    left: x > w.value * 0.66,
    title: monthLabelWithYear(props.months[i] ?? ''),
    rows,
  }
})
</script>

<template>
  <div class="w-full">
    <!-- A legend whenever there is more than one series: identity is never colour alone. -->
    <ul v-if="series.length > 1" class="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-mf-muted">
      <li v-for="s in series" :key="s.key" class="flex items-center gap-1.5">
        <span
          v-if="s.type === 'column'"
          class="inline-block size-2.5 rounded-sm"
          :style="{ backgroundColor: s.color }"
        />
        <span v-else class="inline-block h-0.5 w-3 rounded-full" :style="{ backgroundColor: s.color }" />
        {{ s.label }}
      </li>
    </ul>

    <div
      ref="root"
      class="relative w-full outline-none focus-visible:ring-2 focus-visible:ring-mf-green/50"
      :style="{ height: `${height}px` }"
      tabindex="0"
      role="img"
      :aria-label="label"
      @pointermove="onMove"
      @pointerleave="hover = null"
      @keydown="onKey"
      @blur="hover = null"
    >
      <p v-if="empty" class="grid h-full place-items-center text-sm text-mf-muted">Not enough data yet.</p>

      <svg v-else :width="w" :height="height" class="block overflow-visible">
        <!-- Gridlines: solid hairlines, recessive; ticks round and comma'd. -->
        <g>
          <template v-for="t in ticks" :key="t">
            <line
              :x1="PAD.left"
              :x2="w - PAD.right"
              :y1="y(t)"
              :y2="y(t)"
              :stroke="t === 0 ? 'var(--color-mf-muted)' : 'var(--color-chart-grid)'"
              stroke-width="1"
              shape-rendering="crispEdges"
            />
            <text
              :x="PAD.left - 8"
              :y="y(t)"
              text-anchor="end"
              dominant-baseline="middle"
              class="fill-mf-muted text-[11px] tabular-nums"
            >
              {{ tickText(t) }}
            </text>
          </template>
        </g>

        <g>
          <template v-for="(m, i) in months" :key="m">
            <text
              v-if="labelled.has(i)"
              :x="cx(i)"
              :y="height - 6"
              text-anchor="middle"
              class="fill-mf-muted text-[11px]"
            >
              {{ monthTick(m, i === firstLabelled) }}
            </text>
          </template>
        </g>

        <!-- Crosshair, under the marks so it never hides a value. -->
        <line
          v-if="tooltip"
          :x1="tooltip.x"
          :x2="tooltip.x"
          :y1="PAD.top - 6"
          :y2="height - PAD.bottom"
          stroke="var(--color-mf-ink)"
          stroke-opacity="0.25"
          stroke-width="1"
        />

        <path v-for="c in columns" :key="c.key" :d="c.d" :fill="c.fill" />

        <g v-for="l in lines" :key="l.s.key">
          <path v-for="(d, k) in l.areas" :key="`a${k}`" :d="d" :fill="l.s.color" fill-opacity="0.1" />
          <path
            v-for="(d, k) in l.paths"
            :key="`l${k}`"
            :d="d"
            fill="none"
            :stroke="l.s.color"
            stroke-width="2"
            stroke-linejoin="round"
            stroke-linecap="round"
          />
          <circle
            v-for="p in l.singles"
            :key="`s${p.i}`"
            :cx="cx(p.i)"
            :cy="y(p.v)"
            r="4"
            :fill="l.s.color"
            stroke="var(--color-mf-surface)"
            stroke-width="2"
          />
          <circle
            v-if="l.last"
            :cx="cx(l.last.i)"
            :cy="y(l.last.v)"
            r="4"
            :fill="l.s.color"
            stroke="var(--color-mf-surface)"
            stroke-width="2"
          />
          <circle
            v-if="hover !== null && l.s.values[hover] != null"
            :cx="cx(hover)"
            :cy="y(l.s.values[hover]!)"
            r="5"
            :fill="l.s.color"
            stroke="var(--color-mf-surface)"
            stroke-width="2"
          />
        </g>

        <text
          v-if="end && hover === null"
          :x="end.x + 9"
          :y="end.y"
          dominant-baseline="middle"
          class="fill-mf-ink text-xs font-medium tabular-nums"
        >
          {{ end.text }}
        </text>
      </svg>

      <!-- One tooltip, every series at that month; value first, label second. -->
      <div
        v-if="tooltip && tooltip.rows.length"
        class="pointer-events-none absolute top-1 z-10 min-w-36 rounded-lg bg-mf-surface px-3 py-2 text-xs shadow-lg ring-1 ring-mf-muted/30"
        :style="
          tooltip.left
            ? { right: `${w - tooltip.x + 10}px` }
            : { left: `${tooltip.x + 10}px` }
        "
      >
        <p class="mb-1 text-mf-muted">{{ tooltip.title }}</p>
        <p v-for="r in tooltip.rows" :key="r.s.key" class="flex items-center gap-2 whitespace-nowrap">
          <span
            class="inline-block h-0.5 w-3 shrink-0 rounded-full"
            :style="{ backgroundColor: r.s.colorOf?.(r.v) ?? r.s.color }"
          />
          <span class="font-semibold text-mf-ink tabular-nums">{{ format(r.v) }}</span>
          <span class="text-mf-muted">{{ r.s.label }}</span>
        </p>
      </div>
    </div>
  </div>
</template>
