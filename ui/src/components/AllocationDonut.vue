<script setup lang="ts">
import { computed } from 'vue'
import type { AllocationShare } from '@/api/types'
import { formatMoney, formatPercent } from '@/lib/money'

/**
 * The allocation doughnut from docs/08-ux.md §8.5.
 *
 * Drawn as inline SVG rather than through a charting library: the page's CSP is
 * self-only, everything is embedded in the binary, and one arc generator is less
 * code than the adapter would be. The slices are leaf accounts converted to base
 * currency by the server, so they sum to exactly 100% — the workbook's own
 * doughnut reached about 162%.
 */
const props = defineProps<{
  shares: AllocationShare[]
  selected: string | null
}>()

const emit = defineEmits<{ (e: 'select', assetClass: string | null): void }>()

const classColors: Record<string, string> = {
  cash: '#57BB8A',
  bank: '#4E8098',
  deposit: '#5FA8D3',
  investment: '#8E7DBE',
  metal: '#E8A33D',
  crypto: '#B5838D',
  other: '#7E8D85',
}

/** Grouped by asset class, which is the level the doughnut is readable at. */
const groups = computed(() => {
  const byClass = new Map<
    string,
    { assetClass: string; share: number; minor: number; currency: string; exponent: number }
  >()
  for (const s of props.shares) {
    const existing = byClass.get(s.asset_class)
    if (existing) {
      existing.share += s.share
      existing.minor += s.value.amount_minor
    } else {
      byClass.set(s.asset_class, {
        assetClass: s.asset_class,
        share: s.share,
        minor: s.value.amount_minor,
        currency: s.value.currency,
        exponent: s.value.exponent,
      })
    }
  }
  return [...byClass.values()].sort((a, b) => b.share - a.share)
})

/** Leaf accounts inside the selected class, for the drill-down. */
const drilldown = computed(() =>
  props.selected ? props.shares.filter((s) => s.asset_class === props.selected) : [],
)

const radius = 60
const stroke = 22
const circumference = 2 * Math.PI * radius

/** Arc offsets, accumulated so the slices meet without gaps. */
const arcs = computed(() => {
  let offset = 0
  return groups.value.map((g) => {
    const length = g.share * circumference
    const arc = {
      assetClass: g.assetClass,
      color: classColors[g.assetClass] ?? classColors.other,
      dash: `${length} ${circumference - length}`,
      offset: -offset,
      share: g.share,
    }
    offset += length
    return arc
  })
})

function label(assetClass: string): string {
  return assetClass.charAt(0).toUpperCase() + assetClass.slice(1)
}
</script>

<template>
  <div class="flex flex-col items-center gap-4 sm:flex-row">
    <svg
      :viewBox="`0 0 ${radius * 2 + stroke} ${radius * 2 + stroke}`"
      class="h-40 w-40 flex-none -rotate-90"
      role="img"
      :aria-label="`Allocation across ${groups.length} asset classes`"
    >
      <circle
        :cx="radius + stroke / 2"
        :cy="radius + stroke / 2"
        :r="radius"
        fill="none"
        stroke="currentColor"
        class="text-slate-200 dark:text-slate-800"
        :stroke-width="stroke"
      />
      <circle
        v-for="arc in arcs"
        :key="arc.assetClass"
        :cx="radius + stroke / 2"
        :cy="radius + stroke / 2"
        :r="radius"
        fill="none"
        :stroke="arc.color"
        :stroke-width="selected === arc.assetClass ? stroke + 4 : stroke"
        :stroke-dasharray="arc.dash"
        :stroke-dashoffset="arc.offset"
        class="cursor-pointer transition-[stroke-width]"
        @click="emit('select', selected === arc.assetClass ? null : arc.assetClass)"
      />
    </svg>

    <!-- The legend is the accessible version of the chart: never colour alone. -->
    <ul class="w-full flex-1 space-y-1 text-sm">
      <li v-for="group in groups" :key="group.assetClass">
        <button
          type="button"
          class="tap-target flex w-full items-center gap-2 rounded-lg px-2 py-1 text-left"
          :class="selected === group.assetClass ? 'bg-slate-100 dark:bg-slate-800' : ''"
          :aria-pressed="selected === group.assetClass"
          @click="emit('select', selected === group.assetClass ? null : group.assetClass)"
        >
          <span
            aria-hidden="true"
            class="h-3 w-3 flex-none rounded-full"
            :style="{ backgroundColor: classColors[group.assetClass] ?? classColors.other }"
          />
          <span class="flex-1 truncate">{{ label(group.assetClass) }}</span>
          <span class="money w-20">
            {{
              formatMoney({
                amount_minor: group.minor,
                currency: group.currency,
                exponent: group.exponent,
              })
            }}
          </span>
          <span class="money w-14 text-slate-500">{{ formatPercent(group.share, 1) }}</span>
        </button>

        <ul v-if="selected === group.assetClass" class="mt-1 space-y-0.5 pl-7 text-xs">
          <li
            v-for="account in drilldown"
            :key="account.account_id"
            class="flex items-center gap-2"
          >
            <span class="flex-1 truncate">{{ account.name }}</span>
            <span class="money w-20">{{ formatMoney(account.value) }}</span>
            <span class="money w-14 text-slate-500">{{ formatPercent(account.share, 1) }}</span>
          </li>
        </ul>
      </li>
    </ul>
  </div>
</template>
