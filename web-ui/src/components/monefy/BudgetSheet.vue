<script setup lang="ts">
import { ref } from 'vue'

import { useTaxonomyStore } from '@/stores/taxonomy'
import CategoryIcon from './CategoryIcon.vue'

/**
 * A budget's currency is fixed to the base currency (not offered here) and it
 * always recurs every month (there is no period picker) — the common case,
 * and the two simplifications that keep this sheet to one screen. Both are
 * schema-compatible with the less common cases arriving later: see
 * stores/budgets.ts.
 */
defineProps<{ currency: string }>()
const emit = defineEmits<{
  cancel: []
  create: [{ categoryId?: string; amount: string }]
}>()

const taxonomy = useTaxonomyStore()
/** null means the overall cap, across every category. */
const categoryId = ref<string | null>(null)
const amount = ref('')

function submit() {
  const value = Number(amount.value)
  if (!value || value <= 0) return
  emit('create', { categoryId: categoryId.value ?? undefined, amount: amount.value })
}
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-end mf-scrim" @click.self="$emit('cancel')">
    <form
      class="max-h-[85%] w-full space-y-4 overflow-y-auto rounded-t-2xl bg-mf-bg p-4 pb-[calc(1rem+var(--spacing-safe-b))]"
      @submit.prevent="submit"
    >
      <h2 class="text-lg font-medium">New budget</h2>

      <div>
        <p class="mb-2 text-sm text-mf-muted">Category</p>
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            class="rounded-full border px-3 py-1.5 text-sm"
            :class="categoryId === null ? 'border-mf-green bg-mf-green-soft/30' : 'border-mf-muted/60'"
            @click="categoryId = null"
          >
            Overall
          </button>
          <button
            v-for="category in taxonomy.expenseCategories"
            :key="category.id"
            type="button"
            class="flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-sm"
            :class="
              categoryId === category.id ? 'border-mf-green bg-mf-green-soft/30' : 'border-mf-muted/60'
            "
            @click="categoryId = String(category.id)"
          >
            <CategoryIcon :icon="category.icon" :color="category.color" :size="16" />
            {{ category.name }}
          </button>
        </div>
      </div>

      <label class="block text-sm">
        <span class="mb-1 block text-mf-muted">Monthly limit ({{ currency }})</span>
        <input
          v-model="amount"
          type="number"
          min="0"
          step="0.01"
          required
          placeholder="0.00"
          class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
        />
      </label>

      <div class="flex gap-3 pt-1">
        <button
          type="button"
          class="flex-1 rounded-full border border-mf-muted py-2.5"
          @click="$emit('cancel')"
        >
          Cancel
        </button>
        <button type="submit" class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white">
          Create
        </button>
      </div>
    </form>
  </div>
</template>
