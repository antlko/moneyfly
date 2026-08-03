<script setup lang="ts">
import { useOnline } from '@/lib/offline'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import { formatMoney, parseAmount } from '@/lib/money'
import { today } from '@/lib/period'
import { pushToast } from '@/lib/toast'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { useTransactionsStore } from '@/stores/transactions'
import type { Account, Category } from '@/api/types'

/**
 * Quick entry: category, digits, save. Three taps (docs/08-ux.md §8.3).
 *
 * The keypad is a custom grid rather than the OS keyboard — no zoom, no layout
 * shift, bigger targets. Note, date and account are one tap away, never in the
 * required path.
 */
const taxonomy = useTaxonomyStore()
const transactions = useTransactionsStore()
const router = useRouter()

const RECENT_KEY = 'moneyapp.recentCategories'
const LAST_ACCOUNT_KEY = 'moneyapp.lastAccountByCurrency'

const step = ref<'category' | 'amount'>('category')
const kind = ref<'expense' | 'income'>('expense')
const category = ref<Category | null>(null)
const account = ref<Account | null>(null)
const digits = ref('')
const occurredOn = ref(today())
const description = ref('')
const showDetails = ref(false)
const busy = ref(false)
// Offline, a save is disabled rather than attempted: a button that appears to
// work and silently does not is worse than one visibly unavailable.
const { online } = useOnline()
const error = ref('')

const categories = computed(() =>
  kind.value === 'expense' ? taxonomy.expenseCategories : taxonomy.incomeCategories,
)

/** Ordered by recent frequency, so the common six need no scrolling. */
const orderedCategories = computed(() => {
  const recent = readRecent()
  return [...categories.value].sort((a, b) => {
    const diff = (recent[b.id] ?? 0) - (recent[a.id] ?? 0)
    return diff !== 0 ? diff : a.sort_order - b.sort_order
  })
})

const exponent = computed(() => (account.value ? taxonomy.exponentOf(account.value.currency) : 2))
const amountMinor = computed(() => parseAmount(digits.value, exponent.value))
const preview = computed(() =>
  account.value && amountMinor.value !== null
    ? formatMoney({
        amount_minor: amountMinor.value,
        currency: account.value.currency,
        exponent: exponent.value,
      })
    : '—',
)
const canSave = computed(
  () => !!category.value && !!account.value && amountMinor.value !== null && amountMinor.value > 0,
)

function readRecent(): Record<number, number> {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) ?? '{}') as Record<number, number>
  } catch {
    return {}
  }
}

function noteRecent(id: number) {
  const recent = readRecent()
  recent[id] = (recent[id] ?? 0) + 1
  localStorage.setItem(RECENT_KEY, JSON.stringify(recent))
}

function readLastAccounts(): Record<string, number> {
  try {
    return JSON.parse(localStorage.getItem(LAST_ACCOUNT_KEY) ?? '{}') as Record<string, number>
  } catch {
    return {}
  }
}

/**
 * The account defaults to the last one used, and on a fresh install to spendable
 * money.
 *
 * Not simply the first EUR account: the seeded chart of accounts starts with Gold,
 * and defaulting a grocery shop to the gold holding is worse than useless. Liquid
 * cash first, then a liquid bank account, then anything liquid.
 */
function defaultAccount(): Account | null {
  const last = readLastAccounts()
  const postable = taxonomy.postableAccounts
  for (const currency of Object.keys(last)) {
    const found = postable.find((a) => a.id === last[currency])
    if (found) return found
  }
  const base = 'EUR'
  const preference = [
    (a: Account) => a.is_liquid && a.asset_class === 'cash' && a.currency === base,
    (a: Account) => a.is_liquid && a.asset_class === 'bank' && a.currency === base,
    (a: Account) => a.is_liquid && a.currency === base,
    (a: Account) => a.is_liquid,
  ]
  for (const matches of preference) {
    const found = postable.find(matches)
    if (found) return found
  }
  return postable[0] ?? null
}

function pick(selected: Category) {
  category.value = selected
  step.value = 'amount'
}

