<script setup lang="ts">
import { Banknote } from '@lucide/vue'
import { ref, useTemplateRef } from 'vue'

import type { Period, PeriodKind } from '@/lib/period'
import type { Row } from '@/sync/types'
import AppDrawer from './AppDrawer.vue'
import CategoryIcon from './CategoryIcon.vue'

const props = defineProps<{
  period: Period
  accountLabel: string
  currency: string
  accounts: Row[]
  selected: string[]
}>()
const emit = defineEmits<{
  close: []
  kind: [PeriodKind]
  interval: [string, string]
  goToDay: [string]
  selectAccounts: [string[]]
  manageAccounts: []
}>()

const showAccounts = ref(false)

/**
 * Toggle one account in the filter.
 *
 * An empty selection means *all* accounts, not none — so unticking the last one
 * returns to showing everything rather than to an empty screen, which is what a
 * literal reading would give and what nobody wants.
 */
function toggleAccount(id: string) {
  const next = props.selected.includes(id)
    ? props.selected.filter((x) => x !== id)
    : [...props.selected, id]
  emit('selectAccounts', next.length === props.accounts.length ? [] : next)
}

/** The reference's order, and its wording. */
const KINDS: { kind: PeriodKind; label: string }[] = [
  { kind: 'day', label: 'Day' },
  { kind: 'week', label: 'Week' },
  { kind: 'month', label: 'Month' },
  { kind: 'year', label: 'Year' },
  { kind: 'all', label: 'All' },
]

const showInterval = ref(false)
const from = ref(props.period.anchor)
const to = ref(props.period.until ?? props.period.anchor)

function applyInterval() {
  // Accept the two dates in either order rather than refusing: dragging a range
  // backwards is a normal thing to do and means the same thing.
  const [start, end] = from.value <= to.value ? [from.value, to.value] : [to.value, from.value]
  emit('interval', start, end)
  emit('close')
}

function pick(kind: PeriodKind) {
  emit('kind', kind)
  emit('close')
}

/**
 * Jump to the period containing a chosen day, keeping the kind — picking a date
 * while on Year should show that year, not that day.
 *
 * A named handler rather than an inline expression: the template compiler
 * cannot parse two statements when one of them carries a TypeScript cast, and
 * `vue-tsc` does not catch it — only the dev server does, at run time.
 */
function manageAccounts() {
  emit('manageAccounts')
  emit('close')
}

const dateInput = useTemplateRef<HTMLInputElement>('dateInput')

/** See DateRow: an overlaid `<input type="date">` does not open on desktop. */
function openDatePicker() {
  const el = dateInput.value
  if (!el) return
  try {
    el.showPicker()
  } catch {
    el.click()
  }
}

function chooseDate(event: Event) {
  const value = (event.target as HTMLInputElement).value
  emit('goToDay', value || props.period.anchor)
  emit('close')
}
</script>

<template>
  <AppDrawer side="left" @close="emit('close')">
    <div class="space-y-3 p-3">
      <button
        type="button"
        class="flex w-full items-center gap-3 rounded-lg border border-mf-green-soft bg-mf-surface/60 px-3 py-3 text-left"
        @click="showAccounts = !showAccounts"
      >
        <Banknote :size="30" :stroke-width="1.5" class="shrink-0 text-mf-green-dark" />
        <span class="min-w-0">
          <span class="block truncate text-base">{{ accountLabel }}</span>
          <span class="block text-xs text-mf-muted">{{ currency }}</span>
        </span>
      </button>

      <div v-if="showAccounts" class="space-y-1 rounded-lg bg-mf-green-soft/25 p-2 text-sm">
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded px-2 py-2 text-left"
          :class="selected.length === 0 && 'font-medium text-mf-green-dark'"
          @click="emit('selectAccounts', [])"
        >
          <span class="w-5">{{ selected.length === 0 ? '✓' : '' }}</span>
          All accounts
        </button>
        <button
          v-for="account in accounts"
          :key="account.id"
          type="button"
          class="flex w-full items-center gap-2 rounded px-2 py-2 text-left"
          :class="selected.includes(String(account.id)) && 'font-medium text-mf-green-dark'"
          @click="toggleAccount(String(account.id))"
        >
          <span class="w-5">{{ selected.includes(String(account.id)) ? '✓' : '' }}</span>
          <CategoryIcon :icon="account.icon" :color="account.color" :size="20" />
          <span class="min-w-0 flex-1 truncate">{{ account.name }}</span>
          <span class="text-xs text-mf-muted">{{ account.currency }}</span>
        </button>
        <button
          type="button"
          class="w-full rounded px-2 py-2 text-left text-mf-green-dark"
          @click="manageAccounts"
        >
          Manage accounts…
        </button>
      </div>

      <div class="space-y-2 pt-1">
        <button
          v-for="option in KINDS"
          :key="option.kind"
          type="button"
          class="w-full rounded-lg border py-3 text-base transition"
          :class="
            period.kind === option.kind
              ? 'border-mf-green bg-mf-green font-medium text-white'
              : 'border-mf-green-soft bg-mf-surface/60 text-mf-ink'
          "
          @click="pick(option.kind)"
        >
          {{ option.label }}
        </button>

        <button
          type="button"
          class="w-full rounded-lg border py-3 text-base"
          :class="
            period.kind === 'interval'
              ? 'border-mf-green bg-mf-green font-medium text-white'
              : 'border-mf-green-soft bg-mf-surface/60 text-mf-ink'
          "
          @click="showInterval = !showInterval"
        >
          Interval
        </button>

        <div v-if="showInterval" class="space-y-2 rounded-lg bg-mf-green-soft/25 p-3">
          <label class="flex items-center justify-between gap-2 text-sm">
            <span class="text-mf-muted">From</span>
            <input
              v-model="from"
              type="date"
              class="rounded border border-mf-muted/60 bg-mf-surface px-2 py-1"
            />
          </label>
          <label class="flex items-center justify-between gap-2 text-sm">
            <span class="text-mf-muted">To</span>
            <input
              v-model="to"
              type="date"
              class="rounded border border-mf-muted/60 bg-mf-surface px-2 py-1"
            />
          </label>
          <button
            type="button"
            class="w-full rounded-full bg-mf-green py-2 text-sm font-medium text-white"
            @click="applyInterval"
          >
            Apply
          </button>
        </div>
      </div>

      <!--
        "Choose date" jumps to the period containing a day without changing which
        kind of period is selected — picking a date while on Year should show
        that year, not that day.
      -->
      <button
        type="button"
        class="relative mt-2 block w-full rounded-lg border border-mf-green-soft bg-mf-surface/60 py-3 text-center text-base"
        @click="openDatePicker"
      >
        Choose date
        <!-- Same reasoning as DateRow: an overlaid input does not open on desktop. -->
        <input
          ref="dateInput"
          type="date"
          :value="period.anchor"
          class="pointer-events-none absolute size-0 opacity-0"
          tabindex="-1"
          aria-label="Choose date"
          @input="chooseDate"
        />
      </button>
    </div>
  </AppDrawer>
</template>
