<script setup lang="ts">
import { RouterLink } from 'vue-router'

// Bottom tab bar, four destinations plus the raised central action
// (docs/08-ux.md §8.2). The year grid lives under Budget's own header rather
// than taking a fifth slot: five tabs on a phone are two too many.
const tabs = [
  { to: '/budget', label: 'Budget', icon: '◎' },
  { to: '/capital', label: 'Capital', icon: '◈' },
  { to: '/history', label: 'History', icon: '≡' },
  { to: '/more', label: 'More', icon: '⋯' },
]
</script>

<template>
  <nav
    class="fixed inset-x-0 bottom-0 z-40 border-t border-slate-200 bg-white/95 backdrop-blur dark:border-slate-800 dark:bg-slate-900/95"
    aria-label="Main"
  >
    <ul
      class="mx-auto flex max-w-3xl items-end justify-around px-2 pb-[env(safe-area-inset-bottom)]"
    >
      <li v-for="tab in tabs.slice(0, 2)" :key="tab.to" class="flex-1">
        <RouterLink :to="tab.to" class="tab-link tap-target" active-class="tab-link-active">
          <span aria-hidden="true" class="text-lg">{{ tab.icon }}</span>
          <span>{{ tab.label }}</span>
        </RouterLink>
      </li>
      <li class="flex-none px-2">
        <RouterLink
          to="/entry"
          class="-mt-6 flex h-14 w-14 items-center justify-center rounded-full bg-brand-600 text-2xl text-white shadow-lg shadow-brand-900/30 hover:bg-brand-700"
          aria-label="Add a transaction"
        >
          <span aria-hidden="true">+</span>
        </RouterLink>
      </li>
      <li v-for="tab in tabs.slice(2)" :key="tab.to" class="flex-1">
        <RouterLink :to="tab.to" class="tab-link tap-target" active-class="tab-link-active">
          <span aria-hidden="true" class="text-lg">{{ tab.icon }}</span>
          <span>{{ tab.label }}</span>
        </RouterLink>
      </li>
    </ul>
  </nav>
</template>

<style scoped>
@reference "@/style.css";

.tab-link {
  @apply flex flex-col items-center gap-0.5 py-2 text-xs text-slate-500 dark:text-slate-400;
}

.tab-link-active {
  @apply font-semibold text-brand-600 dark:text-brand-300;
}
</style>
