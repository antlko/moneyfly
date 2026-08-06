<script setup lang="ts">
import { ChevronLeft } from '@lucide/vue'
import { useRouter } from 'vue-router'

import { useIsDesktop } from '@/lib/breakpoint'

/**
 * The green bar every mobile screen that is not the dashboard wears — and,
 * at the desktop breakpoint, a plain title bar instead. Every screen that
 * uses this component gets both for free: there is deliberately no second
 * header component for secondary screens to opt into.
 *
 * It exists because four screens had each grown their own version: a bare
 * `<RouterLink>` reading "‹ Back", with the chevron borrowed from the body font
 * — so it sat on the text baseline instead of centred, at a different weight
 * from everything around it, in a hit target a few pixels tall. A real icon in a
 * 44px square fixes all three at once, and one component means the next screen
 * cannot grow a fifth variant.
 */
const props = defineProps<{
  title: string
  /**
   * What the back control does. Omitted, it steps back through history — so
   * returning from Accounts lands on the drawer you opened it from rather than
   * always on the dashboard. The record screen passes its own, because back
   * there means "return to the keypad", not "leave".
   */
  onBack?: () => void
  /** Where a deep link goes: a reload on this screen has no history to pop. */
  fallback?: string
}>()

const router = useRouter()
const isDesktop = useIsDesktop()

function back() {
  if (props.onBack) {
    props.onBack()
    return
  }
  if (window.history.length > 1) router.back()
  else void router.replace(props.fallback ?? '/')
}
</script>

<template>
  <!--
    No back chevron on desktop: DesktopShell's sidebar is always visible, so
    "back" has no meaning a highlighted nav link doesn't already give —
    unlike on the phone, this screen was never the only way in. Action-slot
    content (each view's own buttons) is unstyled for colour on purpose, so
    it inherits white here and mf-ink in the desktop bar without needing two
    versions of every button.
  -->
  <header v-if="isDesktop" class="mb-6 flex items-center gap-2">
    <h1 class="min-w-0 flex-1 truncate text-2xl font-semibold text-mf-ink">{{ title }}</h1>
    <slot name="actions" />
  </header>

  <header v-else class="shrink-0 bg-mf-green pt-safe-t text-white">
    <div class="flex h-14 items-center gap-1 px-1">
      <button
        type="button"
        class="grid size-11 shrink-0 place-items-center rounded-full"
        aria-label="Back"
        @click="back"
      >
        <ChevronLeft :size="26" :stroke-width="2" />
      </button>
      <h1 class="min-w-0 flex-1 truncate text-lg font-semibold">{{ title }}</h1>
      <slot name="actions" />
    </div>
  </header>
</template>
