<script setup lang="ts">
import { computed, ref } from 'vue'

import { currencyName } from '@/lib/currencies'
import { STORAGE_BASE } from '@/lib/fx'
import { today } from '@/lib/period'

/**
 * Type a rate in by hand.
 *
 * The provider chain is not the whole world: a currency no free feed publishes,
 * a rate a household has agreed between themselves, or the rate actually paid at
 * a counter. Without somewhere to enter one, records in those currencies sit
 * outside every total indefinitely, captioned "no exchange rate yet" with
 * nothing to be done about it.
 *
 * It is phrased the way the rest of the world phrases it — "1 EUR = ___ HUF" —
 * because that is what is written on the board at the exchange desk, and asking
 * someone to invert it in their head before typing is how the wrong number gets
 * entered.
 */
const props = defineProps<{
  code: string
  /** The rate currently in force, as a decimal string, if there is one. */
  current?: string
}>()
const emit = defineEmits<{ save: [{ rate: string; asOf: string }]; close: [] }>()

const rate = ref(props.current ?? '')
/*
 * Dated, and defaulting to today.
 *
 * A rate is a fact about a day. Entering last week's rate today must record it
 * against last week, or the past re-prices itself — and every screen showing a
 * past month would quietly change the moment this was saved.
 */
const asOf = ref(today())

/** Only a positive decimal. The server checks too; this says so before the trip. */
const valid = computed(() => /^\d+(\.\d+)?$/.test(rate.value.trim()) && Number(rate.value) > 0)

function submit() {
  if (!valid.value) return
  emit('save', { rate: rate.value.trim(), asOf: asOf.value })
}
</script>

<template>
  <div class="fixed inset-0 z-50 flex flex-col justify-end mf-scrim" @click.self="emit('close')">
    <form
      class="space-y-4 rounded-t-2xl bg-mf-bg p-4 pb-[calc(1rem+var(--spacing-safe-b))]"
      @submit.prevent="submit"
    >
      <div class="mx-auto h-1 w-10 rounded-full bg-mf-muted" />

      <div>
        <h2 class="text-lg font-medium">Set the {{ code }} rate</h2>
        <p class="text-sm text-mf-muted">{{ currencyName(code) }}</p>
      </div>

      <label class="flex items-center gap-3">
        <span class="shrink-0 text-base">1 {{ STORAGE_BASE }} =</span>
        <input
          v-model="rate"
          type="text"
          inputmode="decimal"
          autocomplete="off"
          placeholder="391.5"
          maxlength="20"
          class="min-w-0 flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 text-right text-lg tabular-nums outline-none focus:border-mf-green"
        />
        <span class="shrink-0 text-base">{{ code }}</span>
      </label>

      <label class="flex items-center justify-between gap-3 text-sm">
        <span class="text-mf-muted">On</span>
        <input
          v-model="asOf"
          type="date"
          class="rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2"
        />
      </label>

      <p class="text-xs text-mf-muted">
        This is used for records on that date and after it, in place of the fetched rate. A newer
        published rate takes over again — for a currency nobody publishes, yours keeps applying.
      </p>

      <div class="flex gap-3 pt-1">
        <button
          type="button"
          class="flex-1 rounded-full border border-mf-muted py-2.5 text-mf-ink"
          @click="emit('close')"
        >
          Cancel
        </button>
        <button
          type="submit"
          class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white disabled:opacity-40"
          :disabled="!valid"
        >
          Save
        </button>
      </div>
    </form>
  </div>
</template>
