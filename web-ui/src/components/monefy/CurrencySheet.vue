<script setup lang="ts">
import { Search } from '@lucide/vue'
import { computed, ref } from 'vue'

import { searchCurrencies } from '@/lib/currencies'

/**
 * Pick a currency to turn on.
 *
 * A search box over a shortlist rather than a `<select>` of every ISO code:
 * finding "HUF" in a list of 180 is worse than typing three letters. Anything
 * already enabled is shown as such instead of being hidden, so the list does not
 * silently change shape as you use it.
 */
const props = defineProps<{ enabled: string[] }>()
defineEmits<{ select: [string]; close: [] }>()

const query = ref('')
const results = computed(() => searchCurrencies(query.value))
const has = (code: string) => props.enabled.includes(code)
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex flex-col justify-end mf-scrim"
    @click.self="$emit('close')"
  >
    <section class="flex max-h-[80%] flex-col rounded-t-2xl bg-mf-bg">
      <header class="shrink-0 px-4 pt-2 pb-3">
        <div class="mx-auto mb-3 h-1 w-10 rounded-full bg-mf-muted" />
        <h2 class="mb-3 text-base font-medium">Add a currency</h2>
        <label class="flex items-center gap-2 rounded-full bg-mf-surface px-3 py-2">
          <Search :size="18" :stroke-width="2" class="shrink-0 text-mf-muted" />
          <input
            v-model="query"
            type="search"
            placeholder="Code or name"
            maxlength="24"
            class="w-full bg-transparent outline-none placeholder:text-mf-muted"
          />
        </label>
      </header>

      <ul
        class="min-h-0 flex-1 divide-y divide-mf-muted/20 overflow-y-auto pb-[calc(1rem+var(--spacing-safe-b))]"
      >
        <li v-for="option in results" :key="option.code">
          <button
            type="button"
            class="flex w-full items-center gap-3 px-4 py-3 text-left disabled:opacity-40"
            :disabled="has(option.code)"
            @click="$emit('select', option.code)"
          >
            <span class="w-12 shrink-0 font-medium">{{ option.code }}</span>
            <span class="min-w-0 flex-1 truncate text-sm text-mf-muted">{{ option.name }}</span>
            <span v-if="has(option.code)" class="shrink-0 text-xs text-mf-green-dark">added</span>
          </button>
        </li>
        <li v-if="!results.length" class="px-4 py-8 text-center text-sm text-mf-muted">
          Nothing matches. Any three-letter code works — type it in full.
        </li>
      </ul>
    </section>
  </div>
</template>
