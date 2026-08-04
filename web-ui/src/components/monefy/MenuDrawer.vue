<script setup lang="ts">
import { Banknote, BookOpen, CircleDollarSign, NotebookTabs, Settings } from '@lucide/vue'
import type { Component } from 'vue'

import AppDrawer from './AppDrawer.vue'

const emit = defineEmits<{ close: []; go: [string] }>()

/** The reference's right-hand drawer: a large icon over each label, one per row. */
const ITEMS: { to: string; label: string; icon: Component; ready: boolean }[] = [
  { to: '/categories', label: 'Categories', icon: NotebookTabs, ready: false },
  { to: '/accounts', label: 'Accounts', icon: Banknote, ready: true },
  { to: '/currencies', label: 'Currencies', icon: CircleDollarSign, ready: true },
  { to: '/account', label: 'Settings', icon: Settings, ready: true },
  { to: '/guides', label: 'Guides', icon: BookOpen, ready: false },
]

function go(item: (typeof ITEMS)[number]) {
  if (!item.ready) return
  emit('go', item.to)
  emit('close')
}
</script>

<template>
  <AppDrawer side="right" @close="emit('close')">
    <nav class="flex flex-col gap-2 p-4">
      <button
        v-for="item in ITEMS"
        :key="item.to"
        type="button"
        class="flex flex-col items-center gap-2 rounded-xl py-5 transition"
        :class="item.ready ? 'text-mf-green-dark active:bg-mf-green-soft/30' : 'text-mf-muted'"
        :disabled="!item.ready"
        @click="go(item)"
      >
        <component :is="item.icon" :size="40" :stroke-width="1.4" />
        <span class="text-base" :class="item.ready ? 'text-mf-ink' : ''">{{ item.label }}</span>
        <!-- Unbuilt destinations stay visible but plainly inert, rather than
             disappearing and making the menu look different every release. -->
        <span v-if="!item.ready" class="text-[10px] tracking-wide uppercase">soon</span>
      </button>
    </nav>
  </AppDrawer>
</template>
