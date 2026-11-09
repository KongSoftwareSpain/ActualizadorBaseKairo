<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useRouter } from "vue-router";
import ActionBar from "@/components/ActionBar.vue";
import InputPanel from "@/components/InputPanel.vue";
import ScriptList from "@/components/ScriptList.vue";
import { formatVersion, useMigratorStore } from "@/stores/migrator";
import { useUserConfigStore } from "@/stores/userConfig";

const config = useUserConfigStore();
const migrator = useMigratorStore();
const router = useRouter();

const logElement = ref<HTMLElement | null>(null);

type ConfirmationKey = "allow" | "kick" | "baseline" | "clean";

interface Confirmation {
  title: string;
  description: string;
  label: string;
  icon: string;
  color: "warning" | "error";
  run: () => Promise<void>;
}

// Actions that are hard to undo ask first. One modal serves all of them.
const confirmations: Record<ConfirmationKey, Confirmation> = {
  allow: {
    title: "Actualizar con otros usuarios conectados",
    description:
      "Hay otras conexiones a la base de datos. Con el programa abierto, un script puede quedar bloqueado (falla a los 60 s y se revierte) y el programa trabaja con un esquema que no conoce. Lo recomendable es cerrarlo y volver a comprobar.",
    label: "Continuar de todos modos",
    icon: "i-lucide-triangle-alert",
    color: "warning",
    run: () => migrator.apply({ allowOtherSessions: true }),
  },
  kick: {
    title: "Expulsar a los demás usuarios",
    description:
      "Se cierran todas las demás conexiones a la base de datos durante la actualización. Lo que tengan sin guardar se pierde, y el programa abierto en esos equipos dejará de funcionar hasta que se reinicie.",
    label: "Expulsar y actualizar",
    icon: "i-lucide-user-x",
    color: "error",
    run: () => migrator.apply({ kickSessions: true }),
  },
  baseline: {
    title: "Registrar como versión base",
    description:
      "Se marca V0001 como aplicada sin ejecutar su script. Úsalo solo si la base de datos ya tiene el esquema completo (viene de Access): si no, las siguientes actualizaciones fallarán.",
    label: "Registrar como versión base",
    icon: "i-lucide-database",
    color: "warning",
    run: () => migrator.registerBaseline(),
  },
  clean: {
    title: "Limpiar la base de datos",
    // A getter: the text names the database and the sessions at the time it opens.
    get description() {
      const kicked = migrator.hasSessions
        ? " Hay otros usuarios conectados: se cerrarán sus conexiones."
        : "";

      return `Se eliminan TODOS los objetos de ${config.database} (tablas con sus datos, vistas, procedimientos, tipos y esquemas), sin backup y sin vuelta atrás. La base de datos queda vacía; al pulsar Actualizar se reconstruye desde V0001.${kicked}`;
    },
    label: "Eliminar todo",
    icon: "i-lucide-trash-2",
    color: "error",
    run: () => migrator.clean(migrator.hasSessions),
  },
};

const pendingConfirmation = ref<ConfirmationKey | null>(null);
const activeConfirmation = computed(() =>
  pendingConfirmation.value ? confirmations[pendingConfirmation.value] : null,
);

// The page is only reachable when connected (see the route guard), but the
// status goes back to null while leaving it: nothing here may assume it is set.
const status = computed(() => migrator.status);

const currentVersionLabel = computed(() => {
  const version = status.value?.currentVersion ?? 0;

  return version === 0 ? "Ninguna" : formatVersion(version);
});

const modifiedNames = computed(() =>
  (status.value?.modified ?? []).map((script) => script.name).join(", "),
);

// Without a value the bar is indeterminate, as while the backup is made.
const progressValue = computed(() =>
  migrator.isBusy && migrator.runProgress === 0 ? null : migrator.runProgress,
);

const isUpToDate = computed(
  () =>
    !migrator.hasPending &&
    !migrator.hasModified &&
    status.value?.needsBaseline === false,
);

function logClass(kind: string): string {
  switch (kind) {
    case "error":
      return "text-error";
    case "script-done":
      return "text-success";
    case "backup":
    case "warning":
      return "text-warning";
    case "script-start":
      return "text-highlighted";
    default:
      return "text-muted";
  }
}

