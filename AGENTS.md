# AGENTS.md

Context for AI coding agents working in this repository. Keep it current when the architecture or conventions change.

## What this project is

**Actualizador**: desktop tool that applies versioned SQL scripts (migrations) to the SQL Server databases of a VB6 ERP/CRM. It is not the Access to SQL Server data migrator (that is a separate project, "Migrador"). The repository folder is `ActualizadorBaseKairo`.

## Platform

Windows only. Do not add cross-platform code (macOS/Linux/mobile/server/Docker build assets, `Mac*` Wails options, `_other.go` fallbacks).

## Tech stack

| Layer | Technology |
| --- | --- |
| Desktop shell | [Wails v3](https://v3.wails.io/), Go |
| Frontend | Vue 3 (`<script setup lang="ts">`), Vite, TypeScript |
| UI kit | [Nuxt UI v4](https://ui.nuxt.com/) used as a **Vue plugin — there is no Nuxt framework** |
| Styling | Tailwind CSS v4 (through Nuxt UI) |
| State | Pinia (setup-style stores) |
| Routing | vue-router with hash history |
| Icons | Lucide via Iconify (`i-lucide-*`) |
| Tasks | [Task](https://taskfile.dev/) (`Taskfile.yml`), driven by the `wails3` CLI |

## Repository layout

```
main.go                   Wails entry point: registers services, creates the window
internal/userconfig/      userconfig.Service: loads/saves user settings as JSON
internal/migrator/        migrator.Service: script loading, backup, baseline, apply; emits `migrator:event`
build/                    Wails build config, Windows Taskfile, icons, NSIS/MSIX installers
frontend/
  bindings/               GENERATED TypeScript bindings (gitignored, do not edit)
  src/                    main.ts, App.vue, components/, views/, stores/, style.css
  theme/                  Nuxt UI theme editor export (main.css, app.config.ts)
  vite.config.ts          Vite + Nuxt UI plugin config (theme app config, icons, aliases)
```

## Commands

| Task | Command |
| --- | --- |
| Dev mode | `wails3 dev` |
| Production build (output in `bin/`) | `wails3 build` |
| Regenerate bindings | `wails3 generate bindings -d frontend/bindings -ts -clean=true -time-type=Date` |
| Type-check frontend | `cd frontend && npx vue-tsc -b` |
| Check / apply frontend formatting | `cd frontend && npm run format:check` / `npm run format` |
| Format / vet Go | `gofmt -w .` / `go vet ./...` |

`wails3 dev` picks a free port for the Vite dev server each run, so several Wails projects can run at once (`WAILS_VITE_PORT` forces a port).

## Architecture

### Go ↔ frontend bridge

- A Go **service** is a struct whose exported methods are callable from the frontend. Register each in `main.go` with `application.NewService(&pkg.Service{})`.
- Wails generates TypeScript bindings into `frontend/bindings/<go-module>/<package path>/`. Import them through the `@bindings` alias. Never edit them by hand; regenerate after changing a service signature or bound struct.
- Generated methods return Promises. A Go method returning `(T, error)` resolves to `T` or rejects with the error.
- Go struct fields reach TypeScript under their `json` tag names. Always add camelCase `json` tags.
- `time.Time` fields are typed `Date` (`-time-type=Date`) but arrive as ISO strings. Parse with `new Date(value)`.
- Go → frontend events: `app.Event.Emit(name, data)`. Register typed events in `init()` with `application.RegisterEvent[T](name)`.

### Migrator

- `internal/migrator` loads `Scripts` (`VNNNN__desc.sql`, UTF-8 or UTF-16 with BOM; ANSI and BOM-less UTF-16 are rejected on purpose), takes a session `sp_getapplock`, backs up, then applies each script in its own transaction.
- One `.bak` per database (`<db>_preupdate.bak`, `WITH INIT`). After an update that did not finish, the database keeps the extended property `Actualizador.RollbackBackup`; a retry reuses that backup instead of overwriting it. The mark is removed when the update completes.
- `Script.Description` is the leading block of `--` comment lines of the file (up to the first blank or code line); the script list shows it. It is part of the checksummed content, so editing it on an applied script marks it `Modified`.
- Scripts must not change the database (`USE`): it is checked after every batch and the session is moved back after a rollback.
- Checksums are computed with LF line endings. An applied script whose checksum changed goes to `Status.Modified` and blocks `Apply`.
- Empty `BackupDir` (the default) backs up into the instance's own backup folder (`SERVERPROPERTY('InstanceDefaultBackupPath')`, else registry `BackupDirectory`): the SQL service account can always write there and the end user normally cannot read it. A custom `BackupDir` must be writable by the SQL Server service account, not by the user running the app (a sysadmin login does not change file permissions).
- Servers that are not local need an empty or absolute `BackupDir` (a path on that server) and are not trusted blindly (`TrustServerCertificate` is only set for local servers).
- Other sessions on the database (the ERP left open): `Status.Sessions` lists them (needs `VIEW SERVER STATE`; `CanSeeSessions` is false otherwise, so an empty list means unknown). `Apply` refuses while there are any unless `ApplyOptions.AllowOtherSessions`; `KickSessions` sets `SINGLE_USER WITH ROLLBACK IMMEDIATE` and always restores `MULTI_USER` (deferred). `SET LOCK_TIMEOUT` (60 s) makes a script blocked by another program fail instead of hanging. The UI exposes kicking only behind a confirmation modal.
- `Clean` (button "Limpiar BD", behind a confirmation modal) drops every user object (tables, views, routines, types, XML schema collections, non-system schemas, `SchemaVersions`) so the next `Apply` rebuilds from V0001. No backup, by design (development use). It does not call `Status`, so it works on a database newer than the scripts. Refuses system databases; same session policy as `Apply` (`holdSessions`). Drops are retried in passes until nothing more can go.
- Base folder (`Scripts`, `Logs`, `Backups`): the executable's folder; under `wails3 dev` (env `FRONTEND_DEVSERVER_URL` set) it is the working directory, i.e. the repository root.
- Do not call methods that use the connection while `isRunning` is set: the single connection is inside a transaction.

### User configuration

- `userconfig.Service` stores settings in `os.UserConfigDir()/Actualizador/config.json`. A missing file is not an error; defaults come from `userconfig.NewConfig()`.
- `useUserConfigStore` mirrors the config as refs. `main.ts` awaits `config.init()` before mounting, so the saved theme applies before first paint. Call `save()` for one-off changes and `scheduleSave()` for values that change on every keystroke.
- Never add passwords or other secrets to `userconfig.Config`.
- To add a setting: add the field (with `json` tag) and default in `internal/userconfig/service.go`, regenerate bindings, then add a ref plus the matching lines in `init()`, `save()` and the returned object of the store.
- Dark mode: the `dark` class on `<html>`; changes after init animate through `document.startViewTransition`.

### Frontend

- Nuxt UI components are auto-registered by the Vite plugin. Do not import them. `<UApp>` must stay the root wrapper in `App.vue`.
- Prefer Nuxt UI components and their `color`/`variant`/`:ui` props over custom markup. Use semantic classes (`text-muted`, `text-highlighted`, `border-default`, `bg-elevated`) instead of raw palette colors so both themes work.
- The theme in `frontend/theme/` is an unchanged export of the Nuxt UI theme editor; do not edit it by hand (see `frontend/theme/README.md`). Custom CSS goes in `style.css`.
- Icons: use `i-lucide-<name>` as literal strings (they are bundled by scanning the source).
- Text selection is disabled app-wide. Add the `selectable` class to text the user should copy.
- Routes are declared in `main.ts`: `/input` (connection, scripts and backup paths) and `/update` (script list, progress, log; guarded: it needs an open connection). `Buttons.vue` (`/buttons`) is a color/variant showcase, not a product page.
- Path aliases: `@/` → `frontend/src/`, `@bindings/` → `frontend/bindings/`.

## Conventions

- **Boolean names use a verb prefix**: `isDark`, `hasPassword`, `canSave`. Never bare adjectives.
- Go: descriptive receiver names (`service`, not `s`). Guard shared state with a mutex. Return errors to the frontend; no `log.Fatal` in service methods. Name exported identifiers without repeating the package name.
- TypeScript/Vue: Composition API, typed `defineProps`/`defineEmits`. Pinia setup stores grouped as `// State`, `// Private`, `// Actions`, `// Side effects`.
- Formatting: Prettier (double quotes, semicolons) and `gofmt`. Run them before finishing a change.
- Keep comments sparse and about *why*.
- Verify changes before finishing: `npx vue-tsc -b` and `npm run format:check` in `frontend/` for the frontend; `go vet ./...` and `gofmt -l .` in the repository root for Go.
