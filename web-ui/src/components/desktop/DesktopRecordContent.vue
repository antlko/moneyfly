<script setup lang="ts">
import { useMediaQuery } from '@vueuse/core'
import { Banknote, LayoutGrid, Save } from '@lucide/vue'
import { onMounted, useTemplateRef } from 'vue'

import AmountKeypad from '@/components/monefy/AmountKeypad.vue'
import CategoryGrid from '@/components/monefy/CategoryGrid.vue'
import DateRow from '@/components/monefy/DateRow.vue'
import { keyFromEvent, parsePasted, type CalcState, type Key } from '@/lib/calculator'
import type { DayKey } from '@/lib/period'
import type { Row } from '@/sync/types'

/**
 * The record screen at the desktop breakpoint: amount, note and the whole
 * category grid on one screen, driven from the keyboard.
 *
 * Purely presentational — `RecordView` owns the state and the save, so a
 * record written here is written by exactly the code the phone uses. What this
 * adds is the input model: the amount is a focused field that feeds physical
 * keys into the *same calculator reducer* as the on-screen keypad (`12+3.5`
 * works, decimals are limited to the currency's exponent), rather than a
 * second text-parsing path that could disagree with what is saved.
 *
 * A record costs: type the amount, click a category — or Enter, then arrow
 * keys and Enter again. Esc leaves.
 */
const props = defineProps<{
  amount: string
  currency: string
  accountName: string
  decimals: number
  categories: Row[]
  /** The category already on the record, if any; Enter saves with it. */
  selectedId?: string
  hasAmount: boolean
  roundingNote: string
  /** A sheet is open over this screen — Esc belongs to it, not to us. */
  sheetOpen: boolean
}>()

const emit = defineEmits<{
  key: [Key]
  set: [CalcState]
  select: [Row]
  confirm: []
  create: []
  'pick-account': []
  back: []
}>()

const note = defineModel<string>('note', { required: true })
const day = defineModel<DayKey>('day', { required: true })

const amountEl = useTemplateRef<HTMLInputElement>('amountEl')
const gridEl = useTemplateRef<HTMLElement>('gridEl')

// A touch screen wide enough for this layout has no physical keyboard to type
// the amount with, so it keeps the on-screen keypad.
const coarse = useMediaQuery('(pointer: coarse)')

onMounted(() => amountEl.value?.focus())

function onAmountKey(e: KeyboardEvent) {
  const k = keyFromEvent(e)
  if (k === null) return
  e.preventDefault()
  if (k === 'enter') emit('confirm')
  else emit('key', k)
}

function onPaste(e: ClipboardEvent) {
  e.preventDefault()
  const next = parsePasted(e.clipboardData?.getData('text') ?? '', props.decimals)
  if (next) emit('set', next)
}

/** Enter with no category chosen hands focus to the grid. */
function focusGrid() {
  gridEl.value?.querySelector<HTMLElement>('[data-category]')?.focus()
}
defineExpose({ focusGrid })

/** Arrow keys move between tiles; Enter/Space then press the focused one natively. */
function onGridKey(e: KeyboardEvent) {
  const tiles = [...(gridEl.value?.querySelectorAll<HTMLElement>('[data-category]') ?? [])]
  const at = tiles.indexOf(document.activeElement as HTMLElement)
  if (at < 0) return
  const grid = gridEl.value?.querySelector('[data-category-grid]')
  const cols = grid ? getComputedStyle(grid).gridTemplateColumns.split(' ').length : 4
  const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -cols, ArrowDown: cols }[e.key]
  if (step === undefined) return
  e.preventDefault()
  tiles[Math.min(tiles.length - 1, Math.max(0, at + step))]?.focus()
}

/** Back to the amount from anywhere on the screen, so typing never needs a click. */
function onScreenKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    if (props.sheetOpen) return
    e.preventDefault()
    emit('back')
    return
  }
  // A digit typed while focus is on a tile or nowhere belongs to the amount.
  const target = e.target as HTMLElement | null
  if (target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || props.sheetOpen) return
  const k = keyFromEvent(e)
  if (k !== null && k !== 'enter' && /^[\d.]$/.test(k)) {
    e.preventDefault()
    amountEl.value?.focus()
    emit('key', k)
  }
}
</script>

<template>
  <div class="flex flex-col gap-4" @keydown="onScreenKey">
    <div class="flex items-center gap-4 rounded-xl bg-mf-green px-4 py-4 text-white">
      <button
        type="button"
        class="flex shrink-0 flex-col items-center gap-0.5 border-r border-white/40 pr-4"
        aria-label="Choose account"
        @click="emit('pick-account')"
      >
        <Banknote :size="30" :stroke-width="1.5" />
        <span class="text-xs tracking-wide">{{ currency }}</span>
        <span v-if="accountName" class="max-w-20 truncate text-[11px] text-white/80">{{
          accountName
        }}</span>
      </button>

      <!--
        Read-only on purpose: every key is routed through the calculator, so
        the field can never hold something `toMinor` would round differently.
        It is still a real input, which is what gets focus, Tab order and paste.
      -->
      <input
        ref="amountEl"
        :value="amount"
        readonly
        inputmode="none"
        autocomplete="off"
        aria-label="Amount"
        class="min-w-0 flex-1 bg-transparent text-right text-5xl font-light tabular-nums outline-none"
        @keydown="onAmountKey"
        @paste="onPaste"
      />
    </div>
    <p v-if="roundingNote" class="-mt-2 text-center text-xs text-mf-red-text">
      {{ roundingNote }}
    </p>

    <div class="flex items-center gap-4">
      <label class="flex min-w-0 flex-1 items-center gap-2 border-b border-mf-muted/60 pb-2">
        <span class="text-mf-muted">✎</span>
        <input
          v-model="note"
          type="text"
          placeholder="Add note"
          maxlength="140"
          aria-label="Note"
          class="w-full bg-transparent outline-none placeholder:text-mf-muted"
        />
      </label>
      <div class="w-56 shrink-0 rounded-lg border border-mf-green-soft bg-mf-surface/60">
        <DateRow v-model:day="day" />
      </div>
    </div>

    <div v-if="coarse" class="-mx-3">
      <AmountKeypad :decimals="decimals" @press="emit('key', $event)" />
    </div>

    <div ref="gridEl" class="-mx-3" @keydown="onGridKey">
      <p class="mb-2 flex items-center gap-2 px-3 text-sm text-mf-muted">
        <LayoutGrid :size="16" :stroke-width="1.8" />
        Pick a category to save
      </p>
      <CategoryGrid
        dense
        :categories="categories"
        :selected-id="selectedId"
        @select="emit('select', $event)"
        @create="emit('create')"
      />
    </div>

    <div class="flex items-center justify-end gap-3">
      <span class="text-xs text-mf-muted">Enter to save · Esc to cancel</span>
      <button
        type="button"
        :disabled="!hasAmount"
        class="flex items-center gap-2 rounded-lg border border-mf-green-soft bg-mf-surface/60 px-5 py-2.5 text-mf-green-dark uppercase disabled:opacity-40"
        @click="emit('confirm')"
      >
        <Save :size="18" :stroke-width="1.8" />
        {{ selectedId ? 'Save' : 'Choose category' }}
      </button>
    </div>
  </div>
</template>
