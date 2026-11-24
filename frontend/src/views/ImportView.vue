<script setup lang="ts">
import { computed, ref } from "vue";
import ActionBar from "@/components/ActionBar.vue";
import PasswordInput from "@/components/PasswordInput.vue";
import { useImporterStore, type ImportRow } from "@/stores/importer";
import { useUserConfigStore } from "@/stores/userConfig";

const importer = useImporterStore();
const config = useUserConfigStore();

const isConfirmOpen = ref(false);

const stateColor = {
  pending: "neutral",
  running: "primary",
  done: "success",
  error: "error",
} as const;

const stateLabel = {
  pending: "Pendiente",
  running: "Importando",
  done: "Hecho",
  error: "Error",
} as const;

const plannedRows = computed(() =>
  importer.rows.filter((row) => row.planned !== null),
);

function percent(row: ImportRow) {
  return row.bytesTotal > 0
    ? Math.min(100, Math.round((row.bytesDone / row.bytesTotal) * 100))
    : 0;
}

async function onImport() {
  if (!importer.canRun) {
    return;
  }

  // Rows with a problem show it and nothing starts.
  if (await importer.plan()) {
    isConfirmOpen.value = true;
  }
}

async function onConfirm() {
  isConfirmOpen.value = false;
  await importer.run();
}
</script>

