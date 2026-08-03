<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <section class="mt-16 text-center">
    <h1 class="text-2xl font-bold">Not found</h1>
    <p class="mt-2 text-sm text-slate-500">That screen does not exist.</p>
    <RouterLink to="/budget" class="mt-4 inline-block font-semibold text-brand-600 underline">
      Back to the budget
    </RouterLink>
  </section>
</template>
