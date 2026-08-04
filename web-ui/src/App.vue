<script setup lang="ts">
import { RouterView } from 'vue-router'
import { Toaster } from 'vue-sonner'
import 'vue-sonner/style.css'
</script>

<template>
  <!--
    Screens fade in and rise slightly; the outgoing one only fades. Both are
    taken out of the flow for the length of the swap (see `.mf-page-*` in
    tailwind.css), otherwise the incoming screen is pushed a full viewport down
    while the old one is still there.

    `relative h-full` is what those absolute positions resolve against.
  -->
  <div class="relative h-full">
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
