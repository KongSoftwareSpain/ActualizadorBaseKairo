# Importar .sql en BD local Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pantalla `/import` en el Actualizador que, para una lista de parejas (carpeta de Kairo + `.sql` de SSMS), limpia la BD local del `config.ini` e importa el script, una pareja detrás de otra.

**Architecture:** Parser de lotes en streaming (`internal/sqlscript`, puro). `migrator` expone lo mínimo para abrir un `Runner` con usuario/contraseña y ejecutar lotes. `internal/importer.Service` (Wails) hace `Plan`/`Run` y emite `importer:event`. Vista Vue `ImportView` con lista de trabajos.

**Tech Stack:** Go (go-mssqldb, Wails v3), Vue 3 + Nuxt UI + Pinia, vue-tsc, Prettier.

**Spec:** `docs/superpowers/specs/2026-11-24-importar-sql-design.md`

## Global Constraints

- Windows solo. Módulo Go `actualizador`. Convenciones de `AGENTS.md`: booleanos con verbo (`isX`, `hasX`, `canX`), tags `json` camelCase, receptores descriptivos (`service`, no `s`), `gofmt`, Prettier (comillas dobles, punto y coma), textos de UI y errores en español.
- Usuario de BD siempre `kong` y contraseña siempre la del login `kong` (decisión del usuario; no se lee `Usuario` del ini ni se usa la constante de `modConfigBD.bas`). Es el login del modo `--silent`: se reutiliza `silentPassword` de `main.go`, inyectada al compilar con `KAIRO_SILENT_PASSWORD` por `build/windows/silent-password.ps1`. La contraseña NO se escribe en el código, el plan, el spec ni git. Servidor y BD: `[BaseDatos] Servidor` y `BaseDatos` del ini.
- Solo servidor local (`isLocalServer`). Servidor remoto se rechaza.
- Sin backup previo. `Clean` antes de importar.
- Lectura en streaming: el `.sql` real pesa 45 MB, UTF-16 LE con BOM, líneas de hasta 5.071 caracteres (no usar `bufio.Scanner` con el límite por defecto), 5.645 `GO`.
- Un trabajo fallido no detiene los siguientes. Dos trabajos con mismo servidor+BD se rechazan en `Plan`.
- Nunca añadir secretos a `userconfig.Config`.

## Review Focus

Entradas que el spec implica y ninguna tarea prueba por sí sola; cada una tiene su test en la tarea indicada.

1. `INSERT` con una cadena multilínea que contiene una línea que es solo `GO`: no debe partir el lote (Task 1).
2. `.sql` sin BOM (UTF-8 plano) o vacío: el primero se lee como UTF-8, el segundo da error claro "no contiene sentencias" (Tasks 1 y 3).
3. `CREATE USER` / `ALTER ROLE ... ADD MEMBER` que fallan porque el login no existe o el usuario ya existe: aviso, no error (Task 1 clasifica, Task 3 lo aplica).
4. Carpeta de Kairo sin `config.ini`, ini sin `[BaseDatos]`, o el usuario eligió el propio `config.ini` en vez de la carpeta (Task 3).
5. Servidores equivalentes escritos distinto (`.\SQLEXPRESS`, `localhost\SQLEXPRESS`, `(local)\SQLEXPRESS`) y nombre de BD en otra capitalización se detectan como duplicado (Task 3).

---

### Task 1: Parser de lotes `internal/sqlscript`

**Files:**
- Create: `internal/sqlscript/sqlscript.go`
- Test: `internal/sqlscript/sqlscript_test.go`

**Interfaces:**
- Produces:
  - `type Batch struct { Number int; Text string; IsSkipped bool; IsPrincipal bool }` (`Number` desde 1, `IsSkipped`: cabecera/servidor, no se ejecuta; `IsPrincipal`: primera sentencia es `CREATE USER`, `ALTER ROLE` o `ALTER AUTHORIZATION`, cuyo fallo es solo aviso)
  - `func NewReader(source io.Reader) (io.Reader, error)` devuelve texto UTF-8: detecta BOM UTF-16 LE/BE y UTF-8; sin BOM se trata como UTF-8
  - `func ReadBatches(source io.Reader, handle func(Batch) error) error` lee con `NewReader`, parte por líneas `GO` y llama a `handle` en orden; si `handle` devuelve error, para y lo devuelve
  - `var ErrNoStatements = errors.New("el script no contiene sentencias")`, devuelto por `ReadBatches` si no hay ningún lote no vacío

