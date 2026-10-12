<script setup lang="ts">
import {
  Banknote,
  ChartLine,
  ChevronLeft,
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
import { useEventListener } from '@vueuse/core'
import type { Component } from 'vue'
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import { opensKeyboard } from '@/lib/keyboard'

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
  { to: '/capital', label: 'Capital', icon: ChartLine },
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

/*
 * Collapsing to an icon rail.
 *
 * Remembered per device, in localStorage rather than as a synced setting: how
 * much room a sidebar deserves is a fact about this screen, not about the
 * person — the same account on a 13" laptop and a 32" monitor wants both. Every
 * access is guarded, because storage can be blocked outright (private windows,
 * site-data settings) and a sidebar is no reason for the app not to start.
 */
const COLLAPSED_KEY = 'moneyfly.sidebar.collapsed'
function readCollapsed(): boolean {
  try {
    return window.localStorage.getItem(COLLAPSED_KEY) === '1'
  } catch {
    return false
  }
}
const collapsed = ref(readCollapsed())
watch(collapsed, (value) => {
  try {
    window.localStorage.setItem(COLLAPSED_KEY, value ? '1' : '0')
  } catch {
    // Not remembered, then — it still works for this visit.
  }
})
const toggle = () => {
  collapsed.value = !collapsed.value
}

// `[` toggles it, the convention in tools that have one — but never while
// typing, where `[` is a character somebody meant.
useEventListener(window, 'keydown', (e: KeyboardEvent) => {
  if (e.key !== '[' || e.metaKey || e.ctrlKey || e.altKey) return
  if (opensKeyboard(document.activeElement as HTMLElement | null)) return
  toggle()
})

/**
 * The active link — a section's own sub-pages keep it lit. Matched on a whole
 * path segment, or `/account` (Settings) would light up on `/accounts`.
 */
const isActive = (to: string) =>
  route.path === to || (to !== '/' && route.path.startsWith(`${to}/`))
</script>

<template>
  <div class="flex min-h-full">
    <!--
      Flush against the left edge of the window, not centred in a max-width
      frame: a sidebar floating in a band of background reads as a card that
      wandered off, and on a wide monitor it left a strip of green on the left
      for no reason.

      The width animates and the content inside it does not move: every icon
      sits at the same x in both states (the 15px inset centres an 18px icon in
      the 72px rail), and only the labels fade. Things that slide sideways while
      the panel resizes are what make a collapse look busy.
    -->
    <!--
      The sticky frame is the hover group; the aside inside it clips its own
      content while it resizes, so the toggle — which hangs half outside it —
      has to live out here, or the clipping would cut it in half.
    -->
    <div class="group/sidebar sticky top-0 z-20 h-screen shrink-0">
      <aside
        class="flex h-full flex-col overflow-hidden border-r border-mf-muted/25 bg-mf-surface p-3 transition-[width] duration-[var(--mf-duration-panel)] ease-[var(--mf-ease-panel)]"
        :class="collapsed ? 'w-[4.5rem]' : 'w-60'"
      >
        <RouterLink to="/" class="mb-5 flex items-center gap-2 px-2 pt-1" aria-label="moneyfly">
          <img src="/icon.svg" alt="" class="size-8 shrink-0" />
          <span
            class="text-lg font-semibold whitespace-nowrap text-mf-green-dark transition-opacity duration-150"
            :class="collapsed && 'opacity-0'"
            >moneyfly</span
          >
        </RouterLink>

        <!--
        The desktop equivalent of the phone's two record buttons — without
        this, recording a spend from a laptop meant knowing /new/expense
        exists, since nothing on the dashboard itself leads there. Stacked, so
        collapsing only fades their labels instead of reflowing them.
      -->
        <div class="mb-4 flex flex-col gap-2">
          <RouterLink
            to="/new/expense"
            title="New expense"
            class="flex items-center gap-2 rounded-lg border border-mf-red-text/40 px-[14px] py-2 text-sm whitespace-nowrap text-mf-red-text hover:bg-mf-red/10"
          >
            <Minus :size="18" :stroke-width="2" class="shrink-0" />
            <span class="transition-opacity duration-150" :class="collapsed && 'opacity-0'"
              >Expense</span
            >
          </RouterLink>
          <RouterLink
            to="/new/income"
            title="New income"
            class="flex items-center gap-2 rounded-lg border border-mf-green px-[14px] py-2 text-sm whitespace-nowrap text-mf-green-dark hover:bg-mf-green-soft/20"
          >
            <Plus :size="18" :stroke-width="2" class="shrink-0" />
            <span class="transition-opacity duration-150" :class="collapsed && 'opacity-0'"
              >Income</span
            >
          </RouterLink>
        </div>

        <nav class="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
          <RouterLink
            v-for="item in NAV"
            :key="item.to"
            :to="item.to"
            :title="collapsed ? item.label : undefined"
            class="flex items-center gap-3 rounded-lg px-[15px] py-2 text-sm whitespace-nowrap"
            :class="
              isActive(item.to)
                ? 'bg-mf-green-soft/30 font-medium text-mf-green-dark'
                : 'text-mf-ink hover:bg-mf-bg'
            "
          >
            <component :is="item.icon" :size="18" :stroke-width="1.8" class="shrink-0" />
            <span class="transition-opacity duration-150" :class="collapsed && 'opacity-0'">{{
              item.label
            }}</span>
          </RouterLink>
        </nav>

        <div
          class="space-y-1 overflow-hidden px-2 pt-4 text-xs whitespace-nowrap text-mf-muted transition-opacity duration-150"
          :class="collapsed && 'invisible opacity-0'"
        >
          <p class="truncate">{{ sync.detail }}</p>
          <p class="truncate">{{ auth.user?.email }}</p>
        </div>
      </aside>

      <!--
      Collapse/expand: a round chevron sitting on the sidebar's edge, a fifth
      of the way down (centred there, whatever the window height), shown while
      the pointer is anywhere over the sidebar —
      the Atlassian/GitLab shape. On the edge because that is the thing it
      moves; hidden until hover so it is not one more control competing with
      the nav the rest of the time.

      Never hidden from the keyboard (focus-visible shows it) or from a touch
      screen at desktop width, which has no hover to reveal it with.
    -->
      <button
        type="button"
        class="absolute top-[20%] right-0 grid size-6 translate-x-1/2 -translate-y-1/2 place-items-center rounded-full bg-mf-surface text-mf-muted opacity-0 shadow-sm ring-1 ring-mf-muted/40 transition-[opacity,color,background-color] duration-150 group-hover/sidebar:opacity-100 hover:bg-mf-green hover:text-white hover:ring-mf-green focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-mf-green pointer-coarse:opacity-100"
        :aria-label="collapsed ? 'Expand sidebar' : 'Collapse sidebar'"
        :aria-expanded="!collapsed"
        :title="collapsed ? 'Expand sidebar  [' : 'Collapse sidebar  ['"
        @click="toggle"
      >
        <ChevronLeft
          :size="14"
          :stroke-width="2.25"
          class="transition-transform duration-[var(--mf-duration-panel)] ease-[var(--mf-ease-panel)]"
          :class="collapsed && 'rotate-180'"
        />
      </button>
    </div>

    <main class="min-w-0 flex-1 p-6">
      <div class="mx-auto max-w-[1280px]">
        <slot />
      </div>
    </main>
  </div>
</template>
