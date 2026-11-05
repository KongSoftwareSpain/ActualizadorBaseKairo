<script setup lang="ts">
import { useUserConfigStore } from "@/stores/userConfig";

const config = useUserConfigStore();

async function toggleTheme() {
  config.isDark = !config.isDark;
  await config.save();
}
</script>

<template>
  <header
    class="flex h-(--ui-header-height) shrink-0 items-center gap-3 border-b border-default bg-default px-4"
  >
    <div
      class="flex size-7 items-center justify-center rounded-md bg-primary text-inverted"
    >
      <UIcon name="i-lucide-layout-dashboard" class="size-4" />
    </div>

    <span class="font-semibold text-highlighted">My Product</span>

    <UTooltip :text="config.isDark ? 'Tema claro' : 'Tema oscuro'">
      <UButton
        :icon="config.isDark ? 'i-lucide-sun' : 'i-lucide-moon'"
        variant="ghost"
        color="neutral"
        class="ml-auto"
        :aria-label="config.isDark ? 'Usar tema claro' : 'Usar tema oscuro'"
        @click="toggleTheme"
      />
    </UTooltip>
  </header>
</template>
