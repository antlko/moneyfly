<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import ModalSheet from '@/components/monefy/ModalSheet.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import { exponent, toMajor, toMinor } from '@/lib/money'
import {
  addMonths,
  currentMonth,
  monthBounds,
  monthLabel,
  monthLabelWithYear,
  type MonthKey,
} from '@/lib/period'
import { isLiquid, useAccountsStore } from '@/stores/accounts'
import { useCapitalStore } from '@/stores/capital'
import { useDashboardStore } from '@/stores/dashboard'
import { useFxStore } from '@/stores/fx'
import type { Row } from '@/sync/types'

/**
 * Check in a month: what each account held at its end.
 *
 * The spreadsheet column, as a form. Every field starts filled in with the best
 * guess there is, so a month where nothing surprising happened is one click:
 *
 *   * a figure already typed for this month, if there is one;
 *   * otherwise the last figure typed in, **plus whatever the records say
 *     moved through the account since** — for an account nobody records
 *     against (gold, a deposit) that is simply the last figure carried forward;
 *   * failing that, what the ledger says the account held at the month's end.
 *
 * Not the raw ledger balance, even for the current month. The ledger is
 * opening balance + records, and most people never typed an opening balance —
 * their cash account reads −€35,000 after a year of recorded spending. The last
 * check-in plus the records since is right whether the opening balance was
 * ever entered or not.
 *
 * Where the ledger and the field disagree, the ledger's figure is offered next
 * to it, one click away — that gap is exactly the spending nobody wrote down.
 */
const props = defineProps<{ month: MonthKey }>()
const emit = defineEmits<{ close: [] }>()

const capital = useCapitalStore()
const accounts = useAccountsStore()
const dashboard = useDashboardStore()
const fx = useFxStore()

const month = ref(props.month)
const isCurrent = computed(() => month.value === currentMonth())
const setBalances = ref(true)

/** What the ledger says each account held — now, or at the end of a past month. */
const ledger = computed(() =>
  isCurrent.value ? accounts.balances : accounts.balancesAt(monthBounds(month.value).to),
)

const groups = computed(() => accounts.groups)
const values = ref<Record<string, string>>({})

const major = (minor: number, currency: string) => String(toMajor(minor, currency))

/** Ledger balances at the end of past months, computed once per month per dialog. */
const ledgerEnds = new Map<MonthKey, Map<string, number>>()
function ledgerAtEndOf(m: MonthKey): Map<string, number> {
  let balances = ledgerEnds.get(m)
  if (!balances) {
    balances = accounts.balancesAt(monthBounds(m).to)
    ledgerEnds.set(m, balances)
  }
  return balances
}

function initialFor(account: Row): string {
  const id = String(account.id)
  const currency = String(account.currency)
  const own = capital.entered(id, month.value)
  if (own) return major(own.amountMinor, own.currency)
  const recorded = accounts.hasRecords.has(id)
  const ledgerValue = ledger.value.get(id) ?? 0
  const carried = capital.heldOf(id, month.value)
  if (carried) {
    // What the records moved between that check-in and this month's end.
    const moved = recorded ? ledgerValue - (ledgerAtEndOf(carried.from).get(id) ?? 0) : 0
    return major(carried.amountMinor + moved, carried.currency)
  }
  if (recorded || isCurrent.value) return major(ledgerValue, currency)
  return ''
}

watch(
  month,
  () => {
    const next: Record<string, string> = {}
    for (const g of groups.value) for (const a of g.accounts) next[String(a.id)] = initialFor(a)
    values.value = next
  },
  { immediate: true },
)

const parsed = (account: Row): number | null => {
  const raw = (values.value[String(account.id)] ?? '').trim()
  if (raw === '') return null
  const n = Number(raw)
  return Number.isFinite(n) ? toMinor(n, String(account.currency)) : null
}

/** The ledger's figure, when it differs from what is in the field. */
function ledgerHint(account: Row): number | null {
  const id = String(account.id)
  if (!accounts.hasRecords.has(id)) return null
  const value = ledger.value.get(id) ?? 0
  return parsed(account) === value ? null : value
}

/** Put the ledger's figure in the field — a named handler, see CLAUDE.md on template casts. */
function applyLedger(account: Row) {
  const value = ledgerHint(account)
  if (value !== null) values.value[String(account.id)] = major(value, String(account.currency))
}

function carriedFrom(account: Row): MonthKey | null {
  const id = String(account.id)
  if (capital.entered(id, month.value)) return null
  const held = capital.heldOf(id, month.value)
  return held?.carried ? held.from : null
}

/** The month's total as typed, in the base currency, priced the way the table prices it. */
const totals = computed(() => {
  const day = capital.pricingDay(month.value)
  let total = 0
  let ready = 0
  for (const g of groups.value) {
    for (const a of g.accounts) {
      const minor = parsed(a)
      if (minor === null) continue
      const currency = String(a.currency)
      const inBase =
        currency === dashboard.baseCurrency ? minor : fx.convert(minor, currency, dashboard.baseCurrency, day)
      if (inBase === null) continue
      total += inBase
      if (isLiquid(a)) ready += inBase
    }
  }
  return { total, ready }
})

