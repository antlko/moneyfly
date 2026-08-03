<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { formatMoney } from '@/lib/money'
import { formatDate, periodRange } from '@/lib/period'
import { pushToast } from '@/lib/toast'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { useTransactionsStore, type TransactionFilter } from '@/stores/transactions'

const route = useRoute()
const taxonomy = useTaxonomyStore()
const transactions = useTransactionsStore()

const query = ref('')
const categoryID = ref<number | undefined>(undefined)
const accountID = ref<number | undefined>(undefined)
const kind = ref<string>('')
const from = ref('')
const to = ref('')

const filter = computed<TransactionFilter>(() => ({
  query: query.value.trim() || undefined,
  categoryID: categoryID.value,
  accountID: accountID.value,
  kind: kind.value || undefined,
  from: from.value || undefined,
  to: to.value || undefined,
}))

let debounce: number | undefined

async function reload() {
  await transactions.load(filter.value)
}

/** Grouped by day, which is how a statement reads. */
const grouped = computed(() => {
  const groups = new Map<string, typeof transactions.items>()
  for (const item of transactions.items) {
    const existing = groups.get(item.occurred_on) ?? []
    existing.push(item)
    groups.set(item.occurred_on, existing)
  }
  return [...groups.entries()]
})

async function remove(id: number) {
  await transactions.remove(id)
  pushToast('Transaction deleted.', 'info')
}

function onScroll(event: Event) {
  const el = event.target as HTMLElement
  if (el.scrollHeight - el.scrollTop - el.clientHeight < 240) {
    transactions.loadMore(filter.value)
  }
}

watch([query], () => {
  window.clearTimeout(debounce)
  debounce = window.setTimeout(reload, 250)
})
watch([categoryID, accountID, kind, from, to], reload)

onMounted(async () => {
  await taxonomy.load()
  // Arriving from a category row on the budget screen pre-applies its filter.
  if (route.query.category) categoryID.value = Number(route.query.category)
  if (typeof route.query.period === 'string') {
    const range = periodRange(route.query.period)
    from.value = range.from
    to.value = range.to
  }
  await reload()
})
</script>

<template>
  <section>
    <h1 class="mb-4 text-lg font-semibold">History</h1>

    <div class="mb-4 space-y-2">
      <label for="search" class="sr-only">Search descriptions and merchants</label>
      <input
        id="search"
        v-model="query"
        type="search"
        placeholder="Search notes and merchants…"
        class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
      />

      <div class="grid grid-cols-2 gap-2 text-sm">
        <div>
          <label for="f-category" class="mb-1 block text-xs text-slate-500">Category</label>
          <select
            id="f-category"
            v-model="categoryID"
            class="w-full rounded-xl border border-slate-300 bg-white px-2 py-2 dark:border-slate-700 dark:bg-slate-900"
          >
            <option :value="undefined">All</option>
            <option v-for="c in taxonomy.categories" :key="c.id" :value="c.id">{{ c.name }}</option>
          </select>
        </div>
        <div>
          <label for="f-account" class="mb-1 block text-xs text-slate-500">Account</label>
          <select
            id="f-account"
            v-model="accountID"
            class="w-full rounded-xl border border-slate-300 bg-white px-2 py-2 dark:border-slate-700 dark:bg-slate-900"
          >
            <option :value="undefined">All</option>
            <option v-for="a in taxonomy.accounts" :key="a.id" :value="a.id">{{ a.name }}</option>
          </select>
        </div>
        <div>
          <label for="f-kind" class="mb-1 block text-xs text-slate-500">Kind</label>
          <select
            id="f-kind"
            v-model="kind"
            class="w-full rounded-xl border border-slate-300 bg-white px-2 py-2 dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="">All</option>
            <option value="expense">Expense</option>
            <option value="income">Income</option>
            <option value="transfer_out">Transfer out</option>
            <option value="transfer_in">Transfer in</option>
          </select>
        </div>
        <div class="grid grid-cols-2 gap-1">
          <div>
            <label for="f-from" class="mb-1 block text-xs text-slate-500">From</label>
            <input
              id="f-from"
              v-model="from"
              type="date"
              class="w-full rounded-xl border border-slate-300 bg-white px-2 py-2 dark:border-slate-700 dark:bg-slate-900"
            />
          </div>
          <div>
            <label for="f-to" class="mb-1 block text-xs text-slate-500">To</label>
            <input
              id="f-to"
              v-model="to"
              type="date"
              class="w-full rounded-xl border border-slate-300 bg-white px-2 py-2 dark:border-slate-700 dark:bg-slate-900"
            />
          </div>
        </div>
      </div>
    </div>

    <div class="max-h-[60vh] overflow-y-auto" @scroll.passive="onScroll">
      <p
        v-if="!transactions.loading && transactions.items.length === 0"
        class="rounded-xl border border-dashed border-slate-300 p-4 text-sm text-slate-500 dark:border-slate-700"
      >
        Nothing matches. Log a transaction from the ➕ button, or widen the filters.
      </p>

      <div v-for="[day, rows] in grouped" :key="day" class="mb-4">
        <h2 class="mb-1 px-1 text-xs font-semibold uppercase tracking-wide text-slate-400">
          {{ formatDate(day) }}
        </h2>
        <ul class="space-y-1">
          <li
            v-for="row in rows"
            :key="row.id"
            class="flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
          >
            <span class="flex-1 min-w-0">
              <span class="block truncate font-medium">
                {{ row.category_name || (row.kind.startsWith('transfer') ? 'Transfer' : '—') }}
              </span>
              <span class="block truncate text-xs text-slate-400">
                {{ row.account_name }}
                <template v-if="row.description"> · {{ row.description }}</template>
                <template v-if="row.occurrence > 1"> · #{{ row.occurrence }}</template>
              </span>
            </span>
            <span class="text-right">
              <span
                class="money block text-sm font-semibold"
                :class="row.kind === 'income' ? 'text-state-within' : ''"
              >
                {{ row.kind === 'income' ? '+' : '' }}{{ formatMoney(row.amount) }}
              </span>
              <span v-if="row.unconverted" class="block text-xs text-state-approach">
                no rate yet
              </span>
              <span
                v-else-if="row.base_amount && row.base_amount.currency !== row.amount.currency"
                class="money block text-xs text-slate-400"
              >
                = {{ formatMoney(row.base_amount) }}
              </span>
            </span>
            <button
              type="button"
              class="tap-target rounded-lg px-2 text-slate-400 hover:text-state-severe"
              :aria-label="'Delete transaction from ' + formatDate(row.occurred_on)"
              @click="remove(row.id)"
            >
              ×
            </button>
          </li>
        </ul>
      </div>

      <button
        v-if="transactions.hasMore"
        type="button"
        class="tap-target w-full rounded-xl border border-slate-300 py-2 text-sm dark:border-slate-700"
        :disabled="transactions.loading"
        @click="transactions.loadMore(filter)"
      >
        {{ transactions.loading ? 'Loading…' : 'Load more' }}
      </button>
    </div>
  </section>
</template>
