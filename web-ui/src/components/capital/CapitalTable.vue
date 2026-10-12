<script setup lang="ts">
import { ChevronDown, CircleHelp } from '@lucide/vue'
import { computed, nextTick, onMounted, ref, useTemplateRef, watch } from 'vue'

import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import { percent } from '@/lib/chart'
import { currentMonth, monthLabel, type MonthKey } from '@/lib/period'
import type { AccountGroup } from '@/stores/accounts'
import type { CapitalMonth } from '@/stores/capital'

/**
 * The spreadsheet, as a table: places down the side, months across, the
 * latest on the right.
 *
 * It is also the table view behind every chart on the page — each line and bar
 * up there is a row here — so nothing the charts show needs a hover to read.
 *
 * Group rows are subtotals in the base currency and fold open to their
 * accounts, which are shown in their own currencies. A figure carried over
 * from an earlier month is drawn faint and italic: it is a placeholder, not a
 * check-in.
 */
const props = defineProps<{
  columns: CapitalMonth[]
  groups: AccountGroup[]
  currency: string
}>()
const emit = defineEmits<{ pick: [MonthKey] }>()

/** Groups shown open. All of them while the table is small enough to read whole. */
const open = ref(new Set<string>())
watch(
  () => props.groups,
  (groups) => {
    const accounts = groups.reduce((sum, g) => sum + g.accounts.length, 0)
    if (accounts <= 10) open.value = new Set(groups.map((g) => g.name))
  },
  { immediate: true },
)
function toggle(name: string) {
  const next = new Set(open.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  open.value = next
}

/** Averages over complete months with any recorded flow — the spreadsheet's AVG column. */
const averages = computed(() => {
  const done = props.columns.filter((c) => c.month !== currentMonth())
  const mean = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : null)
  const withIncome = done.filter((c) => c.incomeMinor > 0)
  const withSpending = done.filter((c) => c.spentMinor > 0)
  return {
    income: mean(withIncome.map((c) => c.incomeMinor)),
    spent: mean(withSpending.map((c) => c.spentMinor)),
    saved: mean(done.map((c) => c.savedShare).filter((v): v is number => v !== null)),
  }
})

const scroller = useTemplateRef<HTMLElement>('scroller')
/** Start scrolled to the latest months — that is what anyone opens this to see. */
const toEnd = () =>
  void nextTick(() => {
    if (scroller.value) scroller.value.scrollLeft = scroller.value.scrollWidth
  })
onMounted(toEnd)
watch(() => props.columns.length, toEnd)

const signClass = (v: number | null) =>
  v === null ? 'text-mf-muted' : v < 0 ? 'text-mf-red-text' : 'text-mf-green-dark'

const cell = 'px-3 py-1.5 text-right whitespace-nowrap tabular-nums'
// The label column stays put while months scroll under it; its hairline edge
// is what stops a half-hidden figure from reading as part of the label.
const head =
  'sticky left-0 z-10 bg-mf-surface px-3 py-1.5 text-left whitespace-nowrap shadow-[1px_0_0_var(--color-chart-grid)]'
</script>

