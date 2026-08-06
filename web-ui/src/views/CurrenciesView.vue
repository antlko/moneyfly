<script setup lang="ts">
import { Plus, RefreshCw, X } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { toast } from 'vue-sonner'

import * as http from '@/api/http'
import CurrencySheet from '@/components/monefy/CurrencySheet.vue'
import RateSheet from '@/components/monefy/RateSheet.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { currencyName } from '@/lib/currencies'
import { STORAGE_BASE, type Ratio } from '@/lib/fx'
import { exponent } from '@/lib/money'
import { useDashboardStore } from '@/stores/dashboard'
import { useFxStore } from '@/stores/fx'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useTaxonomyStore } from '@/stores/taxonomy'

const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()
const settings = useSettingsStore()
const fx = useFxStore()

const showPicker = ref(false)

/**
 * The currencies this person has turned on.
 *
 * A synced setting, so the list follows them between devices. The base currency
 * is always in it — everything is quoted in that, so it cannot be turned off.
 */
const declared = computed<string[]>(() => settings.get(SETTING.currencies, []))
const enabled = computed(() => {
  const codes = new Set<string>([dashboard.baseCurrency, ...declared.value])
  // Anything already in the data belongs on the list whether it was declared or
  // not, otherwise an account imported from another device would show a currency
  // this screen claims is off.
  for (const a of taxonomy.activeAccounts) codes.add(String(a.currency ?? ''))
  return [...codes].filter((c) => c.length === 3).sort()
})

/**
 * Turn a currency on and wait for its rate.
 *
 * The setting is pushed first: that is what tells the server this currency now
 * matters, and the server goes and fetches it. `addQuote` then waits for that to
 * land rather than asking once and reporting "no rate yet" for a currency that
 * is seconds away from having one.
 */
async function add(code: string) {
  showPicker.value = false
  if (enabled.value.includes(code)) return
  await settings.set(SETTING.currencies, [...declared.value, code].sort())
  if (!(await fx.addQuote(code, dashboard.baseCurrency))) {
    toast(`No rate for ${code} yet — it will arrive with the next update`)
  }
}

/**
 * Turning a currency off only removes it from the list of offers.
 *
 * Records already in it keep their currency and keep converting — deleting a
 * currency out from under existing money would be a data-loss button dressed as
 * a preference.
 */
async function remove(code: string) {
  if (code === dashboard.baseCurrency) return
  if (taxonomy.activeAccounts.some((a) => String(a.currency) === code)) {
    toast(`${code} is in use by an account`)
    return
  }
  await settings.set(
    SETTING.currencies,
    declared.value.filter((c) => c !== code),
  )
}

onMounted(() => void fx.refresh(dashboard.baseCurrency, enabled.value))
// A currency arriving by sync from another device should fetch its rate here
// too, without waiting for the next visit. `add` does its own waiting fetch, so
// this only has to cover arrivals it did not initiate.
watch(enabled, (codes) => void fx.refresh(dashboard.baseCurrency, codes))

/** A rate rendered the way a person reads it: 1 EUR = 363.94 HUF. */
const asDecimal = (rate: Ratio) =>
  (Number(rate.num) / Number(rate.den)).toLocaleString(undefined, { maximumFractionDigits: 4 })

const rateFor = (code: string) => fx.latest.find((r) => r.quote === code)

/**
 * Enter a rate by hand.
 *
 * The tap target is the rate itself, including when it reads "no rate yet" —
 * that is the moment someone wants this, and hiding the way out of it behind a
 * separate control would leave the screen saying "there is no rate" while
 * offering nothing to do about it.
 */
const editing = ref<string | null>(null)

/** The rate in force for a code, as the decimal string the sheet edits. */
function currentRate(code: string): string | undefined {
  const held = rateFor(code)
  if (!held) return undefined
  return String(Number(held.rate.num) / Number(held.rate.den))
}

/*
 * A named handler, not `saveRate(editing!, $event)` in the template: a handler
 * carrying a TypeScript assertion does not compile, and `vue-tsc` does not catch
 * it — the dev server does, at request time, by rendering a blank page.
 */
function onSaveRate(entry: { rate: string; asOf: string }) {
  const code = editing.value
  if (code) void saveRate(code, entry)
}

async function saveRate(code: string, { rate, asOf }: { rate: string; asOf: string }) {
  editing.value = null
  try {
    await http.fxSetRate(code, rate, asOf)
  } catch (e) {
    toast(e instanceof Error ? `Could not save the rate: ${e.message}` : 'Could not save the rate')
    return
  }
  // Pull it straight back into the local cache, because that — not the server —
  // is what every total on the dashboard reads.
  await fx.addQuote(code, dashboard.baseCurrency)
  toast(`1 ${STORAGE_BASE} = ${rate} ${code}`)
}