- [ ] **Step 1: Escribir los tests** (nombres y aserciones):
  - `TestReadBatchesSplitsOnGo`: `"A\r\nGO\r\nB\r\ngo 2\r\n"` da lotes `A`, `B`; `GO` en mayúsculas/minúsculas, con espacios alrededor o con contador, es separador; `GOTO x` no lo es.
  - `TestReadBatchesIgnoresGoInsideString`: `"INSERT t VALUES (N'linea1\r\nGO\r\nlinea2')\r\nGO\r\n"` da un solo lote que contiene `linea2`. Las comillas dobladas `''` no cierran la cadena.
  - `TestNewReaderDecodesUTF16LEBOMUTF16BEBOMUTF8BOMAndPlain`: los cuatro orígenes con el texto `"SELECT 'ñ'"` dan el mismo texto.
  - `TestHeaderIsSkipped`: script `USE [master]\nGO\nCREATE DATABASE [X]\n ON PRIMARY (NAME=N'X')\nGO\nALTER DATABASE [X] SET RECOVERY SIMPLE\nGO\nUSE [X]\nGO\nCREATE TABLE t (a int)\nGO\nUSE [master]\nGO\nALTER DATABASE [X] SET READ_WRITE\nGO\n` marca como `IsSkipped` todo menos `CREATE TABLE t (a int)`. Un lote `IF (1 = FULLTEXTSERVICEPROPERTY('IsFullTextInstalled')) begin EXEC [X].[dbo].[sp_fulltext_database] @action = 'enable' end` entre `CREATE DATABASE` y el primer `USE [X]` también se omite.
  - `TestScriptWithoutCreateDatabaseOnlySkipsUseAndAlterDatabase`: `CREATE TABLE a (x int)\nGO\nUSE [Z]\nGO\nCREATE TABLE b (x int)\nGO\n` omite solo `USE [Z]`.
  - `TestPrincipalBatches`: `CREATE USER [k] FOR LOGIN [k]`, `ALTER ROLE [db_datareader] ADD MEMBER [k]`, `ALTER AUTHORIZATION ON SCHEMA::x TO y` → `IsPrincipal`; `CREATE TABLE` no. Un comentario `/****** Objeto: ... ******/` antes de la sentencia no impide la clasificación (se ignoran comentarios `--` y `/* */` iniciales).
  - `TestEmptyScript`: entrada vacía o solo `GO` → `ErrNoStatements`.
  - `TestLongLine`: una línea de 200.000 caracteres se lee entera.
  - `TestRealScript` (se salta si no existe `C:\Users\MIGUEL\Desktop\Tecnicos\script.sql`): 5.645 `GO` (el último lote puede estar vacío), 364 `CREATE TABLE` no omitidos, ninguna sentencia `CREATE DATABASE` ni `ALTER DATABASE` sin omitir.
- [ ] **Step 2: Ejecutar** `go test ./internal/sqlscript/...` y comprobar que falla por símbolos no definidos.
- [ ] **Step 3: Implementar** las firmas de arriba en `internal/sqlscript/sqlscript.go`. Leer con `bufio.Reader.ReadString('\n')` (no `Scanner`). Seguir el estado "dentro de cadena" contando comillas simples por línea (`''` cuenta par); una línea es separador solo si no se está dentro de cadena. Para clasificar, quitar comentarios iniciales y espacios, y mirar el primer token(s) sin distinguir mayúsculas. Regla de omisión: (a) primera sentencia `USE`, `CREATE DATABASE` o `ALTER DATABASE` ⇒ omitido; (b) si el script contiene un `CREATE DATABASE`, todo lote anterior al primer `USE` posterior a él se omite también.
- [ ] **Step 4: Ejecutar** `go test ./internal/sqlscript/...` → PASS.
- [ ] **Step 5: Commit** `git add internal/sqlscript && git commit -m "feat: parser de lotes SQL en streaming para importar scripts"`.

---

### Task 2: `migrator` expone abrir `Runner` y ejecutar lotes

**Files:**
- Modify: `internal/migrator/service.go` (exportar `isLocalServer` y añadir `Open`)
- Modify: `internal/migrator/runner.go` (añadir métodos de `Runner`)
- Test: `internal/migrator/runner_test.go` (extender)

