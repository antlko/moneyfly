<script setup lang="ts">
import { computed } from 'vue'

import { colorVar, iconFor } from '@/lib/categories'

const props = withDefaults(
  defineProps<{
    icon: unknown
    color: unknown
    size?: number
    /** Unused categories are drawn faint, not hidden — the reference keeps them visible. */
    dim?: boolean
  }>(),
  { size: 28, dim: false },
)

const component = computed(() => iconFor(props.icon))
const stroke = computed(() => colorVar(props.color))
</script>

<template>
  <component
    :is="component"
    :size="size"
    :stroke-width="1.6"
    :style="{ color: stroke, opacity: dim ? 0.35 : 1 }"
    aria-hidden="true"
  />
</template>