<template>
  <div ref="scroller" class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-mf-muted/25 text-xs text-mf-muted">
          <th :class="head" class="font-normal">In {{ currency }}</th>
          <th v-for="c in columns" :key="c.month" class="px-1 py-1.5 font-normal">
            <!-- The month is the way to check it in. -->
            <button
              type="button"
              class="flex w-full flex-col items-end rounded-md px-2 py-0.5 hover:bg-mf-bg hover:text-mf-ink"
              :title="`Check in ${monthLabel(c.month)}`"
              @click="emit('pick', c.month)"
            >
              <span :class="c.month === currentMonth() && 'font-medium text-mf-ink'">{{
                monthLabel(c.month).slice(0, 3)
              }}</span>
              <span class="flex items-center gap-1 text-[10px]">
                {{ c.month.slice(0, 4) }}
                <span
                  class="inline-block size-1.5 rounded-full"
                  :class="c.checkedIn ? 'bg-mf-green' : 'bg-mf-muted/50'"
                  :title="c.checkedIn ? 'Checked in' : 'Not checked in'"
                />
              </span>
            </button>
          </th>
          <th class="px-3 py-1.5 text-right font-normal">Average</th>
        </tr>
      </thead>

      <tbody>
        <template v-for="g in groups" :key="g.name">
          <tr class="border-b border-mf-muted/10">
            <th :class="head" class="font-medium">
              <button type="button" class="flex items-center gap-1" @click="toggle(g.name)">
                <ChevronDown
                  :size="14"
                  class="text-mf-muted transition-transform"
                  :class="!open.has(g.name) && '-rotate-90'"
                />
                {{ g.name }}
              </button>
            </th>
            <td v-for="c in columns" :key="c.month" :class="cell" class="font-medium">
              <MoneyAmount
                v-if="c.byGroup.has(g.name)"
                :minor="c.byGroup.get(g.name)!"
                :currency="currency"
              />
              <span v-else class="text-mf-muted">—</span>
            </td>
            <td />
          </tr>
          <template v-if="open.has(g.name)">
            <tr v-for="a in g.accounts" :key="String(a.id)" class="border-b border-mf-muted/10 text-mf-ink/80">
              <th :class="head" class="pl-8 font-normal">
                {{ a.name }}
                <span v-if="a.archived" class="text-xs text-mf-muted">(archived)</span>
              </th>
              <td v-for="c in columns" :key="c.month" :class="cell">
                <MoneyAmount
                  v-if="c.accounts.get(String(a.id))"
                  :minor="c.accounts.get(String(a.id))!.held.amountMinor"
                  :currency="c.accounts.get(String(a.id))!.held.currency"
                  :class="c.accounts.get(String(a.id))!.held.carried && 'text-mf-muted italic'"
                  :title="
                    c.accounts.get(String(a.id))!.held.carried
                      ? `Carried from ${monthLabel(c.accounts.get(String(a.id))!.held.from)}`
                      : undefined
                  "
                />
                <span v-else class="text-mf-muted">—</span>
              </td>
              <td />
            </tr>
          </template>
        </template>

        <tr class="border-t border-mf-muted/40">
          <th :class="head" class="font-medium">Ready to use</th>
          <td v-for="c in columns" :key="c.month" :class="cell">
            <MoneyAmount v-if="c.readyMinor !== null" :minor="c.readyMinor" :currency="currency" />
            <span v-else class="text-mf-muted">—</span>
          </td>
          <td />
        </tr>
        <tr class="border-b border-mf-muted/25">
          <th :class="head" class="font-semibold">Total</th>
          <td v-for="c in columns" :key="c.month" :class="cell" class="font-semibold">
            <MoneyAmount v-if="c.totalMinor !== null" :minor="c.totalMinor" :currency="currency" />
            <span v-else class="text-mf-muted">—</span>
          </td>
          <td />
        </tr>
        <tr class="border-b border-mf-muted/10">
          <th :class="head" class="font-normal">Change</th>
          <td v-for="c in columns" :key="c.month" :class="[cell, signClass(c.changeMinor)]">
            <MoneyAmount v-if="c.changeMinor !== null" :minor="c.changeMinor" :currency="currency" />
            <span v-else>—</span>
          </td>
          <td />
        </tr>

        <tr class="border-b border-mf-muted/10">
          <th :class="head" class="font-normal">Income</th>
          <td v-for="c in columns" :key="c.month" :class="cell">
            <MoneyAmount :minor="c.incomeMinor" :currency="currency" />
          </td>
          <td :class="cell" class="text-mf-muted">
            <MoneyAmount v-if="averages.income !== null" :minor="Math.round(averages.income)" :currency="currency" />
          </td>
        </tr>
        <tr class="border-b border-mf-muted/10">
          <th :class="head" class="font-normal">Spending</th>
          <td v-for="c in columns" :key="c.month" :class="cell">
            <MoneyAmount :minor="c.spentMinor" :currency="currency" />
          </td>
          <td :class="cell" class="text-mf-muted">
            <MoneyAmount v-if="averages.spent !== null" :minor="Math.round(averages.spent)" :currency="currency" />
          </td>
        </tr>
        <tr class="border-b border-mf-muted/10">
          <th :class="head" class="font-normal">Saved</th>
          <td v-for="c in columns" :key="c.month" :class="[cell, signClass(c.savedShare)]">
            {{ c.savedShare === null ? '—' : percent(c.savedShare) }}
          </td>
          <td :class="cell" class="text-mf-muted">
            {{ averages.saved === null ? '' : percent(averages.saved) }}
          </td>
        </tr>
        <tr class="border-b border-mf-muted/10">
          <th :class="head" class="font-normal">
            <span
              class="flex items-center gap-1"
              title="The change in what you hold that your records do not explain: spending nobody wrote down, fees, and things changing value on their own (gold, investments, exchange rates). Change − (income − spending)."
            >
              Unexplained <CircleHelp :size="13" class="text-mf-muted" />
            </span>
          </th>
          <td v-for="c in columns" :key="c.month" :class="[cell, signClass(c.otherMinor)]">
            <MoneyAmount v-if="c.otherMinor !== null" :minor="c.otherMinor" :currency="currency" />
            <span v-else>—</span>
          </td>
          <td />
        </tr>
        <tr>
          <th :class="head" class="font-normal">
            <span
              class="flex items-center gap-1"
              title="How many months what is ready to use would last at your average monthly spending (the six complete months before)."
            >
              Runway <CircleHelp :size="13" class="text-mf-muted" />
            </span>
          </th>
          <td v-for="c in columns" :key="c.month" :class="cell">
            {{ c.runwayMonths === null ? '—' : `${c.runwayMonths.toFixed(1)} mo` }}
          </td>
          <td />
        </tr>
      </tbody>
    </table>
  </div>
</template>
