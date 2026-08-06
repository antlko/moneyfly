<script setup lang="ts">
import { ChevronLeft, Search, SlidersHorizontal } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import TransactionRow from '@/components/monefy/TransactionRow.vue'
import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { toMinor } from '@/lib/money'
import { useDashboardStore } from '@/stores/dashboard'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'

const taxonomy = useTaxonomyStore()
const dashboard = useDashboardStore()
const router = useRouter()

/*
 * Search runs against the local replica, so it works with no connection and
 * returns as you type. There is no server endpoint for it and there should not
 * be — every row is already here.
 */
const all = useLiveQuery<Row[]>(() => db.txn.where('deleted').equals(0).toArray(), [])

const query = ref('')
const showFilters = ref(false)
const categoryId = ref('')
const accountId = ref('')
const from = ref('')
const to = ref('')
const minAmount = ref('')
const maxAmount = ref('')

const results = computed(() => {
  const text = query.value.trim().toLowerCase()
  const min =
    minAmount.value === '' ? null : toMinor(Number(minAmount.value), dashboard.baseCurrency)
  const max =
    maxAmount.value === '' ? null : toMinor(Number(maxAmount.value), dashboard.baseCurrency)

  return all.value
    .filter((row) => {
      if (categoryId.value && String(row.categoryId ?? '') !== categoryId.value) return false
      if (accountId.value && String(row.accountId ?? '') !== accountId.value) return false
      if (from.value && String(row.occurredOn) < from.value) return false
      if (to.value && String(row.occurredOn) > to.value) return false

      // Amounts are compared by magnitude: nobody searching for "over 50"
      // means "greater than minus fifty".
      const size = Math.abs(Number(row.amountMinor ?? 0))
      if (min !== null && size < min) return false
      if (max !== null && size > max) return false

      if (!text) return true
      const category = taxonomy.byId.get(String(row.categoryId ?? ''))
      return (
        String(row.note ?? '')
          .toLowerCase()
          .includes(text) ||
        String(category?.name ?? '')
          .toLowerCase()
          .includes(text)
      )
    })
    .sort((a, b) => String(b.occurredOn).localeCompare(String(a.occurredOn)))
})

const totalMinor = computed(() =>
  results.value.reduce((sum, r) => sum + Number(r.amountMinor ?? 0), 0),
)

/** Tapping a result opens it for editing — the row itself is the control. */
const openRecord = (row: Row) => router.push(`/edit/${row.id}`)

function clear() {
  query.value = ''
  categoryId.value = ''
  accountId.value = ''
  from.value = ''
  to.value = ''
  minAmount.value = ''
  maxAmount.value = ''
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <header class="shrink-0 bg-mf-green pt-safe-t text-white">
      <div class="flex h-14 items-center gap-1 px-1">
        <!--
          The search field takes the place of the title, so this screen keeps its
          own header rather than bending ScreenHeader's slot into that shape —
          but the back control has to be the identical 44px icon button.
        -->
        <button
          type="button"
          class="grid size-11 shrink-0 place-items-center rounded-full"
          aria-label="Back"
          @click="router.back()"
        >
          <ChevronLeft :size="26" :stroke-width="2" />
        </button>
        <div class="flex flex-1 items-center gap-2 rounded-full bg-white/20 px-3 py-1.5">
          <Search :size="18" :stroke-width="2" />
          <input
            v-model="query"
            type="search"
            placeholder="Search notes and categories"
            class="w-full bg-transparent text-white outline-none placeholder:text-white/70"
          />
        </div>
        <button
          type="button"
          aria-label="Filters"
          class="p-2"
          :class="showFilters && 'opacity-70'"
          @click="showFilters = !showFilters"
        >
          <SlidersHorizontal :size="20" :stroke-width="2" />
        </button>
      </div>
    </header>

    <section
      v-if="showFilters"
      class="space-y-2 border-b border-mf-muted/25 bg-mf-surface/60 p-3 text-sm"
    >
      <div class="flex gap-2">
        <select
          v-model="categoryId"
          class="flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-2"
        >
          <option value="">Any category</option>
          <option v-for="c in taxonomy.categories" :key="c.id" :value="String(c.id)">
            {{ c.name }}
          </option>
        </select>
        <select
          v-model="accountId"
          class="flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-2"
        >
          <option value="">Any account</option>
          <option v-for="a in taxonomy.activeAccounts" :key="a.id" :value="String(a.id)">
            {{ a.name }}
          </option>
        </select>
      </div>
      <div class="flex items-center gap-2">
        <input
          v-model="from"
          type="date"
          class="flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-2"
        />
        <span class="text-mf-muted">–</span>
        <input
          v-model="to"
          type="date"
          class="flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-2"
        />
      </div>
      <div class="flex items-center gap-2">
        <input
          v-model="minAmount"
          type="number"
          placeholder="Min"
          class="flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-2"
        />
        <span class="text-mf-muted">–</span>
        <input
          v-model="maxAmount"
          type="number"
          placeholder="Max"
          class="flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-2"
        />
        <button type="button" class="shrink-0 text-mf-green-dark" @click="clear">Clear</button>
      </div>
    </section>

    <p class="flex items-center justify-between px-4 py-2 text-xs text-mf-muted">
      <span>{{ results.length }} record(s)</span>
      <MoneyAmount
        :minor="totalMinor"
        :currency="dashboard.baseCurrency"
        :class="totalMinor < 0 ? 'text-mf-red-text' : 'text-mf-green-dark'"
      />
    </p>

    <main class="flex-1 overflow-y-auto pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <!-- The same row as the dashboard: one design for a transaction, everywhere. -->
      <ul class="divide-y divide-mf-muted/20 px-4">
        <li v-for="row in results" :key="row.id">
          <TransactionRow :row="row" @select="openRecord" />
        </li>
      </ul>

      <p v-if="!results.length" class="px-4 py-10 text-center text-sm text-mf-muted">
        Nothing matches.
      </p>
    </main>
  </div>
</template>
