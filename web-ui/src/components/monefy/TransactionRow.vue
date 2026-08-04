<script setup lang="ts">
import { computed } from 'vue'

import { shortDate } from '@/lib/period'
import { useDashboardStore } from '@/stores/dashboard'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'
import MoneyAmount from './MoneyAmount.vue'

/**
 * One transaction, the way the reference draws it inside an expanded category:
 *
 *     ●  ₴320.00                                    4 Aug
 *        €6.40
 *        Note, if there is one
 *
 * Three things it deliberately does not have. **No icon** — the category is
 * already named by the row above, and repeating its icon on every line turns a
 * list into a column of pictures; a dot in the category's colour is enough to
 * tie them together. **No "Edit" link** — the whole row is the control, which is
 * both a bigger target and one fewer thing to read. **No "Delete"** — a delete
 * button sitting next to every amount is one mis-tap from losing a record, so
 * deleting lives on the edit screen behind a confirmation.
 *
 * The amount is shown in the currency it was recorded in, with the base-currency
 * value small underneath and only when the two differ. That ordering is the
 * reference's, and it is the right way round: what you spent is 320 hryvnia, and
 * the euro figure is this app's interpretation of it.
 */
const props = defineProps<{
  row: Row
  /** Hide the category name when the surrounding list already states it. */
  hideCategory?: boolean
}>()
defineEmits<{ select: [Row] }>()

const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()

const category = computed(() => taxonomy.byId.get(String(props.row.categoryId ?? '')))
const currency = computed(() => String(props.row.currency ?? dashboard.baseCurrency))
const minor = computed(() => Number(props.row.amountMinor ?? 0))
const isTransfer = computed(() => props.row.kind === 'transfer')

/** The base-currency value, or null when it is the same currency or unknown. */
const converted = computed(() => {
  if (currency.value === dashboard.baseCurrency) return null
  return dashboard.inBase(props.row)
})

const note = computed(() => String(props.row.note ?? '').trim())
const subtitle = computed(() => {
  if (note.value) return note.value
  if (isTransfer.value) return 'Transfer'
  return props.hideCategory ? '' : (category.value?.name ?? 'Uncategorised')
})
</script>

<template>
  <button
    type="button"
    class="flex w-full items-start gap-3 py-2.5 text-left"
    @click="$emit('select', row)"
  >
    <span
      class="mt-1.5 size-2.5 shrink-0 rounded-full"
      :style="{ backgroundColor: `var(--color-cat-${category?.color ?? 'gray'})` }"
    />

    <div class="min-w-0 flex-1">
      <MoneyAmount
        :minor="Math.abs(minor)"
        :currency="currency"
        class="text-base"
        :class="minor < 0 ? 'text-mf-red-text' : 'text-mf-green-dark'"
      />
      <!--
        Only when the currencies differ. Printing "€6.40" under "€6.40" would be
        noise, and noise on every row is what the reference's list avoids.
      -->
      <MoneyAmount
        v-if="converted !== null"
        :minor="Math.abs(converted)"
        :currency="dashboard.baseCurrency"
        class="block text-sm text-mf-muted"
      />
      <p v-if="subtitle" class="truncate text-sm text-mf-muted">{{ subtitle }}</p>
    </div>

    <span class="shrink-0 pt-1 text-sm text-mf-muted">
      {{ shortDate(String(row.occurredOn)) }}
    </span>
  </button>
</template>
