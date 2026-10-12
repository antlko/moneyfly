<script setup lang="ts">
import { useEventListener } from '@vueuse/core'

/**
 * The frame every sheet sits in: a **bottom sheet on a phone, a centred dialog
 * on a desktop**.
 *
 * Each sheet used to draw its own scrim and its own full-width panel pinned to
 * the bottom edge. On a phone that is right — the thumb is down there. In the
 * desktop layout the same markup became a strip two thousand pixels wide along
 * the bottom of a monitor, with a dozen category chips in one row and a Cancel
 * button the size of a banner. One frame, one breakpoint, and every sheet gets
 * both shapes without knowing about either.
 *
 * Wrap it in `<Transition name="mf-sheet">` at the call site as before: the
 * panel is this element's only child, which is what `.mf-sheet-* > *` animates
 * (a slide up on a phone, a short rise and fade on a desktop — tailwind.css).
 */
withDefaults(
  defineProps<{
    /** The dialog's width at the desktop breakpoint. */
    size?: 'sm' | 'md' | 'lg' | 'xl'
    label?: string
  }>(),
  { size: 'md', label: undefined },
)
const emit = defineEmits<{ close: [] }>()

const WIDTHS = {
  sm: 'sm:max-w-sm',
  md: 'sm:max-w-lg',
  lg: 'sm:max-w-2xl',
  xl: 'sm:max-w-4xl',
} as const

// Escape closes, as every desktop dialog does. Harmless on a phone, which has
// no Escape key to press.
useEventListener(window, 'keydown', (e: KeyboardEvent) => {
  if (e.key === 'Escape') emit('close')
})
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex flex-col justify-end mf-scrim sm:items-center sm:justify-center sm:p-6"
    @click.self="emit('close')"
  >
    <div
      role="dialog"
      aria-modal="true"
      :aria-label="label"
      class="flex max-h-[85%] w-full flex-col overflow-y-auto rounded-t-2xl bg-mf-bg pb-[var(--spacing-safe-b)] sm:max-h-[min(88vh,56rem)] sm:rounded-2xl sm:pb-0 sm:shadow-2xl"
      :class="WIDTHS[size]"
    >
      <slot />
    </div>
  </div>
</template>
