<script setup lang="ts">
import { computed } from "vue";
import { useRouter } from "vue-router";
import ActionBar from "@/components/ActionBar.vue";
import ScriptsSource from "@/components/ScriptsSource.vue";
import SqlServerTarget from "@/components/SqlServerTarget.vue";
import { useMigratorStore } from "@/stores/migrator";
import { useUserConfigStore } from "@/stores/userConfig";

const config = useUserConfigStore();
const migrator = useMigratorStore();
const router = useRouter();

const canConnect = computed(
  () =>
    !migrator.isBusy &&
    config.server.trim() !== "" &&
    config.database.trim() !== "" &&
    // A password alone is a mistake: it needs its user.
    (migrator.password === "" || config.username.trim() !== ""),
);

async function onConnect() {
  if (!canConnect.value) {
    return;
  }

  await migrator.connect();

  if (migrator.isConnected) {
    await router.push("/update");
  }
}
</script>

<template>
  <form class="flex flex-1 flex-col md:min-h-0" @submit.prevent="onConnect">
    <main
      class="grid flex-1 grid-cols-1 gap-3 p-4 md:min-h-0 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]"
    >
      <ScriptsSource />

      <div
        class="hidden items-center justify-center text-dimmed md:flex"
        aria-hidden="true"
      >
        <div
          class="flex size-8 items-center justify-center rounded-full border border-default bg-default"
        >
          <UIcon name="i-lucide-arrow-right" class="size-4" />
        </div>
      </div>

      <SqlServerTarget />
    </main>

    <UAlert
      v-if="migrator.errorMessage"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      title="No se pudo conectar"
      :description="migrator.errorMessage"
      :ui="{ root: 'mx-4 mb-3 w-auto', description: 'selectable break-words' }"
    />

    <ActionBar>
      <UIcon name="i-lucide-folder-code" class="size-4 shrink-0" />
      <span class="truncate">{{ config.scriptsPath || "./Scripts" }}</span>
      <UIcon name="i-lucide-arrow-right" class="size-4 shrink-0" />
      <UIcon name="i-lucide-database" class="size-4 shrink-0" />
      <span class="truncate">
        {{ config.database || "Sin base de datos" }} · {{ config.server }}
      </span>

      <template #actions>
        <UButton
          type="submit"
          label="Conectar"
          icon="i-lucide-plug"
          size="lg"
          :loading="migrator.isBusy"
          :disabled="!canConnect"
        />
      </template>
    </ActionBar>
  </form>
</template>
