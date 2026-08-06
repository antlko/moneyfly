<script setup lang="ts">
import { LayoutGrid, Repeat, Trash2 } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import AccountSheet from '@/components/monefy/AccountSheet.vue'
import AmountDisplay from '@/components/monefy/AmountDisplay.vue'
import CategoryGrid from '@/components/monefy/CategoryGrid.vue'
import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import DateRow from '@/components/monefy/DateRow.vue'
import AmountKeypad from '@/components/monefy/AmountKeypad.vue'
import NewCategorySheet from '@/components/monefy/NewCategorySheet.vue'
import RecurringSheet from '@/components/monefy/RecurringSheet.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { display, initialState, press, total, typed, type Key } from '@/lib/calculator'
import { DEFAULT_ACCOUNT_ID } from '@/lib/categories'
import { exponent, toMajor, toMinor } from '@/lib/money'
import { nextOccurrence, today, type RecurringFreq } from '@/lib/period'
import { db } from '@/db'
import { useDashboardStore } from '@/stores/dashboard'
import { useRecurringStore } from '@/stores/recurring'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'

/**
 * New record, or an existing one.
 *
 * The same screen for both: the fields are identical, and editing is the one
 * place people go to correct the thing they got wrong three taps ago — sending
 * them somewhere that looks different for it would be strange. `id` present
 * means edit; `kind` then comes from the stored row rather than the route.
 */
const props = defineProps<{ kind?: 'expense' | 'income'; id?: string }>()

const router = useRouter()
const route = useRoute()
const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()
const settings = useSettingsStore()
const recurring = useRecurringStore()

/*
 * One screen, two steps — the amount, then the category. Not two routes: going
 * "back" from the category grid has to return to the digits you typed, and the
 * browser's back button would otherwise leave the screen entirely.
 */
const step = ref<'amount' | 'category'>('amount')
const calc = ref(initialState())
const day = ref(today())
const note = ref('')
const showNewCategory = ref(false)
const showAccounts = ref(false)
const showRecurringPicker = ref(false)
/**
 * The frequency armed by the Repeat control, if any.
 *
 * A choice made here, not a category: tapping Repeat before a category is
 * picked sends the same person through the same grid `confirm()` already
 * would, and whichever category they land on is the one the rule gets —
 * there is no separate "recurring category" to keep in sync with it.
 */
const pendingFreq = ref<RecurringFreq | null>(null)

/** The row being edited, once it has been read out of the replica. */
const existing = ref<Row | null>(null)
const editing = computed(() => props.id !== undefined)
/** Set from the stored row when editing; the route param otherwise. */
const kind = computed<'expense' | 'income'>(
  () => (existing.value?.kind as 'expense' | 'income') ?? props.kind ?? 'expense',
)
/** While editing, the category the row already has — changed by the same grid. */
const categoryId = ref<string | null>(null)

/**
 * Load the record being edited.
 *
 * Straight from IndexedDB, like everything else on this screen: opening an
 * expense to fix its amount must work in a tunnel, and a spinner here would
 * mean the app had suddenly acquired a network dependency for reading its own
 * data.
 */
onMounted(async () => {
  if (!props.id) return
  const row = await db.txn.get(props.id)
  if (!row || row.deleted) {
    await router.replace('/')
    return
  }
  existing.value = row
  accountId.value = String(row.accountId ?? '')
  categoryId.value = String(row.categoryId ?? '')
  day.value = String(row.occurredOn ?? today())
  note.value = String(row.note ?? '')
  const major = Math.abs(Number(row.amountMinor ?? 0))
  calc.value = typed(String(toMajor(major, String(row.currency ?? dashboard.baseCurrency))))
})

/**
 * The account paying for this, and therefore the currency of the amount.
 *
 * A record used to be written in the base currency with an account then picked
 * to match, which put a euro expense on a forint wallet the moment the two
 * disagreed. Following the account is both what the reference does and the only
 * version that survives having two currencies.
 *
 * `null` until something is chosen, so the fallback is resolved live rather than
 * pinned before the replica has loaded.
 *
 * That fallback is the **account the last record was written against**, not the
 * first in the list. Spending is habitual, and the first account is only the
 * right guess for whoever happens to have their everyday wallet at the top.
 * It falls through to the first account when nothing has been recorded yet, or
 * when the remembered one has since been archived or deleted.
 */
