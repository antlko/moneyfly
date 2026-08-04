<script setup lang="ts">
import { computed } from 'vue'

import { splitMoney } from '@/lib/money'

const props = withDefaults(
  defineProps<{
    minor: number
    currency: string
    /** Extra classes for the fractional part, which the reference renders smaller. */
    fractionClass?: string
  }>(),
  { fractionClass: 'text-[0.72em]' },
)

const parts = computed(() => splitMoney(props.minor, props.currency))
</script>

<template>
  <!--
    Monefy renders the cents smaller than the whole units — `-€317.` large and
    `03` small. Splitting the value is the only way to do that, hence
    splitMoney() rather than a single formatted string.
  -->
  <span class="whitespace-nowrap tabular-nums"
    >{{ parts.sign }}{{ parts.symbol }}{{ parts.whole
    }}<span v-if="parts.fraction" :class="fractionClass"
      >{{ parts.separator }}{{ parts.fraction }}</span
    ></span
  >
</template>
