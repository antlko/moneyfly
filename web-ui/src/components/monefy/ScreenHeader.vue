<script setup lang="ts">
import { ChevronLeft } from '@lucide/vue'
import { useRouter } from 'vue-router'

/**
 * The green bar every screen that is not the dashboard wears.
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
  <header class="shrink-0 bg-mf-green pt-safe-t text-white">
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
