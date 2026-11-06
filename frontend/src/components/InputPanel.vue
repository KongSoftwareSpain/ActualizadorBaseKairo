<script setup lang="ts">
defineProps<{
  step: string;
  title: string;
  icon: string;
}>();
</script>

<template>
  <UCard
    variant="subtle"
    :ui="{
      root: 'flex min-h-0 flex-col',
      header: 'flex items-center gap-3 px-4 py-3 sm:px-4',
      body: 'flex min-h-0 flex-1 flex-col p-0 sm:p-0',
      footer:
        'flex items-center justify-center gap-1.5 px-4 py-2.5 text-xs text-dimmed sm:px-4',
    }"
  >
    <template #header>
      <div
        class="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary"
      >
        <UIcon :name="icon" class="size-5" />
      </div>

      <div class="min-w-0 flex-1">
        <p class="text-xs font-medium tracking-wide text-muted uppercase">
          {{ step }}
        </p>
        <h2 class="truncate font-semibold text-highlighted">{{ title }}</h2>
      </div>

      <slot name="actions" />
    </template>

    <slot />

    <template v-if="$slots.footer" #footer>
      <slot name="footer" />
    </template>
  </UCard>
</template>