function selectKind(option: 'expense' | 'income') {
  kind.value = option
  category.value = null
  step.value = 'category'
}

function selectAccount(id: number) {
  account.value = taxonomy.postableAccounts.find((a) => a.id === id) ?? account.value
}

function press(key: string) {
  if (key === 'back') {
    digits.value = digits.value.slice(0, -1)
    return
  }
  if (key === '.') {
    if (exponent.value === 0 || digits.value.includes('.')) return
    digits.value = digits.value === '' ? '0.' : digits.value + '.'
    return
  }
  const [, fraction = ''] = digits.value.split('.')
  if (digits.value.includes('.') && fraction.length >= exponent.value) return
  if (digits.value === '0' && key !== '.') {
    digits.value = key
    return
  }
  digits.value += key
}

async function save() {
  if (!canSave.value || !category.value || !account.value || amountMinor.value === null) return
  error.value = ''
  busy.value = true

  // Optimistic: the draft is kept until the save is known to have landed, so a
  // failure restores it rather than discarding it.
  const draft = {
    account_id: account.value.id,
    category_id: category.value.id,
    occurred_on: occurredOn.value,
    kind: kind.value,
    amount: {
      amount_minor: amountMinor.value,
      currency: account.value.currency,
      exponent: exponent.value,
    },
    description: description.value.trim() || null,
  }

  try {
    const created = await transactions.create(draft)
    noteRecent(category.value.id)
    const lastAccounts = readLastAccounts()
    lastAccounts[account.value.currency] = account.value.id
    localStorage.setItem(LAST_ACCOUNT_KEY, JSON.stringify(lastAccounts))

    const message = created.unconverted
      ? `Saved ${formatMoney(created.amount)} — no exchange rate for that date yet`
      : `Saved ${formatMoney(created.amount)} to ${created.category_name}`
    pushToast(message, created.unconverted ? 'info' : 'success', {
      label: 'Undo',
      run: async () => {
        await transactions.remove(created.id)
        pushToast('Removed.', 'info')
      },
    })

    digits.value = ''
    description.value = ''
    step.value = 'category'
    await router.push('/budget')
  } catch (err) {
    // The draft is intact: the user can correct and retry.
    error.value =
      err instanceof ApiError
        ? Object.values(err.fieldErrors)[0] || err.message
        : 'Could not reach the server. Your entry is still here.'
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  await taxonomy.load()
  account.value = defaultAccount()
})
</script>

