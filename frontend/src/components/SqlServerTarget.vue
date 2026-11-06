<script setup lang="ts">
import { computed } from "vue";
import InputPanel from "@/components/InputPanel.vue";
import PasswordInput from "@/components/PasswordInput.vue";
import { useMigratorStore } from "@/stores/migrator";
import { useUserConfigStore } from "@/stores/userConfig";

const config = useUserConfigStore();
const migrator = useMigratorStore();

// No user and no password: the Windows session is used.
const isWindowsAuth = computed(
  () => config.username.trim() === "" && migrator.password === "",
);
</script>

<template>
  <InputPanel
    step="Destino"
    title="Microsoft SQL Server"
    icon="i-lucide-server"
  >
    <div class="flex min-h-0 flex-col gap-4 p-4 md:overflow-y-auto">
      <UFormField
        label="Servidor"
        help="Ej.: localhost\SQLEXPRESS o 192.168.1.10,1433"
        required
      >
        <UInput
          v-model="config.server"
          placeholder="localhost\SQLEXPRESS"
          icon="i-lucide-server"
          autocomplete="off"
          :disabled="migrator.isBusy"
          class="w-full"
        />
      </UFormField>

      <UFormField label="Base de datos" required>
        <UInput
          v-model="config.database"
          placeholder="Nombre de la base de datos"
          icon="i-lucide-database"
          autocomplete="off"
          :disabled="migrator.isBusy"
          class="w-full"
        />
      </UFormField>

      <USeparator />

      <div class="flex items-center justify-between gap-2">
        <p class="text-sm font-medium text-highlighted">Autenticación</p>
        <UBadge
          :label="isWindowsAuth ? 'Windows' : 'SQL Server'"
          :icon="isWindowsAuth ? 'i-lucide-monitor' : 'i-lucide-key-round'"
          :color="isWindowsAuth ? 'neutral' : 'primary'"
          variant="soft"
        />
      </div>

      <div class="grid grid-cols-2 gap-3">
        <UFormField label="Usuario">
          <UInput
            v-model="config.username"
            placeholder="sa"
            icon="i-lucide-user"
            autocomplete="off"
            :disabled="migrator.isBusy"
            class="w-full"
          />
        </UFormField>

        <UFormField label="Contraseña">
          <PasswordInput
            v-model="migrator.password"
            placeholder="Contraseña"
            :disabled="migrator.isBusy"
            class="w-full"
          />
        </UFormField>
      </div>
    </div>

    <template #footer>
      <UIcon name="i-lucide-info" class="size-3.5 shrink-0" />
      <span>
        Sin usuario ni contraseña se usa tu sesión de Windows. Se necesitan
        permisos db_owner en la base de datos.
      </span>
    </template>
  </InputPanel>
</template>
