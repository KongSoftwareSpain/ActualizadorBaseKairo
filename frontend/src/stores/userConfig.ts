import { Config as UserConfig } from "@bindings/actualizador/internal/userconfig";
import {
  LoadFromFile,
  SaveToFile,
} from "@bindings/actualizador/internal/userconfig/service";
import { defineStore } from "pinia";
import { nextTick, ref, watch } from "vue";

export const useUserConfigStore = defineStore("userConfig", () => {
  // State
  const isDark = ref(false);
  const server = ref("localhost\\SQLEXPRESS");
  const database = ref("");
  const username = ref("");
  const scriptsPath = ref("./Scripts");
  const backupDir = ref("./Backups");

  // Private
  let isInitialized = false;
  let saveTimeout: ReturnType<typeof setTimeout> | undefined;

  function applyDarkMode(value: boolean) {
    document.documentElement.classList.toggle("dark", value);
  }

  function onIsDarkChanged(value: boolean) {
    if (!isInitialized || !document.startViewTransition) {
      applyDarkMode(value);
    } else {
      document.startViewTransition(() => applyDarkMode(value));
    }
  }

  // Actions
  async function init() {
    // A settings file that cannot be read must not keep the app from opening:
    // the refs keep their defaults.
    try {
      const config = await LoadFromFile();

      isDark.value = config.isDark;
      server.value = config.server;
      database.value = config.database;
      username.value = config.username;
      scriptsPath.value = config.scriptsPath;
      backupDir.value = config.backupDir;
    } catch (error) {
      console.error("Failed to load config, using defaults", error);
    }

    // Let the watcher apply the loaded theme without a view transition
    // before marking the store as initialized.
    await nextTick();
    isInitialized = true;
  }

  async function save() {
    clearTimeout(saveTimeout);

    const config = new UserConfig();

    config.isDark = isDark.value;
    config.server = server.value;
    config.database = database.value;
    config.username = username.value;
    config.scriptsPath = scriptsPath.value;
    config.backupDir = backupDir.value;

    await SaveToFile(config);
  }

  // Debounced save for values that change on every keystroke.
  function scheduleSave() {
    clearTimeout(saveTimeout);
    saveTimeout = setTimeout(() => {
      save().catch((error) => console.error("Failed to save config", error));
    }, 300);
  }

  // Side effects
  watch(isDark, onIsDarkChanged, { immediate: true });

  return {
    isDark,
    server,
    database,
    username,
    scriptsPath,
    backupDir,
    init,
    save,
    scheduleSave,
  };
});