const accountId = ref<string | null>(null)
const account = computed(() => {
  const chosen = accountId.value ?? settings.get<string>(SETTING.lastAccount, '')
  return (
    taxonomy.activeAccounts.find((a) => String(a.id) === chosen) ?? taxonomy.activeAccounts[0]
  )
})

const currency = computed(() => String(account.value?.currency ?? dashboard.baseCurrency))
const amount = computed(() => display(calc.value))
const hasAmount = computed(() => total(calc.value) > 0)

const categories = computed(() =>
  kind.value === 'expense' ? taxonomy.expenseCategories : taxonomy.incomeCategories,
)

/**
 * A category tapped on the dashboard arrives as `?category=`, and the screen
 * skips straight to the keypad: the bottom button then saves instead of opening
 * the grid. That is the two-tap path for a purchase you make every week.
 *
 * The grid stays one tap away (the small grid button), because arriving here by
 * tapping the wrong icon must not be a dead end.
 */
const chosen = computed(() => {
  const id = categoryId.value ?? route.query.category
  return typeof id === 'string' ? categories.value.find((c) => c.id === id) : undefined
})

const title = computed(() => {
  if (editing.value) return kind.value === 'expense' ? 'Edit expense' : 'Edit income'
  return kind.value === 'expense' ? 'New expense' : 'New income'
})

function key(pressed: Key) {
  calc.value = press(calc.value, pressed, exponent(currency.value))
}

function toCategories() {
  if (!hasAmount.value) return
  step.value = 'category'
}

function confirm() {
  if (!hasAmount.value) return
  if (chosen.value) void record(chosen.value)
  else toCategories()
}

/**
 * Toggling the Repeat control while it is already armed cancels it — the
 * button is the only indication a rule is about to be created, so it also
 * has to be the way out.
 */
function askRecurring() {
  if (pendingFreq.value) {
    pendingFreq.value = null
    return
  }
  if (!hasAmount.value) return
  showRecurringPicker.value = true
}

function pickFreq(freq: RecurringFreq) {
  showRecurringPicker.value = false
  pendingFreq.value = freq
  confirm()
}

function pickAccount(next: Row) {
  showAccounts.value = false
  accountId.value = String(next.id)
  // The new currency may allow fewer decimals than the old one (EUR to HUF), so
  // a part-typed amount has to be re-normalised rather than left with a
  // fraction the currency cannot express.
  calc.value = press(calc.value, 'clear', exponent(currency.value))
}

async function record(category: Row) {
  const major = total(calc.value)
  if (major <= 0) return

  const minor = toMinor(major, currency.value)
  const payingAccount = String(account.value?.id ?? DEFAULT_ACCOUNT_ID)
  // Expenses are stored negative, the way the Monefy export writes them, so
  // summing a month needs no knowledge of which kind a row is.
  const amountMinor = kind.value === 'expense' ? -minor : minor
  // An edit reuses the row id, so it travels as an ordinary last-write-wins op
  // and merges with whatever another device did to the same record.
  await sync.write(
    'txn',
    {
      kind: kind.value,
      occurredOn: day.value,
      amountMinor,
      currency: currency.value,
      categoryId: category.id,
      accountId: payingAccount,
      note: note.value.trim(),
    },
    props.id,
  )

  // The Repeat control saves this occurrence exactly like any other record,
  // above, and additionally schedules the next one — never the other way
  // round, so "make it recurring" never costs the thing you are looking at
  // right now.
  if (pendingFreq.value) {
    const freq = pendingFreq.value
    pendingFreq.value = null
    await recurring.create({
      kind: kind.value,
      freq,
      nextOn: nextOccurrence(day.value, freq),
      amountMinor,
      currency: currency.value,
      categoryId: category.id,
      accountId: payingAccount,
      note: note.value.trim(),
    })
  }

  // Remember it for the next record. Written after the record itself, so a
  // failure here cannot cost the thing the screen was actually for.
  if (payingAccount !== settings.get<string>(SETTING.lastAccount, '')) {
    await settings.set(SETTING.lastAccount, payingAccount)
  }

  // Deliberately silent when creating: a toast covers the bottom of the
  // dashboard you were just returned to — including the record buttons — and
  // the record is visible on the chart the moment you land.
  if (editing.value) toast('Record updated')

  // Jump to the period the record belongs to, not whichever one happened to be
  // open — recording something dated last week and landing on a chart that does
  // not contain it looks exactly like the record was lost.
  dashboard.goToDay(day.value)
  await router.replace('/')
}