**Interfaces:**
- Consumes: `ConnectionParams`, `buildDSN`, `describeConnectError`, `NewRunner`, `Runner.Lock`, `Runner.Clean`, `Runner.pinDatabase`, `ApplyOptions`, `Event`.
- Produces:
  - `func IsLocalServer(server string) bool` (renombre de `isLocalServer`, actualizar usos y tests)
  - `func Open(params ConnectionParams, emit func(Event)) (runner *Runner, closeAll func(), err error)`: `buildDSN` + `sql.Open` + `db.Conn` (timeout 20 s, errores con `describeConnectError`) + `NewRunner(conn, nil, "", emit)` + `Lock`. `closeAll` cierra el `Runner` y el `*sql.DB`. En error devuelve `closeAll` nulo y ya ha liberado todo.
  - `func (runner *Runner) Database() string` (BD fijada de la conexión, válida tras `Lock`)
  - `func (runner *Runner) ExecBatch(ctx context.Context, text string) error`: ejecuta el lote con la conexión fija; después llama a `pinDatabase` para volver a la BD si el lote hizo `USE`.
  - `Clean` ya es público en `Runner`; no se toca.

- [ ] **Step 1: Test** `TestIsLocalServerExported` (cambia el nombre de los casos existentes de `isLocalServer`) y `TestOpenRejectsMissingServer`: `Open(ConnectionParams{Database: "x"}, nil)` devuelve error `falta el servidor` y `closeAll == nil`. Si ya hay un test de integración con SQL Server en `runner_test.go` gobernado por variables de entorno, añadir ahí `TestExecBatchRunsAndRepins` con el mismo gating: ejecutar `USE master` con `ExecBatch` y comprobar que `SELECT DB_NAME()` vuelve a `Database()`.
- [ ] **Step 2: Ejecutar** `go test ./internal/migrator/...` → falla por símbolos.
- [ ] **Step 3: Implementar** lo anterior reutilizando el código de `Service.Connect` (no duplicar lógica de DSN).
- [ ] **Step 4: Ejecutar** `go test ./internal/migrator/... && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat: migrator expone Open, ExecBatch y IsLocalServer`.

---

### Task 3: `internal/importer.Service` (Plan y Run)

**Files:**
- Create: `internal/importer/service.go`, `internal/importer/ini.go`
- Test: `internal/importer/service_test.go`

**Interfaces:**
- Consumes: `sqlscript.ReadBatches/Batch/ErrNoStatements`, `migrator.Open/IsLocalServer/ConnectionParams/ApplyOptions/Event`, `silent.ReadConnection`.
- Produces:
  - `const EventName = "importer:event"`
  - `type Job struct { ID string; KairoPath string; SqlPath string }` (json camelCase: `id`, `kairoPath`, `sqlPath`)
  - `type PlannedJob struct { ID, Server, Database, Username, SqlPath string; SizeBytes int64; Error string }` (`Error` vacío = válido)
  - `type JobResult struct { ID string; IsDone bool; Batches int; Warnings []string; Error string }`
  - `type Event struct { JobID string; Kind string; BytesDone, BytesTotal int64; Message string }`; `Kind`: `"job-start"`, `"progress"`, `"warning"`, `"job-done"`, `"job-error"`.
  - `func NewService(baseDir, password string, emit func(Event)) *Service` (usuario fijo `const username = "kong"`; `password` es `silentPassword`)
  - `func (service *Service) Plan(jobs []Job) []PlannedJob`
  - `func (service *Service) Run(jobs []Job, options migrator.ApplyOptions) ([]JobResult, error)`: error solo si ya hay otra tanda en curso (`"hay una importación en curso"`) o la contraseña está vacía (`"esta compilación no incluye la contraseña de kong (falta KAIRO_SILENT_PASSWORD al compilar)"`).
  - `func (service *Service) IsRunning() bool`

