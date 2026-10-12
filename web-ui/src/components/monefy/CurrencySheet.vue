<script setup lang="ts">
import { Search } from '@lucide/vue'
import { computed, onMounted, ref, useTemplateRef } from 'vue'

import { searchCurrencies } from '@/lib/currencies'
import { focusUnlessTouch } from '@/lib/keyboard'
import ModalSheet from './ModalSheet.vue'

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
const search = useTemplateRef<HTMLInputElement>('search')
onMounted(() => focusUnlessTouch(search.value))
const results = computed(() => searchCurrencies(query.value))
const has = (code: string) => props.enabled.includes(code)
</script>

<template>
  <ModalSheet label="Add a currency" @close="$emit('close')">
    <section class="flex min-h-0 flex-1 flex-col sm:h-[min(70vh,40rem)]">
      <header class="shrink-0 px-4 pt-2 pb-3 sm:pt-5">
        <div class="mx-auto mb-3 h-1 w-10 rounded-full bg-mf-muted sm:hidden" />
        <h2 class="mb-3 text-base font-medium">Add a currency</h2>
        <label class="flex items-center gap-2 rounded-full bg-mf-surface px-3 py-2">
          <Search :size="18" :stroke-width="2" class="shrink-0 text-mf-muted" />
          <input
            ref="search"
            v-model="query"
            type="search"
            placeholder="Code or name"
            maxlength="24"
            class="w-full bg-transparent outline-none placeholder:text-mf-muted"
          />
        </label>
      </header>

      <ul class="min-h-0 flex-1 divide-y divide-mf-muted/20 overflow-y-auto pb-4">
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
  </ModalSheet>
</template>
