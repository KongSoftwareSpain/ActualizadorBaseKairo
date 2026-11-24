import { Dialogs, Events } from "@wailsio/runtime";
import { ApplyOptions } from "@bindings/actualizador/internal/migrator";
import {
  Job,
  type Event as ImporterEvent,
  type PlannedJob,
} from "@bindings/actualizador/internal/importer";
import { Plan, Run } from "@bindings/actualizador/internal/importer/service";
import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { useUserConfigStore } from "./userConfig.ts";

const ImporterEventName = "importer:event";
const MAX_KAIRO_PATHS = 10;

export type RowState = "pending" | "running" | "done" | "error";

export interface ImportRow {
  id: string;
  kairoPath: string;
  sqlPath: string;
  state: RowState;
  bytesDone: number;
  bytesTotal: number;
  message: string;
  warnings: string[];
  error: string;
  planned: PlannedJob | null;
}

let nextId = 1;

function newRow(sqlPath = ""): ImportRow {
  return {
    id: `row-${nextId++}`,
    kairoPath: "",
    sqlPath,
    state: "pending",
    bytesDone: 0,
    bytesTotal: 0,
    message: "",
    warnings: [],
    error: "",
    planned: null,
  };
}

export const useImporterStore = defineStore("importer", () => {
  // State
  const rows = ref<ImportRow[]>([newRow()]);
  const isBusy = ref(false);
  const runError = ref("");

  const canRun = computed(
    () =>
      !isBusy.value &&
      rows.value.length > 0 &&
      rows.value.every(
        (row) => row.kairoPath.trim() !== "" && row.sqlPath.trim() !== "",
      ),
  );

  // Private
  function findRow(id: string) {
    return rows.value.find((row) => row.id === id);
  }

  function onImporterEvent(event: { data: ImporterEvent }) {
    const data = event.data;
    const row = findRow(data.jobId);

    if (!row) {
      return;
    }

    row.bytesTotal = data.bytesTotal || row.bytesTotal;

    switch (data.kind) {
      case "job-start":
        row.state = "running";
        row.error = "";
        row.warnings = [];
        row.bytesDone = 0;
        row.message = data.message;
        break;
      case "progress":
        row.bytesDone = data.bytesDone;
        break;
      case "warning":
        row.warnings.push(data.message);
        break;
      case "info":
        row.message = data.message;
        break;
      case "job-done":
        row.state = "done";
        row.bytesDone = row.bytesTotal;
        row.message = data.message;
        break;
      case "job-error":
        row.state = "error";
        row.error = data.message;
        break;
    }
  }

  function toJobs() {
    return rows.value.map(
      (row) =>
        new Job({
          id: row.id,
          kairoPath: row.kairoPath.trim(),
          sqlPath: row.sqlPath.trim(),
        }),
    );
  }

  function rememberKairoPaths() {
    const config = useUserConfigStore();
    const used = rows.value.map((row) => row.kairoPath.trim());
    const merged = [...used, ...config.kairoPaths].filter(
      (path, index, all) => path !== "" && all.indexOf(path) === index,
    );

    config.kairoPaths = merged.slice(0, MAX_KAIRO_PATHS);
    config
      .save()
      .catch((error) => console.error("Failed to save config", error));
  }

  // Actions
  function addRow() {
    rows.value.push(newRow());
  }

  // One row per .sql; the Kairo folder of each one is still to choose.
  function addSqlFiles(paths: string[]) {
    const sqlPaths = paths.filter((path) =>
      path.toLowerCase().endsWith(".sql"),
    );
    const empty = rows.value.filter(
      (row) => row.kairoPath === "" && row.sqlPath === "",
    );

    sqlPaths.forEach((path, index) => {
      const row = empty[index];

      if (row) {
        row.sqlPath = path;
      } else {
        rows.value.push(newRow(path));
      }
    });
  }

  function removeRow(id: string) {
    rows.value = rows.value.filter((row) => row.id !== id);
  }

  async function pickKairoFolder(id: string) {
    const folder = await Dialogs.OpenFile({
      CanChooseDirectories: true,
      CanChooseFiles: false,
      Title: "Carpeta de Kairo (la que tiene config.ini)",
    });

    const row = findRow(id);
    if (row && typeof folder === "string" && folder !== "") {
      row.kairoPath = folder;
    }
  }

  async function pickSqlFile(id: string) {
    const file = await Dialogs.OpenFile({
      CanChooseFiles: true,
      Title: "Script .sql",
      Filters: [{ DisplayName: "Scripts SQL (*.sql)", Pattern: "*.sql" }],
    });

    const row = findRow(id);
    if (row && typeof file === "string" && file !== "") {
      row.sqlPath = file;
    }
  }

  async function pickSqlFiles() {
    const files = await Dialogs.OpenFile({
      CanChooseFiles: true,
      AllowsMultipleSelection: true,
      Title: "Scripts .sql",
      Filters: [{ DisplayName: "Scripts SQL (*.sql)", Pattern: "*.sql" }],
    });

    addSqlFiles(files);
  }

  // Checks every row without touching a database. True when all can run.
  async function plan() {
    runError.value = "";

    try {
      const planned = await Plan(toJobs());

      rows.value.forEach((row, index) => {
        row.planned = planned[index] ?? null;
        row.error = planned[index]?.error ?? "";
        row.state = row.error ? "error" : "pending";
      });

      return planned.every((item) => item.error === "");
    } catch (error) {
      runError.value = String(error);
      return false;
    }
  }

  async function run() {
    isBusy.value = true;
    runError.value = "";
    rememberKairoPaths();

    try {
      const results = await Run(
        toJobs(),
        new ApplyOptions({ allowOtherSessions: false, kickSessions: false }),
      );

      results.forEach((result) => {
        const row = findRow(result.id);

        if (!row) {
          return;
        }

        row.state = result.isDone ? "done" : "error";
        row.error = result.error;
        row.warnings = result.warnings ?? [];
      });
    } catch (error) {
      runError.value = String(error);
    } finally {
      isBusy.value = false;
    }
  }

  // Side effects
  Events.On(ImporterEventName, onImporterEvent);

  // The drop target of the window gives full paths (see EnableFileDrop in main.go).
  Events.On("files-dropped", (event: { data: { files: string[] } }) =>
    addSqlFiles(event.data.files),
  );

  return {
    rows,
    isBusy,
    runError,
    canRun,
    addRow,
    addSqlFiles,
    removeRow,
    pickKairoFolder,
    pickSqlFile,
    pickSqlFiles,
    plan,
    run,
  };
});