const saving = ref(false)
async function save() {
  saving.value = true
  try {
    const entries = groups.value.flatMap((g) =>
      g.accounts.map((a) => ({
        accountId: String(a.id),
        amountMinor: parsed(a),
        currency: String(a.currency),
      })),
    )
    await capital.saveMonth(month.value, entries, isCurrent.value && setBalances.value)
    emit('close')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <ModalSheet size="lg" label="Check in a month" @close="emit('close')">
    <form class="flex min-h-0 flex-col" @submit.prevent="save">
      <header class="sticky top-0 z-10 border-b border-mf-muted/20 bg-mf-bg px-4 pt-4 pb-3 sm:px-6">
        <div class="flex items-center gap-2">
          <h2 class="min-w-0 flex-1 text-base font-medium sm:text-lg">What did you have?</h2>
          <button
            type="button"
            class="grid size-8 place-items-center rounded-full hover:bg-mf-surface"
            aria-label="Previous month"
            @click="month = addMonths(month, -1)"
          >
            <ChevronLeft :size="18" />
          </button>
          <span class="min-w-28 text-center text-sm font-medium">{{ monthLabelWithYear(month) }}</span>
          <button
            type="button"
            class="grid size-8 place-items-center rounded-full hover:bg-mf-surface disabled:opacity-30"
            aria-label="Next month"
            :disabled="isCurrent"
            @click="month = addMonths(month, 1)"
          >
            <ChevronRight :size="18" />
          </button>
        </div>
        <p class="mt-1 text-xs text-mf-muted">
          {{
            isCurrent
              ? 'What each account holds now.'
              : `What each account held at the end of ${monthLabel(month)}.`
          }}
          Leave a field empty to carry the previous month's figure.
        </p>
      </header>

      <div class="space-y-4 px-4 py-4 sm:px-6">
        <section v-for="g in groups" :key="g.name">
          <h3 v-if="groups.length > 1" class="mb-1 text-xs font-medium tracking-wide text-mf-muted uppercase">
            {{ g.name }}
          </h3>
          <ul class="divide-y divide-mf-muted/20 rounded-xl bg-mf-surface">
            <li v-for="a in g.accounts" :key="String(a.id)" class="flex items-center gap-3 px-3 py-2">
              <CategoryIcon :icon="a.icon" :color="a.color" :size="22" />
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm">{{ a.name }}</p>
                <p class="text-xs text-mf-muted">
                  <template v-if="carriedFrom(a)">
                    {{ monthLabel(carriedFrom(a)!) }}'s figure{{
                      accounts.hasRecords.has(String(a.id)) ? ' + records since' : ', carried'
                    }}
                  </template>
                  <template v-else-if="!isLiquid(a)">Not ready to use</template>
                  <template v-if="ledgerHint(a) !== null">
                    <template v-if="carriedFrom(a) || !isLiquid(a)"> · </template>
                    Records say
                    <button type="button" class="text-mf-green-dark underline" title="Use this figure" @click="applyLedger(a)">
                      <MoneyAmount :minor="ledgerHint(a)!" :currency="String(a.currency)" />
                    </button>
                  </template>
                </p>
              </div>
              <input
                v-model="values[String(a.id)]"
                type="number"
                :step="exponent(String(a.currency)) === 0 ? '1' : '0.01'"
                :aria-label="`${a.name} at the end of ${monthLabel(month)}`"
                class="w-28 rounded-lg border border-mf-muted/60 bg-mf-surface px-2.5 py-1.5 text-right tabular-nums outline-none focus:border-mf-green sm:w-36"
              />
              <span class="w-9 shrink-0 text-xs text-mf-muted">{{ a.currency }}</span>
            </li>
          </ul>
        </section>
      </div>

      <footer class="sticky bottom-0 space-y-3 border-t border-mf-muted/20 bg-mf-bg px-4 py-3 sm:px-6">
        <div class="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-sm">
          <span class="text-mf-muted">Total</span>
          <MoneyAmount :minor="totals.total" :currency="dashboard.baseCurrency" class="font-semibold" />
          <span class="text-mf-muted">Ready to use</span>
          <MoneyAmount :minor="totals.ready" :currency="dashboard.baseCurrency" class="font-semibold" />
        </div>
        <label v-if="isCurrent" class="flex items-start gap-2 text-sm">
          <input v-model="setBalances" type="checkbox" class="mt-0.5 accent-[var(--color-mf-green)]" />
          <span>
            Also make these the accounts' current balances
            <span class="block text-xs text-mf-muted">
              Moves each opening balance so your records add up to the figure above.
            </span>
          </span>
        </label>
        <div class="flex gap-3">
          <button type="button" class="flex-1 rounded-full border border-mf-muted py-2.5" @click="emit('close')">
            Cancel
          </button>
          <button
            type="submit"
            :disabled="saving"
            class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white disabled:opacity-50"
          >
            Save {{ monthLabel(month) }}
          </button>
        </div>
      </footer>
    </form>
  </ModalSheet>
</template>
