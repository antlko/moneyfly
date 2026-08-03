<script setup lang="ts">
import { computed } from 'vue'

/**
 * The gradient area chart from docs/08-ux.md §8.4, drawn as inline SVG.
 *
 * Two things the workbook's charts got wrong and this one does not
 * (deviation D10):
 *
 *   - **Months are labelled.** All three sheet charts plotted against a bare
 *     index, so no point could be read back to a month.
 *   - **Incomplete months are excluded, not plotted as zero.** All three dived to
 *     the floor at the right edge because July was unfilled. A month with no data
 *     is simply not a point.
 *
 * The axis accommodates negative values, because `Saved %` reached -3.75 and that
 * is the most informative figure in the dataset.
 */
const props = withDefaults(
  defineProps<{
    points: { period: string; value: number | null }[]
    /** Rendered under the value in the tooltip title. */
    format?: (value: number) => string
    height?: number
    label?: string
  }>(),
  { height: 120, format: (v: number) => v.toFixed(2), label: 'Series' },
)

const width = 320
const pad = { top: 8, right: 4, bottom: 18, left: 4 }

/** Only recorded months are points. An absent month leaves a gap, by design. */
const recorded = computed(() =>
  props.points
    .map((p, index) => ({ ...p, index }))
    .filter((p): p is { period: string; value: number; index: number } => p.value !== null),
)

const bounds = computed(() => {
  const values = recorded.value.map((p) => p.value)
  if (values.length === 0) return { min: 0, max: 1 }
  let min = Math.min(...values, 0)
  let max = Math.max(...values, 0)
  if (min === max) {
    min -= 1
    max += 1
  }
  return { min, max }
})

const plotWidth = width - pad.left - pad.right
const plotHeight = computed(() => props.height - pad.top - pad.bottom)

function x(index: number): number {
  const span = Math.max(props.points.length - 1, 1)
  return pad.left + (index / span) * plotWidth
}

function y(value: number): number {
  const { min, max } = bounds.value
  const t = (value - min) / (max - min)
  return pad.top + (1 - t) * plotHeight.value
}

const line = computed(() =>
  recorded.value.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(p.index)},${y(p.value)}`).join(' '),
)

/** The area is closed on the zero line, so a negative stretch reads as negative. */
const area = computed(() => {
  if (recorded.value.length === 0) return ''
  const first = recorded.value[0]
  const last = recorded.value[recorded.value.length - 1]
  return `${line.value} L${x(last.index)},${y(0)} L${x(first.index)},${y(0)} Z`
})

const zeroLine = computed(() => y(0))

/** Every third month, so the labels do not collide at phone width. */
const labels = computed(() =>
  props.points
    .map((p, index) => ({ period: p.period, index }))
    .filter((_, index) => index % 3 === 0 || index === props.points.length - 1),
)

function monthLabel(period: string): string {
  const [year, month] = period.split('-').map(Number)
  return new Date(Date.UTC(year, month - 1, 1)).toLocaleDateString(undefined, {
    month: 'short',
    timeZone: 'UTC',
  })
}
</script>

<template>
  <figure class="w-full">
    <svg
      :viewBox="`0 0 ${width} ${height}`"
      class="w-full"
      role="img"
      :aria-label="`${label}: ${recorded.length} recorded month${recorded.length === 1 ? '' : 's'}`"
      preserveAspectRatio="none"
    >
      <defs>
        <linearGradient id="series-fill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="currentColor" stop-opacity="0.35" />
          <stop offset="100%" stop-color="currentColor" stop-opacity="0.02" />
        </linearGradient>
      </defs>

      <!-- The zero line, so a negative Saved % is visibly below it. -->
      <line
        :x1="pad.left"
        :x2="width - pad.right"
        :y1="zeroLine"
        :y2="zeroLine"
        stroke="currentColor"
        stroke-opacity="0.2"
        stroke-dasharray="2 3"
      />

      <path v-if="area" :d="area" fill="url(#series-fill)" stroke="none" />
      <path
        v-if="line"
        :d="line"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linejoin="round"
        stroke-linecap="round"
        vector-effect="non-scaling-stroke"
      />

      <circle
        v-for="p in recorded"
        :key="p.period"
        :cx="x(p.index)"
        :cy="y(p.value)"
        r="2.5"
        fill="currentColor"
      >
        <title>{{ p.period }}: {{ format(p.value) }}</title>
      </circle>
    </svg>

    <!-- Month labels, not a bare index. -->
    <figcaption class="mt-1 flex justify-between text-[10px] text-slate-400">
      <span v-for="l in labels" :key="l.period">{{ monthLabel(l.period) }}</span>
    </figcaption>
  </figure>
</template>
