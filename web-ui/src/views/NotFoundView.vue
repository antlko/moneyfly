<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <main class="flex h-full flex-col items-center justify-center gap-4 p-8 text-center">
    <p class="text-6xl font-light text-mf-muted">404</p>
    <RouterLink to="/" class="text-mf-green-dark underline underline-offset-4">
      Back to moneyfly
    </RouterLink>
  </main>
</template>