Comportamiento:
- `KairoPath` puede ser la carpeta o el propio `config.ini`; si es carpeta se lee `<carpeta>\config.ini`. Errores en español y específicos: ini no encontrado, sin `[BaseDatos] Servidor` y `BaseDatos`, servidor remoto (`"el servidor %s no es local: solo se importa en el equipo propio"`), `.sql` inexistente.
- Clave de duplicado: host normalizado (`.`, `(local)`, `localhost`, `127.0.0.1` y el nombre del equipo → `local`) + instancia + BD, todo en minúsculas. En `Plan`, la segunda aparición lleva `Error` "misma base de datos que la fila N". `Run` vuelve a planificar y no ejecuta filas con `Error`; las marca como `JobResult.Error`.
- `Run` por trabajo, secuencial: `job-start` → `migrator.Open` con usuario `kong` y la contraseña del servicio → `runner.Clean(ctx, options)` → `ReadBatches` sobre el fichero con un lector que cuenta bytes leídos (`progress`, como mucho cada 250 ms y al final) → por cada lote no omitido `runner.ExecBatch`. Fallo en lote con `IsPrincipal` ⇒ `warning` y se acumula en `Warnings`. Otro fallo ⇒ `JobResult.Error` = `"lote N: <mensaje>"` + primeras 200 caracteres del lote; se corta ese trabajo, se cierra la conexión, se sigue con el siguiente. `ErrNoStatements` ⇒ error del trabajo. Registro en `<baseDir>\Logs\import.log` (una línea por evento, formato del log de migrator).
- `closeAll` siempre se llama al terminar el trabajo (también en error).

- [ ] **Step 1: Tests de `Plan`** (sin SQL Server; ficheros en `t.TempDir()`):
  - `TestPlanReadsServerDatabaseAndAlwaysKong`: ini con `Servidor=localhost\SQLEXPRESS`, `BaseDatos=Tecnicos`, `Usuario=otro` ⇒ `Server` y `Database` leídos y `Username == \"kong\"` (el `Usuario` del ini se ignora).
  - `TestPlanAcceptsConfigIniPathAndFolder`.
  - `TestPlanRejectsRemoteServer`: `Servidor=10.0.0.5\SQL` ⇒ `Error` contiene `no es local`.
  - `TestPlanRejectsMissingIniAndMissingSection` y `TestPlanRejectsMissingSqlFile`.
  - `TestPlanRejectsDuplicateDatabase`: `.\SQLEXPRESS`/`TECNICOS` y `localhost\SQLEXPRESS`/`tecnicos` ⇒ la segunda tiene `Error`, la primera no.
  - `TestRunRefusesEmptyPassword`.
- [ ] **Step 2: Test de integración** `TestRunAgainstRealServer`, gating por `IMPORT_TEST_SERVER`, `IMPORT_TEST_DATABASE`, `IMPORT_TEST_SQL`, `IMPORT_TEST_PASSWORD` (mismo estilo que `silent/integration_test.go`; BD desechable): importa el `.sql` y comprueba `SELECT COUNT(*) FROM sys.tables` ≥ 364 y `JobResult.IsDone`.
- [ ] **Step 3: Ejecutar** `go test ./internal/importer/... ./internal/silent/...` → falla por símbolos.
- [ ] **Step 4: Implementar** `ini.go` (resolver ruta y leer conexión vía `silent.ReadConnection`) y `service.go`. Guardar un mutex y `isRunning` como en `migrator.Service`.
- [ ] **Step 5: Ejecutar** `go test ./... && go vet ./... && gofmt -l .` → PASS y sin salida de gofmt.
- [ ] **Step 6: Commit** `feat: servicio de importación de .sql (Plan y Run)`.

---

### Task 4: Cableado en `main.go`

**Files:**
- Modify: `main.go`, `AGENTS.md`

**Interfaces:**
- Consumes: `importer.NewService`, `importer.EventName`, `importer.Event`, `silentPassword` (ya existe en `main.go`).

- [ ] **Step 1: Cablear** en `main.go`: `application.RegisterEvent[importer.Event](importer.EventName)` en `init()`; `importService := importer.NewService(baseDir, silentPassword, func(e importer.Event){ if app != nil { app.Event.Emit(importer.EventName, e) } })`; añadirlo a `Services`. `confirmCloseWhileUpdating` debe preguntar también si `importService.IsRunning()` (mismo diálogo, texto adaptado: cerrar interrumpe la importación y deja la BD a medias); pasar una función `isBusy func() bool`.
- [ ] **Step 2: AGENTS.md**: añadir sección "Importer" (qué hace, solo servidor local, sin backup, login `kong` con la contraseña de `silentPassword`, eventos, `Logs\import.log`) y las líneas del layout para `internal/importer` y `internal/sqlscript`.
- [ ] **Step 3: Verificar** `go build ./... && go vet ./...` y `wails3 generate bindings -d frontend/bindings -ts -clean=true -time-type=Date`; comprobar que existen bindings de `importer`. Esperado: sin errores.
- [ ] **Step 4: Commit** `feat: registrar el servicio de importacion en main`.

