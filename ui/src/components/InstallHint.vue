<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { isIOSSafari, isStandalone } from '@/lib/offline'

/**
 * iOS has no install prompt, so this is the only way a user learns the app can
 * live on the home screen. Shown once, on iOS Safari, only outside standalone —
 * and it states the limits plainly rather than hiding them (docs/08-ux.md §8.9).
 */
const DISMISSED = 'moneyapp.install-hint-dismissed'
const show = ref(false)

function dismiss() {
  show.value = false
  try {
    localStorage.setItem(DISMISSED, '1')
  } catch {
    // A private-mode Safari refuses to write. The hint reappearing is a smaller
    // problem than a crash.
  }
}

onMounted(() => {
  if (!isIOSSafari() || isStandalone()) return
  try {
    if (localStorage.getItem(DISMISSED)) return
  } catch {
    return
  }
  show.value = true
})
</script>

<template>
  <aside
    v-if="show"
    class="mx-4 mb-3 rounded-xl border border-brand-300 bg-brand-50 p-3 text-sm dark:border-brand-800 dark:bg-brand-900/20"
  >
    <p class="font-medium">Add MoneyApp to your home screen</p>
    <p class="mt-1 text-xs text-slate-600 dark:text-slate-300">
      Tap
      <span aria-hidden="true" class="mx-0.5 font-semibold">⎋</span>
      <span class="sr-only">Share</span>
      in Safari's toolbar, then <strong>Add to Home Screen</strong>. It opens without browser
      chrome.
    </p>
    <p class="mt-1 text-xs text-slate-500">
      iOS installs manually and sends no notifications; only the app shell is stored on the device,
      so nothing you record can be lost with it.
    </p>
    <button
      type="button"
      class="tap-target mt-2 rounded-lg border border-slate-300 px-3 py-1.5 text-xs dark:border-slate-700"
      @click="dismiss"
    >
      Got it
    </button>
  </aside>
</template>
