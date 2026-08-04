<script setup lang="ts">
import { LayoutGrid, Repeat } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import AmountDisplay from '@/components/monefy/AmountDisplay.vue'
import CategoryGrid from '@/components/monefy/CategoryGrid.vue'
import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import DateRow from '@/components/monefy/DateRow.vue'
import AmountKeypad from '@/components/monefy/AmountKeypad.vue'
import NewCategorySheet from '@/components/monefy/NewCategorySheet.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { display, initialState, press, total, type Key } from '@/lib/calculator'
import { DEFAULT_ACCOUNT_ID } from '@/lib/categories'
import { exponent, toMinor } from '@/lib/money'
import { today } from '@/lib/period'
import { useDashboardStore } from '@/stores/dashboard'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'
import type { Row } from '@/sync/types'

const props = defineProps<{ kind: 'expense' | 'income' }>()

const router = useRouter()
const route = useRoute()
const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()

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

const currency = computed(() => dashboard.baseCurrency)
const amount = computed(() => display(calc.value))
const hasAmount = computed(() => total(calc.value) > 0)

const categories = computed(() =>
  props.kind === 'expense' ? taxonomy.expenseCategories : taxonomy.incomeCategories,
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
  const id = route.query.category
  return typeof id === 'string' ? categories.value.find((c) => c.id === id) : undefined
})

const title = computed(() => (props.kind === 'expense' ? 'New expense' : 'New income'))

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

const account = computed(
  () =>
    taxonomy.activeAccounts.find((a) => a.currency === currency.value) ??
    taxonomy.activeAccounts[0],
)

async function record(category: Row) {
  const major = total(calc.value)
  if (major <= 0) return

  const minor = toMinor(major, currency.value)
  const id = await sync.write('txn', {
    kind: props.kind,
    occurredOn: day.value,
    // Expenses are stored negative, the way the Monefy export writes them, so
    // summing a month needs no knowledge of which kind a row is.
    amountMinor: props.kind === 'expense' ? -minor : minor,
    currency: currency.value,
    categoryId: category.id,
    accountId: account.value?.id ?? DEFAULT_ACCOUNT_ID,
    note: note.value.trim(),
  })

  // Deliberately silent. A toast here covers the bottom of the dashboard you
  // were just returned to — including the record buttons — and the record is
  // visible on the chart the moment you land, which is confirmation enough.
  // Undo is the Delete on each row of the records sheet (swipe the balance up).
  void id

  // Jump to the period the record belongs to, not whichever one happened to be
  // open — recording something dated last week and landing on a chart that does
  // not contain it looks exactly like the record was lost.
  dashboard.goToDay(day.value)
  await router.replace('/')
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
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader :title="title" :on-back="back">
      <template #actions>
        <button
          type="button"
          class="grid size-11 place-items-center"
          aria-label="Make recurring"
          @click="toast('Recurring records arrive in a later phase')"
        >
          <Repeat :size="22" :stroke-width="1.8" />
        </button>
      </template>
    </ScreenHeader>

    <DateRow v-model:day="day" />

    <AmountDisplay
      :amount="amount"
      :currency="currency"
      @backspace="key('backspace')"
      @pick-account="toast('Multiple accounts arrive in a later phase')"
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
      <NewCategorySheet
        v-if="showNewCategory"
        :kind="kind"
        @cancel="showNewCategory = false"
        @create="createCategory"
      />
    </Transition>
  </div>
</template>