<template>
  <div class="flex flex-1 flex-col md:min-h-0" data-file-drop-target>
    <main class="flex flex-1 flex-col gap-3 p-4 md:min-h-0 md:overflow-y-auto">
      <div class="text-sm text-muted">
        Cada fila importa un .sql de SSMS en la base de datos del config.ini de
        su carpeta de Kairo. Se borra todo el contenido de esa base de datos
        antes de importar. Arrastra aquí uno o varios .sql para añadir filas.
      </div>

      <UCard>
        <div class="flex flex-col gap-3">
          <span class="text-sm font-semibold text-highlighted">
            Conexión al SQL Server
          </span>

          <div class="flex flex-wrap items-center gap-2">
            <UInput
              v-model="config.importUsername"
              placeholder="Usuario"
              icon="i-lucide-user"
              class="w-48"
              :disabled="importer.isBusy || importer.isWindowsLogin"
              :ui="{ base: 'selectable' }"
              @update:model-value="config.scheduleSave()"
            />
            <PasswordInput
              v-model="importer.sqlPassword"
              :placeholder="
                config.importUsername.trim().toLowerCase() === 'kong'
                  ? 'Contraseña predeterminada de kong'
                  : 'Contraseña'
              "
              class="w-72"
              :disabled="importer.isBusy || importer.isWindowsLogin"
            />
            <UCheckbox
              v-model="importer.isWindowsLogin"
              label="Usar mi usuario de Windows"
              :disabled="importer.isBusy"
            />
          </div>
        </div>
      </UCard>

      <datalist id="kairo-paths">
        <option v-for="path in config.kairoPaths" :key="path" :value="path" />
      </datalist>

      <UCard v-for="(row, index) in importer.rows" :key="row.id">
        <div class="flex flex-col gap-3">
          <div class="flex items-center gap-2">
            <span class="text-sm font-semibold text-highlighted">
              Fila {{ index + 1 }}
            </span>
            <UBadge
              :color="stateColor[row.state]"
              variant="subtle"
              :label="stateLabel[row.state]"
            />
            <span
              v-if="row.planned?.database"
              class="selectable text-sm text-muted"
            >
              {{ row.planned.server }} / {{ row.planned.database }} · login de
              Kairo:
              {{ row.planned.appUser }}
            </span>
            <UButton
              icon="i-lucide-trash-2"
              color="neutral"
              variant="ghost"
              class="ml-auto"
              aria-label="Quitar fila"
              :disabled="importer.isBusy"
              @click="importer.removeRow(row.id)"
            />
          </div>

          <div class="flex items-center gap-2">
            <UInput
              v-model="row.kairoPath"
              placeholder="Carpeta de Kairo (con config.ini)"
              icon="i-lucide-folder"
              class="flex-1"
              :disabled="importer.isBusy"
              :ui="{ base: 'selectable' }"
              list="kairo-paths"
            />
            <UButton
              label="Elegir…"
              color="neutral"
              variant="outline"
              :disabled="importer.isBusy"
              @click="importer.pickKairoFolder(row.id)"
            />
          </div>

          <div class="flex items-center gap-2">
            <UInput
              v-model="row.sqlPath"
              placeholder="Fichero .sql"
              icon="i-lucide-file-code"
              class="flex-1"
              :disabled="importer.isBusy"
              :ui="{ base: 'selectable' }"
            />
            <UButton
              label="Elegir…"
              color="neutral"
              variant="outline"
              :disabled="importer.isBusy"
              @click="importer.pickSqlFile(row.id)"
            />
          </div>

          <UProgress
            v-if="row.state === 'running' || row.state === 'done'"
            :model-value="percent(row)"
            size="md"
          />

          <UAlert
            v-if="row.error"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            title="No se pudo importar"
            :description="row.error"
            :ui="{ description: 'selectable break-words whitespace-pre-wrap' }"
          />

          <UAlert
            v-if="row.warnings.length > 0"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :title="`${row.warnings.length} aviso(s) (usuarios o roles que no existen en este equipo)`"
            :description="row.warnings.join('\n')"
            :ui="{ description: 'selectable break-words whitespace-pre-wrap' }"
          />
        </div>
      </UCard>

      <UAlert
        v-if="importer.runError"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        title="No se pudo empezar"
        :description="importer.runError"
        :ui="{ description: 'selectable break-words' }"
      />
    </main>

    <ActionBar>
      <UButton
        label="Añadir fila"
        icon="i-lucide-plus"
        color="neutral"
        variant="outline"
        :disabled="importer.isBusy"
        @click="importer.addRow()"
      />
      <UButton
        label="Añadir .sql…"
        icon="i-lucide-files"
        color="neutral"
        variant="outline"
        :disabled="importer.isBusy"
        @click="importer.pickSqlFiles()"
      />

      <UCheckbox
        v-model="importer.shouldKickSessions"
        label="Cerrar Kairo y otras conexiones abiertas a esas bases de datos"
        :disabled="importer.isBusy"
      />

      <template #actions>
        <UButton
          label="Importar"
          icon="i-lucide-database-backup"
          size="xl"
          :loading="importer.isBusy"
          :disabled="!importer.canRun"
          @click="onImport"
        />
      </template>
    </ActionBar>

    <UModal
      v-model:open="isConfirmOpen"
      title="Importar en estas bases de datos"
      description="Las que ya existen se vacían del todo antes de importar, sin copia de seguridad. Las que no existen se crean."
    >
      <template #body>
        <p v-if="importer.shouldKickSessions" class="mb-2 text-sm">
          Se cerrarán las conexiones abiertas a estas bases de datos (Kairo
          abierto, por ejemplo).
        </p>
        <ul class="selectable flex flex-col gap-1 text-sm">
          <li
            v-for="row in plannedRows"
            :key="row.id"
            :class="row.planned?.isNew ? 'text-muted' : 'text-error'"
          >
            {{ row.planned?.server }} / {{ row.planned?.database }} —
            {{ row.planned?.isNew ? "se creará" : "se borrará todo" }}
          </li>
        </ul>
      </template>

      <template #footer>
        <UButton
          label="Cancelar"
          color="neutral"
          variant="outline"
          size="xl"
          @click="isConfirmOpen = false"
        />
        <UButton
          label="Importar"
          icon="i-lucide-trash-2"
          color="error"
          size="xl"
          @click="onConfirm"
        />
      </template>
    </UModal>
  </div>
</template>
