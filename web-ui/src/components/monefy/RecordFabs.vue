<script setup lang="ts">
defineEmits<{ expense: []; income: [] }>()
</script>

<template>
  <!--
    The two controls the whole app is built around. They sit above
    env(safe-area-inset-bottom) so the iPhone home indicator never covers them —
    a record button you have to reach past a system gesture is a broken record
    button.
  -->
  <footer
    class="flex shrink-0 items-center justify-around px-10 pt-2 pb-[calc(1rem+var(--spacing-safe-b))]"
  >
    <button
      type="button"
      aria-label="New expense"
      class="grid h-[4.5rem] w-[4.5rem] place-items-center rounded-full border-[3px] border-mf-red bg-mf-surface text-4xl leading-none text-mf-red shadow-sm active:scale-95"
      @click="$emit('expense')"
    >
      <span class="-mt-1">−</span>
    </button>
    <button
      type="button"
      aria-label="New income"
      class="grid h-[4.5rem] w-[4.5rem] place-items-center rounded-full border-[3px] border-mf-green bg-mf-surface text-4xl leading-none text-mf-green shadow-sm active:scale-95"
      @click="$emit('income')"
    >
      <span class="-mt-1">+</span>
    </button>
  </footer>
</template>