async function onConfirm() {
  const confirmation = activeConfirmation.value;
  pendingConfirmation.value = null;

  await confirmation?.run();
}

async function onBack() {
  // Leave first: disconnecting clears the status this page is showing.
  await router.push("/input");
  await migrator.disconnect();
}

watch(
  () => migrator.log.length,
  async () => {
    await nextTick();

    if (logElement.value) {
      logElement.value.scrollTop = logElement.value.scrollHeight;
    }
  },
);
</script>

<template>
  <div v-if="status" class="flex flex-1 flex-col md:min-h-0">
    <main
      class="grid flex-1 grid-cols-1 gap-3 p-4 md:min-h-0 md:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]"
    >
      <InputPanel
        step="Scripts"
        title="Migraciones"
        icon="i-lucide-list-checks"
      >
        <template #actions>
          <UBadge
            :label="String(status.scripts.length)"
            color="neutral"
            variant="subtle"
          />
        </template>

        <div class="min-h-0 flex-1 md:overflow-y-auto">
          <ScriptList :rows="migrator.scriptRows" />
        </div>
      </InputPanel>

      <InputPanel
        step="Proceso"
        title="Actualización"
        icon="i-lucide-database-zap"
      >
        <div class="flex min-h-0 flex-1 flex-col gap-4 p-4">
          <div class="grid grid-cols-3 gap-4">
            <div>
              <p class="text-xs text-muted">Versión actual</p>
              <p class="text-lg font-semibold text-highlighted">
                {{ currentVersionLabel }}
              </p>
            </div>
            <div>
              <p class="text-xs text-muted">Aplicados</p>
              <p class="text-lg font-semibold text-highlighted">
                {{ status.appliedCount }}
              </p>
            </div>
            <div>
              <p class="text-xs text-muted">Pendientes</p>
              <p class="text-lg font-semibold text-highlighted">
                {{ status.pending.length }}
              </p>
            </div>
          </div>

          <UProgress
            v-if="migrator.isBusy || migrator.runProgress > 0"
            :model-value="progressValue"
            size="sm"
          />

          <UAlert
            v-if="migrator.hasSessions"
            color="warning"
            variant="subtle"
            icon="i-lucide-users"
            title="Hay otros usuarios conectados a la base de datos"
            :ui="{ actions: 'mt-2 gap-3' }"
            :actions="[
              {
                label: 'Volver a comprobar',
                size: 'xl',
                color: 'neutral',
                variant: 'outline',
                disabled: migrator.isBusy,
                onClick: () => migrator.refresh(),
              },
              {
                label: 'Continuar de todos modos',
                size: 'xl',
                color: 'warning',
                variant: 'solid',
                disabled: migrator.isBusy || !migrator.hasPending,
                onClick: () => (pendingConfirmation = 'allow'),
              },
              {
                label: 'Expulsar y continuar',
                size: 'xl',
                color: 'error',
                variant: 'outline',
                disabled: migrator.isBusy || !migrator.hasPending,
                onClick: () => (pendingConfirmation = 'kick'),
              },
            ]"
          >
            <template #description>
              <p>
                Lo habitual es un cliente que dejó el programa abierto. Ciérralo
                y vuelve a comprobar; actualizar con él abierto puede bloquear
                scripts y deja al programa con un esquema que no conoce.
              </p>
              <ul class="selectable mt-2 space-y-0.5 font-mono text-xs">
                <li v-for="session in migrator.sessions" :key="session.id">
                  #{{ session.id }} · {{ session.host || "?" }} ·
                  {{ session.program || "?" }} · {{ session.login }}
                </li>
              </ul>
            </template>
          </UAlert>

          <UAlert
            v-else-if="status.canSeeSessions === false && migrator.hasPending"
            color="neutral"
            variant="subtle"
            icon="i-lucide-user-search"
            title="No se pudo comprobar si hay otros usuarios conectados"
            description="El login no tiene VIEW SERVER STATE. Asegúrate de que el programa está cerrado en todos los equipos."
            :ui="{ actions: 'mt-2 gap-3' }"
            :actions="[
              {
                label: 'Expulsar y continuar',
                size: 'xl',
                color: 'error',
                variant: 'outline',
                disabled: migrator.isBusy,
                onClick: () => (pendingConfirmation = 'kick'),
              },
            ]"
          />

          <UAlert
            v-if="migrator.hasModified"
            color="error"
            variant="subtle"
            icon="i-lucide-file-warning"
            title="Scripts ya aplicados modificados"
            :description="`${modifiedNames}. Un script aplicado nunca se edita: restaura su contenido y haz el cambio en un script nuevo.`"
            :ui="{ description: 'selectable break-words' }"
          />

          <UAlert
            v-if="status.needsBaseline"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            title="Base de datos sin control de versiones"
            description="Tiene tablas pero no SchemaVersions: viene de Access. Regístrala como versión base (el script no se ejecuta) antes de actualizar."
            :ui="{ actions: 'mt-2 gap-3' }"
            :actions="[
              {
                label: 'Registrar como versión base',
                size: 'xl',
                color: 'warning',
                variant: 'solid',
                disabled: migrator.isBusy,
                onClick: () => (pendingConfirmation = 'baseline'),
              },
            ]"
          />

          <UAlert
            v-else-if="isUpToDate && !migrator.backupPath"
            color="success"
            variant="subtle"
            icon="i-lucide-circle-check"
            title="La base de datos está actualizada"
          />

          <UAlert
            v-if="migrator.backupPath"
            color="warning"
            variant="subtle"
            icon="i-lucide-archive"
            title="Backup previo"
            :description="migrator.backupPath"
            :ui="{ description: 'selectable break-all font-mono' }"
          />

          <UAlert
            v-if="migrator.errorMessage"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            title="No se pudo completar la actualización"
            :description="migrator.errorMessage"
            :ui="{ description: 'selectable break-words' }"
          />

          <div class="flex min-h-0 flex-1 flex-col gap-1.5">
            <div class="flex items-center justify-between">
              <p class="text-sm font-medium text-highlighted">Registro</p>
              <UButton
                label="Limpiar"
                icon="i-lucide-eraser"
                color="neutral"
                variant="ghost"
                size="xs"
                :disabled="migrator.log.length === 0"
                @click="migrator.clearLog()"
              />
            </div>

            <div
              ref="logElement"
              class="selectable min-h-32 flex-1 overflow-y-auto rounded-md border border-default bg-elevated/50 p-2 font-mono text-xs"
            >
              <p v-if="migrator.log.length === 0" class="text-muted">
                Sin actividad.
              </p>
              <p
                v-for="entry in migrator.log"
                :key="entry.id"
                class="break-words whitespace-pre-wrap"
                :class="logClass(entry.kind)"
              >
                <span class="text-dimmed">{{ entry.time }}</span>
                {{ entry.message }}
              </p>
            </div>
          </div>
        </div>
      </InputPanel>
    </main>

    <ActionBar>
      <UButton
        label="Volver"
        icon="i-lucide-arrow-left"
        color="neutral"
        variant="outline"
        :disabled="migrator.isBusy"
        @click="onBack"
      />
      <UIcon name="i-lucide-database" class="ml-2 size-4 shrink-0" />
      <span class="truncate">
        {{ config.database }} · {{ config.server }}
      </span>

      <template #actions>
        <UButton
          label="Limpiar BD"
          icon="i-lucide-trash-2"
          color="error"
          variant="outline"
          size="lg"
          :disabled="!migrator.canClean"
          @click="pendingConfirmation = 'clean'"
        />
        <UButton
          label="Actualizar"
          icon="i-lucide-database-zap"
          size="lg"
          :loading="migrator.isBusy"
          :disabled="!migrator.canApply"
          @click="migrator.apply()"
        />
      </template>
    </ActionBar>

    <UModal
      :open="activeConfirmation !== null"
      :title="activeConfirmation?.title"
      :description="activeConfirmation?.description"
      @update:open="(isOpen) => !isOpen && (pendingConfirmation = null)"
    >
      <template #footer>
        <UButton
          label="Cancelar"
          color="neutral"
          variant="outline"
          size="xl"
          @click="pendingConfirmation = null"
        />
        <UButton
          v-if="activeConfirmation"
          :label="activeConfirmation.label"
          :icon="activeConfirmation.icon"
          :color="activeConfirmation.color"
          size="xl"
          @click="onConfirm"
        />
      </template>
    </UModal>
  </div>
</template>
