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
    class="fixed inset-0 z-40 flex mf-scrim"
    :class="side === 'right' && 'justify-end'"
    @click.self="$emit('close')"
  >
    <!--
      The slide itself belongs to the `mf-drawer-*` transition wrapped around
      this component at the call site — a keyframe here could only animate the
      way in, and a drawer that snaps out of existence is the half that gets
      noticed.
    -->
    <aside class="flex w-[78%] max-w-xs flex-col overflow-y-auto bg-mf-bg shadow-2xl">
      <div class="pt-safe-t" />
      <slot />
      <div class="pb-[calc(1rem+var(--spacing-safe-b))]" />
    </aside>
  </div>
</template>
