<script setup lang="ts">
const colors = [
  "primary",
  "secondary",
  "success",
  "info",
  "warning",
  "error",
  "neutral",
] as const;
const variants = [
  "solid",
  "outline",
  "soft",
  "subtle",
  "ghost",
  "link",
] as const;
</script>

<template>
  <div class="p-8 pt-0 space-y-8">
    <h1 class="text-2xl font-bold">Button variants</h1>

    <div v-for="variant in variants" :key="variant" class="space-y-2">
      <h2 class="text-lg font-semibold capitalize">{{ variant }}</h2>
      <div class="flex flex-wrap gap-2">
        <UButton
          v-for="color in colors"
          :key="color"
          :color="color"
          :variant="variant"
          :label="color"
        />
      </div>
    </div>

    <div class="space-y-2">
      <h2 class="text-lg font-semibold">Disabled</h2>
      <div class="flex flex-wrap gap-2">
        <UButton
          v-for="color in colors"
          :key="color"
          :color="color"
          disabled
          :label="color"
        />
      </div>
    </div>

    <div class="space-y-2">
      <h2 class="text-lg font-semibold">Loading</h2>
      <div class="flex flex-wrap gap-2">
        <UButton
          v-for="color in colors"
          :key="color"
          :color="color"
          loading
          :label="color"
        />
      </div>
    </div>
  </div>
</template>