<template>
  <section>
    <header class="mb-4 flex items-center gap-2">
      <h1 class="flex-1 text-lg font-semibold">
        {{ step === 'category' ? 'What did you spend on?' : 'How much?' }}
      </h1>
      <div
        class="flex rounded-xl bg-slate-100 p-0.5 text-sm dark:bg-slate-800"
        role="group"
        aria-label="Transaction kind"
      >
        <button
          v-for="option in ['expense', 'income'] as const"
          :key="option"
          type="button"
          class="tap-target rounded-lg px-3 capitalize"
          :class="
            kind === option ? 'bg-white font-semibold shadow dark:bg-slate-700' : 'text-slate-500'
          "
          :aria-pressed="kind === option"
          @click="selectKind(option)"
        >
          {{ option }}
        </button>
      </div>
    </header>

    <!-- Step 1: the category grid, most-used first. -->
    <div v-if="step === 'category'">
      <p
        v-if="categories.length === 0"
        class="rounded-xl border border-dashed border-slate-300 p-4 text-sm text-slate-500 dark:border-slate-700"
      >
        No {{ kind }} categories yet. Monefy exports no income at all, so income categories start
        empty —
        <RouterLink
          to="/settings/categories"
          class="font-semibold text-brand-600 underline dark:text-brand-300"
          >create one</RouterLink
        >.
      </p>
      <ul class="grid grid-cols-3 gap-2 sm:grid-cols-4">
        <li v-for="c in orderedCategories" :key="c.id">
          <button
            type="button"
            class="tap-target flex w-full flex-col items-center gap-1 rounded-xl border border-slate-200 bg-white p-3 text-center hover:border-brand-400 dark:border-slate-800 dark:bg-slate-900"
            @click="pick(c)"
          >
            <span
              class="flex h-10 w-10 items-center justify-center rounded-full text-xl"
              :style="{ backgroundColor: (c.color ?? '#64748b') + '22' }"
              aria-hidden="true"
              >{{ c.icon ?? '•' }}</span
            >
            <span class="w-full truncate text-xs">{{ c.name }}</span>
          </button>
        </li>
      </ul>
    </div>

    <!-- Step 2: the custom keypad. -->
    <div v-else class="space-y-4">
      <button
        type="button"
        class="tap-target flex w-full items-center gap-3 rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
        @click="step = 'category'"
      >
        <span
          class="flex h-9 w-9 items-center justify-center rounded-full text-lg"
          :style="{ backgroundColor: (category?.color ?? '#64748b') + '22' }"
          aria-hidden="true"
          >{{ category?.icon ?? '•' }}</span
        >
        <span class="flex-1 text-left font-medium">{{ category?.name }}</span>
        <span class="text-xs text-slate-400">change</span>
      </button>

      <div class="rounded-2xl bg-slate-100 p-5 text-center dark:bg-slate-800">
        <p class="money text-center text-4xl font-bold" aria-live="polite">
          {{
            digits === ''
              ? formatMoney({ amount_minor: 0, currency: account?.currency ?? 'EUR', exponent })
              : preview
          }}
        </p>
        <p class="mt-1 text-xs text-slate-500">
          {{ account?.name }} · {{ account?.currency }}
          <span v-if="exponent === 0"> (no decimals)</span>
        </p>
      </div>

      <div class="grid grid-cols-3 gap-2">
        <button
          v-for="key in ['1', '2', '3', '4', '5', '6', '7', '8', '9', '.', '0', 'back']"
          :key="key"
          type="button"
          class="tap-target rounded-xl bg-white py-4 text-xl font-semibold shadow-sm hover:bg-brand-50 disabled:opacity-30 dark:bg-slate-900 dark:hover:bg-slate-800"
          :disabled="key === '.' && exponent === 0"
          :aria-label="key === 'back' ? 'Delete last digit' : key"
          @click="press(key)"
        >
          {{ key === 'back' ? '⌫' : key }}
        </button>
      </div>

      <button
        type="button"
        class="tap-target w-full text-left text-sm text-brand-600 underline dark:text-brand-300"
        :aria-expanded="showDetails"
        @click="showDetails = !showDetails"
      >
        {{ showDetails ? 'Hide' : 'Add' }} note, date or account
      </button>

      <div v-if="showDetails" class="space-y-3">
        <div>
          <label for="account" class="mb-1 block text-sm font-medium">Account</label>
          <select
            id="account"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
            :value="account?.id"
            @change="selectAccount(Number(($event.target as HTMLSelectElement).value))"
          >
            <option v-for="a in taxonomy.postableAccounts" :key="a.id" :value="a.id">
              {{ a.name }} ({{ a.currency }})
            </option>
          </select>
        </div>
        <div>
          <label for="date" class="mb-1 block text-sm font-medium">Date</label>
          <input
            id="date"
            v-model="occurredOn"
            type="date"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
          />
        </div>
        <div>
          <label for="note" class="mb-1 block text-sm font-medium">Note</label>
          <input
            id="note"
            v-model="description"
            type="text"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900"
          />
        </div>
      </div>

      <p
        v-if="error"
        class="rounded-xl bg-state-severe/10 px-3 py-2 text-sm text-state-severe"
        role="alert"
      >
        {{ error }}
      </p>

      <button
        type="button"
        class="tap-target w-full rounded-xl bg-brand-600 px-4 py-3.5 text-lg font-semibold text-white hover:bg-brand-700 disabled:opacity-40"
        :disabled="!canSave || busy || !online"
        @click="save"
      >
        {{ busy ? 'Saving…' : 'Save' }}
      </button>
    </div>
  </section>
</template>
