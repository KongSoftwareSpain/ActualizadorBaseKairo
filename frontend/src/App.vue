<script setup lang="ts">
import AppHeader from "./components/AppHeader.vue";
</script>

<template>
  <UApp>
    <div class="flex h-dvh flex-col">
      <AppHeader />

      <div class="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <RouterView />
      </div>
    </div>
  </UApp>
</template>
