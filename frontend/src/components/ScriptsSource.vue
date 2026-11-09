<script setup lang="ts">
import InputPanel from "@/components/InputPanel.vue";
import { useMigratorStore } from "@/stores/migrator";
import { useUserConfigStore } from "@/stores/userConfig";

const config = useUserConfigStore();
const migrator = useMigratorStore();
</script>

<template>
  <InputPanel
    step="Origen"
    title="Scripts de migración"
    icon="i-lucide-folder-code"
  >
    <div class="flex min-h-0 flex-col gap-4 p-4 md:overflow-y-auto">
      <UFormField
        label="Carpeta de scripts"
        help="./Scripts es la carpeta junto al ejecutable. Acepta rutas absolutas, por ejemplo las de otro repositorio."
      >
        <UInput
          v-model="config.scriptsPath"
          placeholder="./Scripts"
          icon="i-lucide-folder"
          autocomplete="off"
          :disabled="migrator.isBusy"
          class="w-full"
        />
      </UFormField>

      <USeparator />

      <UFormField
        label="Carpeta de backups"
        help="Vacío usa la carpeta de backups del servidor SQL (recomendado: el servicio ya puede escribir ahí). Si indicas otra, debe poder escribirla el servicio de SQL Server."
      >
        <UInput
          v-model="config.backupDir"
          placeholder="Carpeta de backups del servidor"
          icon="i-lucide-archive"
          autocomplete="off"
          :disabled="migrator.isBusy"
          class="w-full"
        />
      </UFormField>
    </div>

    <template #footer>
      <UIcon name="i-lucide-info" class="size-3.5 shrink-0" />
      <span>Los scripts se llaman VNNNN__descripcion.sql.</span>
    </template>
  </InputPanel>
</template>
