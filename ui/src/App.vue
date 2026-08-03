<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import TabBar from '@/components/TabBar.vue'
import OfflineBanner from '@/components/OfflineBanner.vue'
import InstallHint from '@/components/InstallHint.vue'
import ToastHost from '@/components/ToastHost.vue'

const auth = useAuthStore()
const route = useRoute()

// The chrome is hidden on the login and forced-password-change screens: neither
// has anywhere to navigate to.
const showChrome = computed(
  () => !!auth.user && !auth.user.must_change_password && route.name !== 'login',
)

onMounted(() => {
  // Proves the SPA, the embedded assets and the API are genuinely connected
  // rather than three things that merely exist.
  auth.loadBuild().catch(() => undefined)
})
</script>

<template>
  <div class="min-h-screen overflow-x-hidden pb-20">
    <OfflineBanner />
    <a
      href="#main"
      class="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-lg focus:bg-brand-600 focus:px-3 focus:py-2 focus:text-white"
    >
      Skip to content
    </a>
    <main id="main" class="mx-auto w-full max-w-3xl px-4 py-4">
      <InstallHint v-if="showChrome" />
      <RouterView />
    </main>
    <TabBar v-if="showChrome" />
    <ToastHost />
  </div>
</template>
