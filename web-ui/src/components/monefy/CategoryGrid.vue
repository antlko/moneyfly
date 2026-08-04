<script setup lang="ts">
import { Plus } from '@lucide/vue'

import { colorVar } from '@/lib/categories'
import type { Row } from '@/sync/types'
import CategoryIcon from './CategoryIcon.vue'

defineProps<{ categories: Row[] }>()
defineEmits<{ select: [Row]; create: [] }>()
</script>

<template>
  <!--
    Four columns, and tapping a tile *saves* — it is the last tap of the three,
    not a selection to confirm afterwards. The trailing green "+" adds a
    category, exactly where the reference puts it.
  -->
  <div class="grid grid-cols-4 gap-2 px-3">
    <button
      v-for="category in categories"
      :key="category.id"
      type="button"
      class="flex aspect-[4/3] flex-col items-center justify-center gap-1 rounded-lg border border-mf-green-soft bg-mf-surface/60 px-1 active:bg-mf-green-soft/40"
      @click="$emit('select', category)"
    >
      <CategoryIcon :icon="category.icon" :color="category.color" :size="26" />
      <span
        class="w-full truncate text-center text-[11px] leading-tight"
        :style="{ color: colorVar(category.color) }"
        >{{ category.name }}</span
      >
    </button>

    <button
      type="button"
      class="flex aspect-[4/3] items-center justify-center"
      aria-label="New category"
      @click="$emit('create')"
    >
      <span
        class="grid h-12 w-12 place-items-center rounded-full bg-mf-green-soft/70 text-mf-green-dark"
      >
        <Plus :size="26" :stroke-width="2" />
      </span>
    </button>
  </div>
</template>