/**
 * Delete, with an undo rather than a confirmation dialogue.
 *
 * A tombstone keeps the row, so restoring it is a write like any other — which
 * makes undo both possible and honest. A modal asking "are you sure" before
 * every delete trains people to dismiss it.
 */
async function remove() {
  const row = existing.value
  if (!props.id || !row) return
  const body = { ...row } as Record<string, unknown>
  for (const k of ['id', 'lamport', 'deviceId', 'updatedAt', 'deleted']) delete body[k]

  await sync.remove('txn', props.id)
  await router.replace('/')
  toast('Record deleted', {
    action: { label: 'Undo', onClick: () => void sync.write('txn', body, props.id) },
  })
}

async function createCategory(input: {
  name: string
  icon: string
  color: string
  kind: 'expense' | 'income'
}) {
  showNewCategory.value = false
  await sync.write('category', { ...input, sortOrder: categories.value.length, archived: 0 })
}

function back() {
  if (step.value === 'category') step.value = 'amount'
  else void router.replace('/')
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg sm:mx-auto sm:w-full sm:max-w-md">
    <ScreenHeader :title="title" :on-back="back">
      <template #actions>
        <button
          v-if="editing"
          type="button"
          class="grid size-11 place-items-center"
          aria-label="Delete record"
          @click="remove"
        >
          <Trash2 :size="22" :stroke-width="1.8" />
        </button>
        <button
          v-else
          type="button"
          class="grid size-11 place-items-center rounded-full"
          :class="pendingFreq && 'bg-white/25'"
          :aria-label="pendingFreq ? 'Cancel repeat' : 'Make recurring'"
          @click="askRecurring"
        >
          <Repeat :size="22" :stroke-width="1.8" />
        </button>
      </template>
    </ScreenHeader>

    <DateRow v-model:day="day" />

    <AmountDisplay
      :amount="amount"
      :currency="currency"
      :account-name="String(account?.name ?? '')"
      @backspace="key('backspace')"
      @pick-account="showAccounts = true"
    />

    <template v-if="step === 'amount'">
      <label class="mx-3 mt-4 mb-3 flex items-center gap-2 border-b border-mf-muted/60 pb-2">
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
        <div class="flex gap-2 px-3">
          <button
            type="button"
            :disabled="!hasAmount"
            class="flex flex-1 items-center justify-center gap-2 rounded-lg border border-mf-green-soft bg-mf-surface/60 py-3.5 text-base tracking-wide text-mf-green-dark uppercase disabled:opacity-40"
            @click="confirm"
          >
            <CategoryIcon v-if="chosen" :icon="chosen.icon" :color="chosen.color" :size="22" />
            {{ chosen ? chosen.name : 'Choose category' }}
          </button>
          <button
            v-if="chosen"
            type="button"
            :disabled="!hasAmount"
            aria-label="Choose a different category"
            class="rounded-lg border border-mf-green-soft bg-mf-surface/60 px-4 text-mf-green-dark disabled:opacity-40"
            @click="toCategories"
          >
            <LayoutGrid :size="22" :stroke-width="1.8" />
          </button>
        </div>
      </div>
    </template>

    <div v-else class="min-h-0 flex-1 overflow-y-auto pt-4 pb-[calc(1rem+var(--spacing-safe-b))]">
      <CategoryGrid :categories="categories" @select="record" @create="showNewCategory = true" />
    </div>

    <Transition name="mf-sheet">
      <AccountSheet
        v-if="showAccounts"
        :options="taxonomy.activeAccounts"
        :selected-id="String(account?.id ?? '')"
        @select="pickAccount"
        @close="showAccounts = false"
      />
    </Transition>

    <Transition name="mf-sheet">
      <NewCategorySheet
        v-if="showNewCategory"
        :kind="kind"
        @cancel="showNewCategory = false"
        @create="createCategory"
      />
    </Transition>

    <Transition name="mf-sheet">
      <RecurringSheet
        v-if="showRecurringPicker"
        @select="pickFreq"
        @close="showRecurringPicker = false"
      />
    </Transition>
  </div>
</template>
