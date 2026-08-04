<script setup lang="ts">
withDefaults(defineProps<{ side?: 'left' | 'right' }>(), { side: 'left' })
defineEmits<{ close: [] }>()
</script>

<template>
  <!--
    Both of the reference's menus are drawers, not dropdowns: they slide in over
    the dashboard, dim it, and close when the dimmed area is tapped. One shell
    for both, differing only in which edge they come from.
  -->
  <div
    class="fixed inset-0 z-40 flex bg-black/30"
    :class="side === 'right' && 'justify-end'"
    @click.self="$emit('close')"
  >
    <aside
      class="flex w-[78%] max-w-xs flex-col overflow-y-auto bg-mf-bg shadow-2xl"
      :class="side === 'left' ? 'mf-slide-left' : 'mf-slide-right'"
    >
      <div class="pt-safe-t" />
      <slot />
      <div class="pb-[calc(1rem+var(--spacing-safe-b))]" />
    </aside>
  </div>
</template>

<style scoped>
/*
 * Kept local rather than as theme tokens: this is the drawer's own entrance, not
 * something another component should reach for.
 */
.mf-slide-left {
  animation: mf-in-left 180ms cubic-bezier(0.22, 0.61, 0.36, 1);
}
.mf-slide-right {
  animation: mf-in-right 180ms cubic-bezier(0.22, 0.61, 0.36, 1);
}
@keyframes mf-in-left {
  from {
    transform: translateX(-100%);
  }
}
@keyframes mf-in-right {
  from {
    transform: translateX(100%);
  }
}
</style>
