<script setup lang="ts">
import { dismissToast, useToasts } from '@/lib/toast'

const { toasts } = useToasts()

async function run(id: number, action: () => void | Promise<void>) {
  await action()
  dismissToast(id)
}
</script>

<template>
  <div
    class="pointer-events-none fixed inset-x-0 bottom-24 z-50 flex flex-col items-center gap-2 px-4"
    role="status"
    aria-live="polite"
  >
    <div
      v-for="toast in toasts"
      :key="toast.id"
      class="pointer-events-auto flex w-full max-w-md items-center gap-3 rounded-xl px-4 py-3 text-sm shadow-lg"
      :class="{
        'bg-state-within text-white': toast.tone === 'success',
        'bg-state-severe text-white': toast.tone === 'error',
        'bg-slate-800 text-white dark:bg-slate-700': toast.tone === 'info',
      }"
    >
      <span class="flex-1">{{ toast.message }}</span>
      <button
        v-if="toast.action"
        type="button"
        class="tap-target rounded-lg px-3 py-1 font-semibold underline decoration-2 underline-offset-2"
        @click="run(toast.id, toast.action.run)"
      >
        {{ toast.action.label }}
      </button>
      <button
        type="button"
        class="tap-target px-2 text-lg leading-none"
        :aria-label="'Dismiss: ' + toast.message"
        @click="dismissToast(toast.id)"
      >
        ×
      </button>
    </div>
  </div>
</template>
