import { Config as UserConfig } from "@bindings/changeme/internal/userconfig";
import {
  LoadFromFile,
  SaveToFile,
} from "@bindings/changeme/internal/userconfig/service";
import { defineStore } from "pinia";
import { nextTick, ref, watch } from "vue";

export const useUserConfigStore = defineStore("userConfig", () => {
  // State
  const isDark = ref(false);

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
    const config = await LoadFromFile();

    isDark.value = config.isDark;

    // Let the watcher apply the loaded theme without a view transition
    // before marking the store as initialized.
    await nextTick();
    isInitialized = true;
  }

  async function save() {
    clearTimeout(saveTimeout);

    const config = new UserConfig();

    config.isDark = isDark.value;

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
    init,
    save,
    scheduleSave,
  };
});
