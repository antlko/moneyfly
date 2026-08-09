<script setup lang="ts">
import { X } from '@lucide/vue'
import { computed } from 'vue'

import { useIsDesktop } from '@/lib/breakpoint'
import { useNotifyStore } from '@/stores/notify'

/**
 * Where notices are actually drawn. See stores/notify.ts for what goes here and
 * what deliberately does not.
 *
 * Anchored top-right rather than bottom-centre: the bottom of a phone is the two
 * record buttons and the bottom of the desktop dashboard is content, and a
 * message that covers either is a message in the way. Every notice has a close
 * button — the previous ones did not, which is the complaint that prompted this.
 */
const isDesktop = useIsDesktop()
const notify = useNotifyStore()

// Errors are a desktop-only affordance; a phone screen has nowhere to put one
// that is not on top of the task, and the screens themselves show inline errors.
const visible = computed(() =>
  notify.notices.filter((n) => (n.kind === 'error' ? isDesktop.value : true)),
)
</script>

<template>
  <div
    v-if="visible.length"
    class="pointer-events-none fixed inset-x-0 top-0 z-50 flex flex-col items-center gap-2 p-3 pt-[calc(0.75rem+var(--spacing-safe-t))] sm:items-end"
    role="region"
    aria-label="Notifications"
  >
    <TransitionGroup name="mf-fade">
      <div
        v-for="n in visible"
        :key="n.id"
        :role="n.kind === 'error' ? 'alert' : 'status'"
        class="pointer-events-auto flex w-full max-w-sm items-start gap-3 rounded-xl px-3 py-2.5 text-sm shadow-lg"
        :class="
          n.kind === 'error'
            ? 'bg-mf-red-text text-white'
            : 'bg-mf-surface text-mf-text ring-1 ring-mf-muted/30'
        "
      >
        <span class="min-w-0 flex-1 break-words">{{ n.message }}</span>
        <button
          v-if="n.action"
          type="button"
          class="shrink-0 font-medium underline underline-offset-2"
          @click="n.action.run()"
        >
          {{ n.action.label }}
        </button>
        <button
          type="button"
          aria-label="Dismiss"
          class="-mr-1 shrink-0 rounded p-0.5 opacity-70 hover:opacity-100"
          @click="notify.dismiss(n.id)"
        >
          <X :size="16" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>
