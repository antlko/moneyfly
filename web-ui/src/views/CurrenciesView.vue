<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { computed, onMounted } from 'vue'

import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { STORAGE_BASE } from '@/lib/fx'
import { exponent } from '@/lib/money'
import { useDashboardStore } from '@/stores/dashboard'
import { useFxStore } from '@/stores/fx'
import { useTaxonomyStore } from '@/stores/taxonomy'

const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()
const fx = useFxStore()

onMounted(() => void fx.refresh(dashboard.baseCurrency))

/** The currencies this replica actually uses — the ones worth having a rate for. */
const inUse = computed(() => {
  const codes = new Set<string>([dashboard.baseCurrency])
  for (const a of taxonomy.activeAccounts) codes.add(String(a.currency ?? ''))
  return [...codes].filter((c) => c.length === 3).sort()
})

/** A rate rendered the way a person reads it: 1 EUR = 363.94 HUF. */
const asDecimal = (rate: { num: bigint; den: bigint }) =>
  (Number(rate.num) / Number(rate.den)).toLocaleString(undefined, { maximumFractionDigits: 4 })

/**
 * How stale is too stale. Rates do not move at weekends, so a couple of days is
 * normal; a week means the refresh has been failing and nobody noticed, which
 * is the failure this screen exists to surface.
 */
const STALE_AFTER_DAYS = 5
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Currencies">
      <template #actions>
        <button
          type="button"
          class="grid size-11 place-items-center"
          aria-label="Refresh rates"
          :disabled="fx.refreshing"
          @click="fx.refresh(dashboard.baseCurrency)"
        >
          <RefreshCw :size="20" :stroke-width="2" :class="fx.refreshing && 'animate-spin'" />
        </button>
      </template>
    </ScreenHeader>

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))]">
      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-1 font-medium">Base currency</h2>
        <p class="text-2xl">{{ dashboard.baseCurrency }}</p>
        <p class="mt-2 text-sm text-mf-muted">
          Every total on the dashboard is shown in this currency. Records keep the currency of the
          account that paid for them, and are converted at the rate on the day they happened.
        </p>
      </section>

      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-2 font-medium">In use</h2>
        <ul class="space-y-1 text-sm">
          <li v-for="code in inUse" :key="code" class="flex justify-between">
            <span>{{ code }}</span>
            <span class="text-mf-muted">
              {{ exponent(code) }} decimal{{ exponent(code) === 1 ? '' : 's' }}
            </span>
          </li>
        </ul>
      </section>

      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-1 font-medium">Rates</h2>
        <p class="mb-3 text-sm text-mf-muted">
          Stored against {{ STORAGE_BASE }} and cached on this device, so conversion works offline.
        </p>

        <p v-if="fx.lastError" class="mb-3 text-sm text-mf-red-text">
          Could not reach the server: {{ fx.lastError }}. The rates below are what this device
          already had.
        </p>

        <ul class="space-y-2 text-sm">
          <li
            v-for="r in fx.latest"
            :key="r.quote"
            class="flex items-baseline justify-between gap-3"
          >
            <span class="shrink-0"
              >1 {{ STORAGE_BASE }} = {{ asDecimal(r.rate) }} {{ r.quote }}</span
            >
            <!--
              The age is the point of this list. A dead provider does not throw
              an error anywhere — it just stops moving, and every total quietly
              keeps using last month's number.
            -->
            <span
              class="shrink-0 text-xs"
              :class="r.ageDays >= STALE_AFTER_DAYS ? 'text-mf-red-text' : 'text-mf-muted'"
            >
              {{ r.ageDays === 0 ? 'today' : `${r.ageDays}d old` }}
            </span>
          </li>
          <li v-if="!fx.latest.length" class="py-4 text-center text-mf-muted">
            No rates cached yet. They arrive the first time this device is online with a
            foreign-currency account or record.
          </li>
        </ul>
      </section>
    </main>
  </div>
</template>
