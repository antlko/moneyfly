<script setup lang="ts">
import { ArrowDown } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import AmountDisplay from '@/components/monefy/AmountDisplay.vue'
import AmountKeypad from '@/components/monefy/AmountKeypad.vue'
import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import DateRow from '@/components/monefy/DateRow.vue'
import { display, initialState, press, total, type Key } from '@/lib/calculator'
import { exponent, toMinor } from '@/lib/money'
import { today } from '@/lib/period'
import { useDashboardStore } from '@/stores/dashboard'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'

const router = useRouter()
const taxonomy = useTaxonomyStore()
const dashboard = useDashboardStore()

const accounts = computed(() => taxonomy.activeAccounts)
const fromId = ref(String(accounts.value[0]?.id ?? ''))
const toId = ref(String(accounts.value[1]?.id ?? accounts.value[0]?.id ?? ''))

const from = computed(() => accounts.value.find((a) => String(a.id) === fromId.value))
const to = computed(() => accounts.value.find((a) => String(a.id) === toId.value))

const calc = ref(initialState())
const day = ref(today())
const note = ref('')

const currency = computed(() => String(from.value?.currency ?? dashboard.baseCurrency))
const toCurrency = computed(() => String(to.value?.currency ?? currency.value))
/** Only asked for when the two sides differ; otherwise the rate is 1 by definition. */
const crossCurrency = computed(() => currency.value !== toCurrency.value)
const received = ref('')

const amount = computed(() => display(calc.value))
const valid = computed(
  () => total(calc.value) > 0 && fromId.value !== '' && toId.value !== '' && fromId.value !== toId.value,
)

const key = (pressed: Key) => {
  calc.value = press(calc.value, pressed, exponent(currency.value))
}

/**
 * A transfer is **one row**, not a linked pair.
 *
 * A pair could arrive half-applied on another device — money leaving one account
 * and never reaching the other — and no amount of care on the client prevents
 * that, because the two rows sync independently. One row cannot tear.
 */
async function save() {
  if (!valid.value) return
  const sent = toMinor(total(calc.value), currency.value)
  const arrived = crossCurrency.value
    ? toMinor(Number(received.value) || 0, toCurrency.value)
    : sent

  await sync.write('txn', {
    kind: 'transfer',
    occurredOn: day.value,
    accountId: fromId.value,
    toAccountId: toId.value,
    // Negative on the way out, positive on the way in. A cross-currency
    // transfer's rate is not recoverable later, so both sides are stored.
    amountMinor: -sent,
    currency: currency.value,
    toAmountMinor: arrived,
    toCurrency: toCurrency.value,
    note: note.value.trim(),
  })

  dashboard.goToDay(day.value)
  await router.replace('/')
}

function swap() {
  const previous = fromId.value
  fromId.value = toId.value
  toId.value = previous
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <header class="bg-mf-green px-2 pt-safe-t text-white">
      <div class="flex h-14 items-center">
        <button type="button" class="px-2 py-2 text-base" @click="router.replace('/')">
          Cancel
        </button>
        <p class="flex-1 text-center text-lg font-semibold">Transfer</p>
        <span class="w-16" />
      </div>
    </header>

    <DateRow v-model:day="day" />

    <div v-if="accounts.length < 2" class="m-4 rounded-lg bg-mf-green-soft/30 p-4 text-sm">
      A transfer needs two accounts. Add another one under Accounts first.
    </div>

    <template v-else>
      <div class="mx-3 space-y-2">
        <label class="block">
          <span class="mb-1 block text-xs text-mf-muted">From</span>
          <div class="flex items-center gap-2 rounded-lg border border-mf-green-soft bg-mf-surface/60 px-3 py-2">
            <CategoryIcon :icon="from?.icon" :color="from?.color" :size="24" />
            <select v-model="fromId" class="w-full bg-transparent outline-none">
              <option v-for="a in accounts" :key="a.id" :value="String(a.id)">
                {{ a.name }} · {{ a.currency }}
              </option>
            </select>
          </div>
        </label>

        <div class="flex justify-center">
          <button
            type="button"
            aria-label="Swap accounts"
            class="grid h-8 w-8 place-items-center rounded-full bg-mf-green-soft/50 text-mf-green-dark"
            @click="swap"
          >
            <ArrowDown :size="18" :stroke-width="2" />
          </button>
        </div>

        <label class="block">
          <span class="mb-1 block text-xs text-mf-muted">To</span>
          <div class="flex items-center gap-2 rounded-lg border border-mf-green-soft bg-mf-surface/60 px-3 py-2">
            <CategoryIcon :icon="to?.icon" :color="to?.color" :size="24" />
            <select v-model="toId" class="w-full bg-transparent outline-none">
              <option v-for="a in accounts" :key="a.id" :value="String(a.id)">
                {{ a.name }} · {{ a.currency }}
              </option>
            </select>
          </div>
        </label>
      </div>

      <div class="mt-3">
        <AmountDisplay :amount="amount" :currency="currency" @backspace="key('backspace')" />
      </div>

      <label v-if="crossCurrency" class="mx-3 mt-3 flex items-center gap-2 text-sm">
        <span class="shrink-0 text-mf-muted">Received ({{ toCurrency }})</span>
        <input
          v-model="received"
          type="number"
          step="0.01"
          class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
        />
      </label>

      <label class="mx-3 mt-3 flex items-center gap-2 border-b border-mf-muted/60 pb-2">
        <span class="text-mf-muted">✎</span>
        <input
          v-model="note"
          type="text"
          placeholder="Add note"
          maxlength="140"
          class="w-full bg-transparent outline-none placeholder:text-mf-muted"
        />
      </label>

      <div
        class="flex min-h-0 flex-1 flex-col justify-end gap-3 pb-[calc(0.75rem+var(--spacing-safe-b))]"
      >
        <AmountKeypad @press="key" />
        <div class="px-3">
          <button
            type="button"
            :disabled="!valid"
            class="w-full rounded-lg border border-mf-green-soft bg-mf-surface/60 py-3.5 text-base tracking-wide text-mf-green-dark uppercase disabled:opacity-40"
            @click="save"
          >
            Transfer
          </button>
        </div>
      </div>
    </template>
  </div>
</template>
