# Importar .sql en BD local (diseño)

Fecha: 2026-11-24. Estado: aprobado por el usuario.

## Intención y porqué

Atención al cliente (no técnicos) se trae la BD de un cliente como `.sql` generado con "Generate Scripts" de SSMS (esquema y datos, p. ej. `C:\Users\MIGUEL\Desktop\Tecnicos\script.sql`: 45 MB, UTF-16, 5.645 `GO`, 364 tablas, ~28.000 `INSERT`). No saben ejecutarlo en su SQL Server local. Antes, con Access, era trivial. Quieren soltar el `.sql`, indicar la carpeta de Kairo y que se importe en la BD del `config.ini` con la contraseña fija de Kairo.

## Objetivos

- Pantalla `/import` en el Actualizador Go (Wails): lista de trabajos, cada uno = carpeta de Kairo + `.sql`.
- Importar varios trabajos en una tanda, uno detrás de otro.
- Cero conocimientos técnicos: leer servidor y BD del `config.ini`, nada que teclear.

## No objetivos

Servidores remotos, restaurar `.bak`, backup previo, ejecución en paralelo, editar el `.sql`.

## Antiobjetivos

Borrar por error la BD de un cliente (servidor remoto); filtrar la contraseña a git; que un trabajo fallido pare los demás.

## Restricciones

- Windows solo. Convenciones de `AGENTS.md` (booleanos con verbo, `json` camelCase, Prettier/gofmt).
- Usuario siempre `kong` y contraseña del login `kong` (decisión posterior del usuario; se ignora `Usuario` del ini). Es el login del modo `--silent`: se reutiliza `silentPassword`, inyectada al compilar con `KAIRO_SILENT_PASSWORD`; no va en git. Servidor y BD: `[BaseDatos] Servidor` y `BaseDatos` (`internal/silent.ReadConnection`).
- El `.sql` puede ser UTF-16/UTF-8 con BOM; hay que leerlo en flujo (45 MB), no entero si se evita.

## Enfoques considerados

1. **Modo dentro del actualizador (elegido por el usuario).** Reutiliza ini, conexión, `Clean`, sesiones.
2. Ejecutable aparte en el mismo repositorio: más simple de usar, pero un `.exe` más. Descartado por el usuario.
3. Dentro de Kairo VB6: lento y frágil con 5.645 lotes. Descartado.

## Diseño

- Servicio Go nuevo `importer.Service` (paquete `internal/importer`), registrado en `main.go`. Emite eventos `importer:event` (progreso por trabajo).
- `Job { id, kairoPath, sqlPath }`. `Plan(jobs)` valida cada trabajo sin tocar nada: lee el ini, comprueba servidor local (`isLocalServer`), que el `.sql` existe y es legible, y rechaza dos trabajos con mismo servidor+BD. Devuelve servidor/BD por trabajo para el modal.
- `Run(jobs)`: por trabajo, en secuencia y una conexión por trabajo:
  1. Conectar con usuario del ini + contraseña inyectada.
  2. `Clean` (reutilizar el de `migrator`; refuerza la política de sesiones: si hay otras sesiones, error del trabajo salvo que el usuario confirme echarlas, igual que `Apply`).
  3. Leer el `.sql`, partir por líneas que sean solo `GO`.
  4. Descartar: `USE [..]`, `CREATE DATABASE`, `ALTER DATABASE ... SET` (incluye `COMPATIBILITY_LEVEL`, `QUERY_STORE`, `FILESTREAM`), y el `USE [master]` final. Todo lo demás se ejecuta contra la BD del ini.
  5. Ejecutar lote a lote (autocommit). Fallo de `CREATE USER` / `ALTER ROLE` / `ALTER AUTHORIZATION` por login inexistente: aviso en el registro y seguir. Cualquier otro error: parar el trabajo, informar número de lote y mensaje, y pasar al siguiente trabajo.
  6. Eventos de progreso: lote N de M, estado del trabajo.
- Resumen final por trabajo (hecho / error / avisos). Registro en `Logs\import.log`.
- Frontend: vista `ImportView.vue` y ruta `/import`, enlace desde la cabecera. Filas con selector de carpeta Kairo y de `.sql`, estado, barra de progreso, quitar fila. Arrastrar varios `.sql` crea una fila por fichero. Modal de confirmación único que lista servidor y BD de cada fila y avisa del borrado total.
- Guardar en `userconfig` las últimas carpetas de Kairo usadas (no secretos).

## Detalles que decide el constructor

Forma exacta del parser de lotes (cadenas con `GO` dentro; tamaño de lectura), formato del registro, nombres de funciones, textos de UI, cómo se reutiliza `Clean` (extraer si hace falta de `migrator.Service` a algo invocable con una conexión propia).

## Verificación

Tests Go del parser (UTF-16/UTF-8, descarte de cabecera, `GO` con espacios) y del rechazo de remoto y duplicados. Prueba manual con `script.sql` real contra SQL Server local. `go vet`, `gofmt -l .`, `npx vue-tsc -b`, `npm run format:check`.
