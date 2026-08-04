<script setup lang="ts">
import { RouterView } from 'vue-router'
import { Toaster } from 'vue-sonner'
import 'vue-sonner/style.css'
</script>

<template>
  <RouterView />
  <!--
    Toasts sit above the record buttons rather than at the very bottom edge:
    down there they would cover the two controls the app exists for, and an
    "Undo" you have to reach around is not an undo.
  -->
  <Toaster position="bottom-center" :offset="{ bottom: '7.5rem' }" :duration="4000" rich-colors />
</template>
