<script setup lang="ts">
import { ref } from 'vue'

import { CATEGORY_COLORS, CATEGORY_ICONS, colorVar, type CategoryColor } from '@/lib/categories'
import CategoryIcon from './CategoryIcon.vue'

const props = defineProps<{ kind: 'expense' | 'income' }>()
const emit = defineEmits<{
  cancel: []
  create: [{ name: string; icon: string; color: CategoryColor; kind: 'expense' | 'income' }]
}>()

const name = ref('')
const icon = ref(Object.keys(CATEGORY_ICONS)[0])
const color = ref<CategoryColor>(CATEGORY_COLORS[0])

const iconNames = Object.keys(CATEGORY_ICONS)

function submit() {
  const trimmed = name.value.trim()
  if (!trimmed) return
  emit('create', { name: trimmed, icon: icon.value, color: color.value, kind: props.kind })
}
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-end bg-black/30" @click.self="$emit('cancel')">
    <form
      class="max-h-[85%] w-full space-y-4 overflow-y-auto rounded-t-2xl bg-mf-bg p-4 pb-[calc(1rem+var(--spacing-safe-b))]"
      @submit.prevent="submit"
    >
      <h2 class="text-lg font-medium">New {{ kind }} category</h2>

      <input
        v-model="name"
        type="text"
        placeholder="Name"
        required
        maxlength="40"
        class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
      />

      <div>
        <p class="mb-2 text-sm text-mf-muted">Colour</p>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="option in CATEGORY_COLORS"
            :key="option"
            type="button"
            class="h-8 w-8 rounded-full border-2"
            :style="{
              backgroundColor: colorVar(option),
              borderColor: color === option ? 'var(--color-mf-ink)' : 'transparent',
            }"
            :aria-label="option"
            @click="color = option"
          />
        </div>
      </div>

      <div>
        <p class="mb-2 text-sm text-mf-muted">Icon</p>
        <div class="grid grid-cols-8 gap-2">
          <button
            v-for="option in iconNames"
            :key="option"
            type="button"
            class="grid aspect-square place-items-center rounded-lg border"
            :class="icon === option ? 'border-mf-green bg-mf-green-soft/30' : 'border-transparent'"
            :aria-label="option"
            @click="icon = option"
          >
            <CategoryIcon :icon="option" :color="color" :size="22" />
          </button>
        </div>
      </div>

      <div class="flex gap-3 pt-1">
        <button
          type="button"
          class="flex-1 rounded-full border border-mf-muted py-2.5 text-mf-ink"
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
