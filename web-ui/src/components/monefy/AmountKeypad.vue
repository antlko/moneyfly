<script setup lang="ts">
import type { Key } from '@/lib/calculator'

const emit = defineEmits<{ press: [Key] }>()

// The reference layout, read straight off the record screen: digits in a 3-wide
// block with the operators down the right, and `. 0 = /` on the bottom row.
const ROWS: Key[][] = [
  ['1', '2', '3', '+'],
  ['4', '5', '6', '-'],
  ['7', '8', '9', '*'],
  ['.', '0', '=', '/'],
]

// A key that does not visibly react has been pressed twice by the time anyone
// is sure. There is no hover on a phone and no travel under the finger, so the
// press *is* the whole feedback — hence a shrink and a fill, fast enough
// (75ms) to land inside the tap rather than after it.
//
// The glyphs on the keys are not the values behind them: a hyphen-minus and an
// asterisk are what the reducer understands, but − and × are what belong on a
// button.
const GLYPHS: Partial<Record<Key, string>> = { '-': '−', '*': '×', '/': '÷' }
const glyph = (key: Key) => GLYPHS[key] ?? key
</script>

<template>
  <div class="grid grid-cols-4 gap-2 px-3">
    <button
      v-for="key in ROWS.flat()"
      :key="key"
      type="button"
      class="rounded-lg border border-mf-green-soft bg-mf-surface/60 py-3.5 text-2xl font-light text-mf-ink transition-[transform,background-color] duration-75 active:scale-95 active:bg-mf-green-soft/70"
      @click="emit('press', key)"
    >
      {{ glyph(key) }}
    </button>
  </div>
</template>
