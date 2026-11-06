import { Events } from "@wailsio/runtime";
import {
  ApplyOptions,
  Event as MigratorLogEvent,
  ConnectionParams,
  Script,
  Status,
} from "@bindings/actualizador/internal/migrator/models";
import {
  Apply,
  Connect,
  Disconnect,
  RegisterBaseline,
  Status as ReadStatus,
} from "@bindings/actualizador/internal/migrator/service";
import { MigratorEvent } from "@bindings/events";
import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { useUserConfigStore } from "./userConfig";

const MAX_LOG_ENTRIES = 1000;

export type ScriptState = "applied" | "pending" | "running" | "done" | "failed";

export interface LogEntry {
  id: number;
  time: string;
  kind: string;
  message: string;
}

export interface ScriptRow {
  script: Script;
  state: ScriptState;
}

export function formatVersion(version: number): string {
  return `V${String(version).padStart(4, "0")}`;
}

function toMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }

  return String(error);
}

export const useMigratorStore = defineStore("migrator", () => {
  // State
  // The password lives only in memory: it is never saved to the config file.
  const password = ref("");
  const status = ref<Status | null>(null);
  const isBusy = ref(false);
  const backupPath = ref("");
  const errorMessage = ref("");
  const log = ref<LogEntry[]>([]);
  const runningVersion = ref(0);
  const failedVersion = ref(0);
  const doneVersions = ref<number[]>([]);
  const runTotal = ref(0);

  const isConnected = computed(() => status.value !== null);
  const hasPending = computed(() => (status.value?.pending.length ?? 0) > 0);
  // Applied scripts edited afterwards: the update is refused until restored.
  const hasModified = computed(() => (status.value?.modified.length ?? 0) > 0);
  // Other connections (the ERP left open). Applying needs an explicit choice.
  const sessions = computed(() => status.value?.sessions ?? []);
  const hasSessions = computed(() => sessions.value.length > 0);
  const canApply = computed(
    () =>
      isConnected.value &&
      !isBusy.value &&
      hasPending.value &&
      !hasModified.value &&
      !hasSessions.value &&
      !status.value?.needsBaseline,
  );

  // Every script with its state. During an update, scripts finished in this
  // run show as "done" before the status is refreshed.
  const scriptRows = computed<ScriptRow[]>(() => {
    if (!status.value) {
      return [];
    }

    const pending = new Set(status.value.pending.map((s) => s.version));

    return status.value.scripts.map((script) => {
      let state: ScriptState = "pending";

      if (!pending.has(script.version)) {
        state = "applied";
      } else if (doneVersions.value.includes(script.version)) {
        state = "done";
      } else if (script.version === runningVersion.value) {
        state = "running";
      } else if (script.version === failedVersion.value) {
        state = "failed";
      }

      return { script, state };
    });
  });

  // Share of the scripts of the current run already committed, 0 to 100.
  const runProgress = computed(() =>
    runTotal.value === 0
      ? 0
      : Math.round((doneVersions.value.length / runTotal.value) * 100),
  );

  // Private
  let nextLogId = 0;

  function addLog(kind: string, message: string) {
    log.value.push({
      id: nextLogId++,
      time: new Date().toLocaleTimeString(),
      kind,
      message,
    });

    if (log.value.length > MAX_LOG_ENTRIES) {
      log.value.splice(0, log.value.length - MAX_LOG_ENTRIES);
    }
  }

  function onMigratorEvent(event: { data: MigratorLogEvent }) {
    const { kind, version, message } = event.data;

    if (kind === "script-start") {
      runningVersion.value = version;
    } else if (kind === "script-done") {
      runningVersion.value = 0;
      doneVersions.value.push(version);
    } else if (kind === "error" && version > 0) {
      runningVersion.value = 0;
      failedVersion.value = version;
    }

    addLog(kind, message);
  }

  function resetRun() {
    runningVersion.value = 0;
    failedVersion.value = 0;
    doneVersions.value = [];
    runTotal.value = 0;
  }

  async function run(action: () => Promise<void>) {
    isBusy.value = true;
    errorMessage.value = "";

    try {
      await action();
    } catch (error) {
      errorMessage.value = toMessage(error);
      addLog("error", errorMessage.value);
    } finally {
      isBusy.value = false;
    }
  }

  async function refreshStatus() {
    try {
      status.value = await ReadStatus();
    } catch {
      // Keep the last known status; the error is already in the log.
    }
  }

  // Actions
  async function connect() {
    const config = useUserConfigStore();

    const params = new ConnectionParams({
      server: config.server,
      database: config.database,
      username: config.username,
      password: password.value,
      scriptsPath: config.scriptsPath,
      backupDir: config.backupDir,
    });

    await run(async () => {
      status.value = null;
      backupPath.value = "";
      resetRun();

      status.value = await Connect(params);

      // Remember the values only once they connected successfully. Failing to
      // save is not a failed connection: it only goes to the log.
      try {
        await config.save();
      } catch (error) {
        addLog(
          "warning",
          `No se pudo guardar la configuración: ${toMessage(error)}`,
        );
      }
    });
  }

  async function refresh() {
    await run(async () => {
      status.value = await ReadStatus();
    });
  }

  async function apply(
    options: { allowOtherSessions?: boolean; kickSessions?: boolean } = {},
  ) {
    await run(async () => {
      backupPath.value = "";
      resetRun();
      runTotal.value = status.value?.pending.length ?? 0;

      try {
        const result = await Apply(new ApplyOptions(options));
        status.value = result.status;
        backupPath.value = result.backupPath;
      } finally {
        // Applied scripts stay committed even when a later one fails.
        await refreshStatus();
      }
    });
  }

  async function registerBaseline() {
    await run(async () => {
      status.value = await RegisterBaseline();
    });
  }

  async function disconnect() {
    await Disconnect();
    status.value = null;
    backupPath.value = "";
    errorMessage.value = "";
    resetRun();
  }

  function clearLog() {
    log.value = [];
  }

  // Side effects
  Events.On(MigratorEvent, onMigratorEvent);

  return {
    password,
    status,
    isBusy,
    backupPath,
    errorMessage,
    log,
    runningVersion,
    runProgress,
    scriptRows,
    isConnected,
    hasPending,
    hasModified,
    sessions,
    hasSessions,
    canApply,
    connect,
    refresh,
    apply,
    registerBaseline,
    disconnect,
    clearLog,
  };
});
