<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'

const previousTitle = document.title
onMounted(() => { document.title = '404 Not Found' })
onUnmounted(() => { document.title = previousTitle })
</script>

<template>
  <main class="not-found"><h1>404 Not Found</h1></main>
</template>

<style scoped>
.not-found { min-height: 100vh; padding: 24px; box-sizing: border-box; color: #000; background: #fff; font-family: serif; }
h1 { margin: 0; font-size: 32px; font-weight: bold; }
</style>