/**
 * How stale is too stale. Rates do not move at weekends, so a couple of days is
 * normal; a week means the refresh has been failing and nobody noticed, which is
 * the failure this screen exists to surface.
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
          @click="fx.refresh(dashboard.baseCurrency, enabled)"
        >
          <RefreshCw :size="20" :stroke-width="2" :class="fx.refreshing && 'animate-spin'" />
        </button>
        <button
          type="button"
          class="grid size-11 place-items-center"
          aria-label="Add a currency"
          @click="showPicker = true"
        >
          <Plus :size="22" :stroke-width="2" />
        </button>
      </template>
    </ScreenHeader>

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <section class="rounded-2xl bg-mf-surface p-4">
        <h2 class="mb-1 font-medium">Base currency</h2>
        <p class="text-2xl">{{ dashboard.baseCurrency }}</p>
        <p class="mt-2 text-sm text-mf-muted">
          Every total is shown in this currency. A record keeps the currency of the account that
          paid for it and is converted at the rate on the day it happened.
        </p>
      </section>

      <section class="rounded-2xl bg-mf-surface">
        <div class="flex items-baseline justify-between p-4 pb-2">
          <h2 class="font-medium">Your currencies</h2>
          <span class="text-xs text-mf-muted">1 {{ STORAGE_BASE }} =</span>
        </div>

        <ul class="divide-y divide-mf-muted/20">
          <li v-for="code in enabled" :key="code" class="flex items-center gap-3 px-4 py-3">
            <div class="min-w-0 flex-1">
              <p class="flex items-baseline gap-2">
                <span class="font-medium">{{ code }}</span>
                <span class="truncate text-sm text-mf-muted">{{ currencyName(code) }}</span>
              </p>
              <p class="text-xs text-mf-muted">
                {{ exponent(code) }} decimal{{ exponent(code) === 1 ? '' : 's' }}
              </p>
            </div>

            <div v-if="code === dashboard.baseCurrency" class="shrink-0 text-sm text-mf-muted">
              base
            </div>
            <!--
              The rate is the button. "No rate yet" is precisely when someone
              wants to type one, so the way to do that has to be on the words
              that say there isn't one — not behind a control somewhere else.
            -->
            <button
              v-else
              type="button"
              class="shrink-0 rounded-lg px-2 py-1 text-right active:bg-mf-green-soft/30"
              :aria-label="`Set the ${code} rate`"
              @click="editing = code"
            >
              <template v-if="rateFor(code)">
                <span class="block text-sm">{{ asDecimal(rateFor(code)!.rate) }}</span>
                <!--
                  The age is the point. A dead provider raises no error anywhere —
                  it just stops moving, and every total quietly keeps using an old
                  number.
                -->
                <span
                  class="block text-xs"
                  :class="
                    rateFor(code)!.ageDays >= STALE_AFTER_DAYS ? 'text-mf-red-text' : 'text-mf-muted'
                  "
                >
                  {{ rateFor(code)!.ageDays === 0 ? 'today' : `${rateFor(code)!.ageDays}d old` }}
                </span>
              </template>
              <span v-else class="block text-xs text-mf-muted">
                {{ fx.refreshing ? 'fetching…' : 'no rate yet — tap to set' }}
              </span>
            </button>

            <button
              v-if="code !== dashboard.baseCurrency"
              type="button"
              class="grid size-8 shrink-0 place-items-center rounded-full text-mf-muted"
              :aria-label="`Remove ${code}`"
              @click="remove(code)"
            >
              <X :size="18" :stroke-width="2" />
            </button>
          </li>
        </ul>

        <p class="px-4 pt-2 pb-4 text-sm text-mf-muted">
          Adding a currency here is what makes it available when you create an account, and what
          starts its rate updating daily.
        </p>
      </section>

      <p v-if="fx.lastError" class="rounded-2xl bg-mf-red/15 p-4 text-sm text-mf-red-text">
        Could not reach the server: {{ fx.lastError }}. Anything below is what this device already
        had, and conversion carries on using it.
      </p>
    </main>

    <Transition name="mf-sheet">
      <CurrencySheet
        v-if="showPicker"
        :enabled="enabled"
        @select="add"
        @close="showPicker = false"
      />
    </Transition>

    <Transition name="mf-sheet">
      <RateSheet
        v-if="editing"
        :key="editing"
        :code="editing"
        :current="currentRate(editing)"
        @save="onSaveRate"
        @close="editing = null"
      />
    </Transition>
  </div>
</template>
