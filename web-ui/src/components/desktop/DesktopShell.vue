<script setup lang="ts">
import {
  Banknote,
  CircleDollarSign,
  FileUp,
  LayoutDashboard,
  Minus,
  PiggyBank,
  Plug,
  Plus,
  Repeat,
  Settings,
  SlidersHorizontal,
  Users,
} from '@lucide/vue'
import type { Component } from 'vue'
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { useSyncStore } from '@/stores/sync'

/**
 * The desktop chrome: a persistent sidebar around whichever route is active.
 *
 * Mounted once, in App.vue, around the whole `<RouterView>` — not per screen —
 * so navigating between sections is an instant content swap, not a a rebuilt
 * page. Secondary screens (Accounts, Budgets, …) render their existing
 * components unmodified inside the content area; only the dashboard route
 * itself gets a bespoke desktop layout (DashboardView.vue picks between the
 * two — see lib/breakpoint.ts).
 */
const auth = useAuthStore()
const sync = useSyncStore()
const route = useRoute()

const BASE_NAV: { to: string; label: string; icon: Component }[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/accounts', label: 'Accounts', icon: Banknote },
  { to: '/budgets', label: 'Budgets', icon: PiggyBank },
  { to: '/recurring', label: 'Recurring', icon: Repeat },
  { to: '/currencies', label: 'Currencies', icon: CircleDollarSign },
  { to: '/import', label: 'Import', icon: FileUp },
  { to: '/integrations', label: 'Integrations', icon: Plug },
  { to: '/account', label: 'Settings', icon: Settings },
]

// Same rule as the mobile menu drawer: the route itself bounces a non-admin
// straight back to the dashboard, so the link only appears for someone it
// would not be a dead end for.
const NAV = computed(() =>
  auth.user?.isAdmin
    ? [
        ...BASE_NAV,
        { to: '/admin/users', label: 'Users', icon: Users },
        { to: '/admin/settings', label: 'Instance', icon: SlidersHorizontal },
      ]
    : BASE_NAV,
)
</script>

<template>
  <div class="mx-auto flex min-h-full max-w-[1440px]">
    <aside
      class="sticky top-0 flex h-screen w-60 shrink-0 flex-col border-r border-mf-muted/25 bg-mf-surface p-4"
    >
      <div class="mb-6 flex items-center gap-2 px-2">
        <img src="/icon.svg" alt="" class="size-8" />
        <span class="text-lg font-semibold text-mf-green-dark">moneyfly</span>
      </div>

      <!--
        The desktop equivalent of the phone's two record buttons — without
        this, recording a spend from a laptop meant knowing /new/expense
        exists, since nothing on the dashboard itself leads there.
      -->
      <div class="mb-4 flex gap-2">
        <RouterLink
          to="/new/expense"
          class="flex flex-1 items-center justify-center gap-1.5 rounded-lg border border-mf-red-text/40 py-2 text-sm text-mf-red-text"
        >
          <Minus :size="16" :stroke-width="2" />
          Expense
        </RouterLink>
        <RouterLink
          to="/new/income"
          class="flex flex-1 items-center justify-center gap-1.5 rounded-lg border border-mf-green py-2 text-sm text-mf-green-dark"
        >
          <Plus :size="16" :stroke-width="2" />
          Income
        </RouterLink>
      </div>

      <nav class="flex flex-1 flex-col gap-1">
        <RouterLink
          v-for="item in NAV"
          :key="item.to"
          :to="item.to"
          class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm"
          :class="
            route.path === item.to
              ? 'bg-mf-green-soft/30 font-medium text-mf-green-dark'
              : 'text-mf-ink hover:bg-mf-bg'
          "
        >
          <component :is="item.icon" :size="18" :stroke-width="1.8" />
          {{ item.label }}
        </RouterLink>
      </nav>

      <div class="space-y-1 truncate px-2 pt-4 text-xs text-mf-muted">
        <p>{{ sync.detail }}</p>
        <p class="truncate">{{ auth.user?.email }}</p>
      </div>
    </aside>

    <main class="min-w-0 flex-1 p-6">
      <slot />
    </main>
  </div>
</template>
