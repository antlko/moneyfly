<script setup lang="ts">
defineEmits<{ expense: []; income: [] }>()
</script>

<template>
  <!--
    The two controls the whole app is built around. They sit above
    env(safe-area-inset-bottom) so the iPhone home indicator never covers them —
    a record button you have to reach past a system gesture is a broken record
    button.

    They are also the *largest* controls on the screen, and deliberately so:
    they are aimed at with a thumb, often without looking, and the reference
    makes them about a quarter of the screen wide for that reason.

    `--spacing-fab-b` is the clearance below — the home indicator's, not the full
    bottom inset. See the token's note in tailwind.css.
  -->
  <footer
    class="flex shrink-0 items-center justify-around px-10 pt-3 pb-fab-b"
  >
    <button
      type="button"
      aria-label="New expense"
      class="grid h-[6.5rem] w-[6.5rem] place-items-center rounded-full border-[3px] border-mf-red bg-mf-surface text-5xl leading-none text-mf-red shadow-sm transition-transform duration-100 active:scale-90"
      @click="$emit('expense')"
    >
      <span class="-mt-1">−</span>
    </button>
    <button
      type="button"
      aria-label="New income"
      class="grid h-[6.5rem] w-[6.5rem] place-items-center rounded-full border-[3px] border-mf-green bg-mf-surface text-5xl leading-none text-mf-green shadow-sm transition-transform duration-100 active:scale-90"
      @click="$emit('income')"
    >
      <span class="-mt-1">+</span>
    </button>
  </footer>
</template>