---

### Task 5: Vista `/import`

**Files:**
- Create: `frontend/src/views/ImportView.vue`, `frontend/src/stores/importer.ts`
- Modify: `frontend/src/main.ts` (ruta `/import`), `frontend/src/components/AppHeader.vue` (enlace a `/import` e `/input`), `internal/userconfig/service.go` (campo `KairoPaths []string` con tag `kairoPaths`, por defecto vacío; las últimas carpetas de Kairo usadas, máximo 10, sin duplicados) y `frontend/src/stores/userConfig.ts` (ref y líneas en `init()`/`save()` como indica `AGENTS.md`).
- Test: `internal/userconfig/service_test.go` (campo nuevo se guarda y carga; fichero antiguo sin el campo carga con lista vacía).

**Interfaces:**
- Consumes: bindings generados de `importer.Service` (`Plan`, `Run`, `IsRunning`) y el evento `importer:event`.
- Produces: store `useImporterStore` con `jobs` (`{ id, kairoPath, sqlPath, planned?, state: "pending" | "running" | "done" | "error", percent, warnings, error }`), `addJobs(sqlPaths: string[])`, `removeJob(id)`, `plan()`, `run(options)`, `isBusy`, `canRun`.

- [ ] **Step 1: Test Go** del campo nuevo en `userconfig` → falla → implementar → PASS (`go test ./internal/userconfig/...`).
- [ ] **Step 2: Store** `importer.ts` (estilo setup store con secciones `// State`, `// Private`, `// Actions`, `// Side effects`): escucha `importer:event` y actualiza la fila por `jobId`. Pulsar "Importar" llama primero a `plan()`; si alguna fila tiene `error`, se muestra en la fila y no se lanza nada.
- [ ] **Step 3: Vista** con `UTable`/lista de filas: selector de carpeta de Kairo (con las últimas guardadas como sugerencias), selector de `.sql`, servidor/BD planificados bajo la fila, `UProgress` por fila, aviso y error expandibles, botón quitar, botón "Añadir fila", botón "Importar" deshabilitado sin filas completas o con tanda en curso. Soltar varios `.sql` en la ventana crea una fila por fichero (soporte de arrastrar de Wails v3; si el arrastre no está disponible en la versión instalada, dejar solo el selector múltiple y decirlo en el informe). Un único `UModal` de confirmación lista `servidor / BD` de cada fila y avisa en rojo: "Se borrará TODO el contenido de estas bases de datos antes de importar". Si el servicio informa de sesiones abiertas, el error de la fila lo dice (no se ofrece echarlas en esta versión).
- [ ] **Step 4: Ruta e interfaz**: registrar `/import` en `main.ts`; enlace en la cabecera con iconos `i-lucide-*` literales.
- [ ] **Step 5: Verificar** en `frontend/`: `npx vue-tsc -b` y `npm run format:check` (si falla el formato, `npm run format`). Esperado: sin errores.
- [ ] **Step 6: Commit** `feat: pantalla de importación de .sql con varias filas`.

---

### Task 6: Verificación manual con el script real

**Files:** ninguno (solo verificación).

- [ ] **Step 1:** Crear una BD vacía de pruebas en el SQL Server local y una carpeta con un `config.ini` apuntando a ella (`[BaseDatos] Servidor` y `BaseDatos`). Compilar con `KAIRO_SILENT_PASSWORD` definida (`powershell -File build/windows/silent-password.ps1`) y `wails3 task build`.
- [ ] **Step 2:** Importar `C:\Users\MIGUEL\Desktop\Tecnicos\script.sql` en una fila: esperado, 364 tablas, las filas de `Articulos` presentes, avisos solo por `CREATE USER`/`ALTER ROLE` de logins inexistentes, y `Logs\import.log` con el resumen.
- [ ] **Step 3:** Dos filas con BDs distintas: ambas terminan. Dos filas con la misma BD: la segunda se rechaza antes de empezar. Fila con servidor remoto: rechazada. Segunda importación sobre la misma BD: se limpia y queda igual.
- [ ] **Step 4:** Ejecutar `go test ./... && go vet ./... && gofmt -l .` y en `frontend/` `npx vue-tsc -b && npm run format:check`. Informar de lo que pasó y de lo que no se pudo comprobar (sin SQL Server o sin la contraseña no hay prueba manual: decirlo).
