<script setup lang="ts">
import { CalendarDays, ChevronDown } from '@lucide/vue'
import { useTemplateRef } from 'vue'

import { longDate, type DayKey } from '@/lib/period'

const props = defineProps<{ day: DayKey }>()
const emit = defineEmits<{ 'update:day': [DayKey] }>()

const input = useTemplateRef<HTMLInputElement>('input')

/**
 * Ask for the platform's own date picker — belt, and braces.
 *
 * On a real iPhone this call is usually redundant: tapping a genuine
 * `<input type="date">` opens the wheel by itself, no JavaScript involved,
 * *provided the tap lands on the input*. That "provided" is why this exists
 * at all and why the input below covers the whole row rather than sitting off
 * to one side — WebKit only honours a **direct** tap on the control itself.
 * `showPicker()` called from a click on some other, wrapping element (a
 * `<button>` the input merely happens to sit inside) is refused outright, no
 * error, nothing opens, which is indistinguishable from the row being dead.
 * That was this component's very first shape, and it is the bug this file has
 * now been rewritten twice to get out from under.
 *
 * Desktop Chrome and Firefox are the opposite problem: a direct tap on the
 * input opens nothing unless it lands exactly on the small built-in calendar
 * glyph, which `opacity-0` also hides. There `showPicker()` genuinely is
 * necessary, and — called from the input's own click, same element — it works.
 * One handler, attached to the one element real interaction happens on,
 * settles both platforms without asking which one the person is on.
 */
function open() {
  const el = input.value
  if (!el) return
  try {
    el.showPicker()
  } catch {
    el.click()
  }
}

/**
 * Read the picked date back out, on either event a browser might use to say
 * "committed".
 *
 * A `date` input is supposed to fire both — `input` as the value changes,
 * `change` once it is settled — but Android's picker for it is a full-screen
 * calendar dialog, not the compact stepper desktop shows, and committing it
 * with OK does not reliably raise `input` there. Listening for `input` alone
 * left that OK button looking dead: the dialog opened, a date could be tapped,
 * and nothing came back. Both handlers call this, so whichever the browser
 * actually sends is enough, and a browser that sends both just re-emits the
 * same value the second time.
 */
function commit(e: Event) {
  emit('update:day', (e.target as HTMLInputElement).value || props.day)
}
</script>

<template>
  <!--
    Not a <button>. The visible icon, date and chevron are decoration —
    `pointer-events-none`, so a tap passes straight through them — and the real
    input beneath is what actually receives it, sized to the whole row rather
    than to its own content. A bigger, forgiving target, and — the part that is
    load-bearing rather than nice-to-have — the element the tap lands on *is*
    the element whose picker opens, which is what a real iPhone insists on.
    Routing the tap through a separate wrapping button is the shape that reads
    as completely dead on one.

    `inset-0` alone does not do the sizing, and the gap is easy to miss because
    nothing looks wrong until a finger goes looking for the edges: a `<div>`
    with `absolute inset-0` fills its positioned ancestor, but `<input>` is a
    *replaced* element, and a replaced element keeps its own intrinsic size —
    here, zero — under `inset-0` regardless. `h-full w-full` is what actually
    stretches it.
  -->
  <div class="relative flex h-11 shrink-0 items-center justify-center gap-2 text-mf-ink">
    <CalendarDays :size="20" :stroke-width="1.7" class="pointer-events-none text-mf-green-dark" />
    <span class="pointer-events-none text-base">{{ longDate(day) }}</span>
    <ChevronDown :size="16" :stroke-width="2" class="pointer-events-none text-mf-muted" />
    <input
      ref="input"
      type="date"
      :value="day"
      class="absolute inset-0 h-full w-full cursor-pointer touch-manipulation opacity-0"
      aria-label="Date"
      @click="open"
      @input="commit"
      @change="commit"
    />
  </div>
</template>
