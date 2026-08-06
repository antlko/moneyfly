<script setup lang="ts">
import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import { Toaster } from 'vue-sonner'
import 'vue-sonner/style.css'

import DesktopShell from '@/components/desktop/DesktopShell.vue'
import { useIsDesktop } from '@/lib/breakpoint'
import { useAuthStore } from '@/stores/auth'

/**
 * The one place "the shell component picks the mobile Monefy frame or the
 * desktop dashboard" (CLAUDE.md) actually happens for the *chrome* — DashboardView
 * makes the same choice for the home route's own content (lib/breakpoint.ts).
 *
 * The route tree is identical either way: DesktopShell wraps RouterView, not
 * replaces it, so every existing screen renders unmodified inside it.
 */
const isDesktop = useIsDesktop()
const auth = useAuthStore()
const route = useRoute()
// The sidebar links to screens an unsigned-in visitor cannot reach and shows
// an account that does not exist yet — every public route (sign-in, the 404)
// gets the plain mobile-style frame regardless of width, the same shell a
// phone would show it in.
const showDesktopShell = computed(() => isDesktop.value && auth.isSignedIn && !route.meta.public)
</script>

<template>
  <DesktopShell v-if="showDesktopShell">
    <RouterView />
  </DesktopShell>

  <!--
    Screens fade in and rise slightly; the outgoing one only fades. Both are
    taken out of the flow for the length of the swap (see `.mf-page-*` in
    tailwind.css), otherwise the incoming screen is pushed a full viewport down
    while the old one is still there.

    `relative h-full` is what those absolute positions resolve against. Only
    the mobile frame animates a route change at all — a desktop content swap
    inside a persistent sidebar reads as *navigating within one app*, and a
    page-style fade would say the opposite.
  -->
  <div v-else class="relative h-full">
    <RouterView v-slot="{ Component }">
      <Transition name="mf-page" mode="default">
        <component :is="Component" />
      </Transition>
    </RouterView>
  </div>

  <!--
    Toasts sit above the record buttons rather than at the very bottom edge:
    down there they would cover the two controls the app exists for, and an
    "Undo" you have to reach around is not an undo.
  -->
  <Toaster position="bottom-center" :offset="{ bottom: '7.5rem' }" :duration="4000" rich-colors />
</template>
