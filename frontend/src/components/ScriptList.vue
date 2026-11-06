<script setup lang="ts">
import { formatVersion, type ScriptRow } from "@/stores/migrator";

defineProps<{ rows: ScriptRow[] }>();

// Literal icon names: Iconify bundles only the ones found in the source.
const stateIcons = {
  applied: "i-lucide-circle-check",
  done: "i-lucide-circle-check",
  pending: "i-lucide-circle",
  running: "i-lucide-loader-circle",
  failed: "i-lucide-circle-x",
} as const;

const stateClasses = {
  applied: "text-success",
  done: "text-success",
  pending: "text-dimmed",
  running: "animate-spin text-primary",
  failed: "text-error",
} as const;

const stateLabels = {
  applied: "Aplicado",
  done: "Aplicado ahora",
  pending: "Pendiente",
  running: "Aplicando",
  failed: "Con error",
} as const;
</script>

<template>
  <ul class="selectable divide-y divide-default">
    <li
      v-for="row in rows"
      :key="row.script.version"
      class="flex items-center gap-3 px-4 py-2.5"
      :class="{ 'bg-elevated': row.state === 'running' }"
    >
      <UIcon
        :name="stateIcons[row.state]"
        class="size-5 shrink-0"
        :class="stateClasses[row.state]"
        :aria-label="stateLabels[row.state]"
      />

      <div class="min-w-0 flex-1">
        <p class="truncate text-sm font-medium text-highlighted">
          {{ row.script.name }}
        </p>
        <p class="text-xs text-muted">
          {{ formatVersion(row.script.version) }} ·
          {{ stateLabels[row.state] }}
        </p>
      </div>
    </li>
  </ul>
</template>
