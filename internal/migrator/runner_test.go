package migrator

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"
)

// Integration tests need a local SQL Server. Override the default with
// MIGRATOR_TEST_SERVER (for example "localhost\SQLEXPRESS").
func testServer() string {
	if server := os.Getenv("MIGRATOR_TEST_SERVER"); server != "" {
		return server
	}

	return `localhost\SQLEXPRESS`
}

func openDB(t *testing.T, database string) *sql.DB {
	t.Helper()

	query := url.Values{}
	query.Set("database", database)
	query.Set("trusted_connection", "yes")
	query.Set("TrustServerCertificate", "true")

	host, instance, _ := strings.Cut(testServer(), `\`)
	dsnURL := url.URL{Scheme: "sqlserver", Host: host, RawQuery: query.Encode()}
	if instance != "" {
		dsnURL.Path = "/" + instance
	}

	db, err := sql.Open("sqlserver", dsnURL.String())
	if err != nil {
		t.Fatal(err)
	}

	return db
}

// testBackupDir returns a folder the SQL Server service can write to: a
// dedicated subfolder of the instance default backup path. Created through
// the server (xp_create_subdir), since the test user may lack write access.
func testBackupDir(t *testing.T, master *sql.DB) string {
	t.Helper()

	ctx := context.Background()

	var root sql.NullString
	err := master.QueryRowContext(ctx,
		`SELECT CAST(SERVERPROPERTY('InstanceDefaultBackupPath') AS nvarchar(512))`).Scan(&root)
	if err != nil || !root.Valid {
		t.Skipf("no se pudo obtener la carpeta de backups del servidor: %v", err)
	}

	dir := filepath.Join(root.String, "MigradorTests")

	if _, err := master.ExecContext(ctx, `EXEC master.dbo.xp_create_subdir @p1`, dir); err != nil {
		t.Skipf("no se pudo crear %s: %v", dir, err)
	}

	t.Cleanup(func() {
		// Only the dedicated test folder is cleaned, never the real backups.
		cutoff := time.Now().Add(24 * time.Hour).Format("2006-01-02T15:04:05")
		query := fmt.Sprintf(`EXEC master.dbo.xp_delete_file 0, @p1, N'bak', N'%s', 0`, cutoff)

		cleaner := openDB(t, "master")
		defer cleaner.Close()

		if _, err := cleaner.ExecContext(ctx, query, dir); err != nil {
			t.Logf("no se pudieron borrar los .bak de prueba en %s: %v", dir, err)
		}
	})

	return dir
}

// newTestRunner creates a fresh database, and drops it when the test ends.
func newTestRunner(t *testing.T, scriptsDir string, events *[]Event) *Runner {
	t.Helper()

	ctx := context.Background()
	name := fmt.Sprintf("MigradorTest_%d", time.Now().UnixNano())

	master := openDB(t, "master")
	if err := master.PingContext(ctx); err != nil {
		master.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}

	backupDir := testBackupDir(t, master)

	if _, err := master.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		master.Close()
		t.Fatal(err)
	}

	db := openDB(t, name)
	t.Cleanup(func() {
		db.Close()
		master.ExecContext(ctx, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.ExecContext(ctx, "DROP DATABASE ["+name+"]")
		master.ExecContext(ctx, "EXEC msdb.dbo.sp_delete_database_backuphistory @p1", name)
		master.Close()
	})

	scripts, err := LoadScripts(scriptsDir)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(conn, scripts, backupDir, func(event Event) {
		if events != nil {
			*events = append(*events, event)
		}
	})
	t.Cleanup(func() { runner.Close() })

	return runner
}

func execSQL(t *testing.T, runner *Runner, statement string) {
	t.Helper()

	if _, err := runner.conn.ExecContext(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, runner *Runner, table string) int {
	t.Helper()

	var count int
	if err := runner.conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}

	return count
}

func tableExists(t *testing.T, runner *Runner, table string) bool {
	t.Helper()

	var id sql.NullInt64
	if err := runner.conn.QueryRowContext(context.Background(), "SELECT OBJECT_ID(@p1, N'U')", table).Scan(&id); err != nil {
		t.Fatal(err)
	}

	return id.Valid
}

// backupSetCount counts the backup sets inside a .bak file, asking the server.
func backupSetCount(t *testing.T, runner *Runner, path string) int {
	t.Helper()

	rows, err := runner.conn.QueryContext(context.Background(), "RESTORE HEADERONLY FROM DISK = @p1", path)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return count
}

func eventsOfKind(events []Event, kind string) []Event {
	var found []Event
	for _, event := range events {
		if event.Kind == kind {
			found = append(found, event)
		}
	}

	return found
}

const createClientes = `SET XACT_ABORT ON
GO
CREATE TABLE dbo.Clientes (Id int NOT NULL PRIMARY KEY, Nombre nvarchar(50) NOT NULL)
GO
INSERT INTO dbo.Clientes VALUES (1, N'Peña')
`

const createPedidos = `CREATE TABLE dbo.Pedidos (Id int NOT NULL PRIMARY KEY)
GO
CREATE VIEW dbo.VPedidos AS SELECT Id FROM dbo.Pedidos
`

func TestApplyEmptyDatabaseThenIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))
	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	if rows := countRows(t, runner, "dbo.SchemaVersions"); rows != 2 {
		t.Fatalf("filas en SchemaVersions: %d, esperado 2", rows)
	}

	var events []Event
	runner.emit = func(event Event) { events = append(events, event) }

	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	if started := eventsOfKind(events, "script-start"); len(started) != 0 {
		t.Fatalf("la segunda ejecución aplicó %s", started[0].Message)
	}
}

func TestApplyRollsBackFailingScript(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))
	writeScript(t, dir, "V0002__roto.sql", []byte(
		"CREATE TABLE dbo.Temporal (Id int)\nGO\nINSERT INTO dbo.NoExiste VALUES (1)\n"))
	writeScript(t, dir, "V0003__pedidos.sql", []byte(createPedidos))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	backupPath, err := runner.Apply(ctx)
	if err == nil {
		t.Fatal("se esperaba error")
	}

	if want := "V0002__roto.sql"; !strings.Contains(err.Error(), want) {
		t.Fatalf("el error no indica el fichero %s: %v", want, err)
	}

	if backupPath == "" || !strings.Contains(err.Error(), backupPath) {
		t.Fatalf("el error debe incluir la ruta del backup %q: %v", backupPath, err)
	}

	if rows := countRows(t, runner, "dbo.SchemaVersions"); rows != 1 {
		t.Fatalf("filas en SchemaVersions: %d, esperado 1", rows)
	}

	if tableExists(t, runner, "dbo.Temporal") {
		t.Fatal("el rollback no deshizo el CREATE TABLE del script roto")
	}

	if tableExists(t, runner, "dbo.Pedidos") {
		t.Fatal("se aplicó un script posterior al fallo")
	}
}

func TestApplyOnlyNewScript(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))

	scripts, err := LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}
	runner.scripts = scripts

	var events []Event
	runner.emit = func(event Event) { events = append(events, event) }

	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	started := eventsOfKind(events, "script-start")
	if len(started) != 1 || started[0].Version != 2 {
		t.Fatalf("scripts aplicados: %+v, esperado solo V2", started)
	}
}

func TestApplyKeepsAccents(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := runner.conn.QueryRowContext(ctx, "SELECT Nombre FROM dbo.Clientes WHERE Id = 1").Scan(&name); err != nil {
		t.Fatal(err)
	}

	if name != "Peña" {
		t.Fatalf("nombre: %q", name)
	}
}

func TestStatusRejectsNewerDatabase(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if err := runner.EnsureVersionsTable(ctx); err != nil {
		t.Fatal(err)
	}

	execSQL(t, runner, "INSERT INTO dbo.SchemaVersions (Version, ScriptName) VALUES (7, N'V0007__futuro.sql')")

	if _, err := runner.Status(ctx); err == nil {
		t.Fatal("se esperaba error por BD más nueva")
	}
}

func TestApplyKeepsSingleBackup(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	var events []Event
	runner := newTestRunner(t, dir, &events)

	firstPath, err := runner.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(firstPath, "_preupdate.bak") {
		t.Fatalf("nombre de backup inesperado: %q", firstPath)
	}

	messages := eventsOfKind(events, "backup")
	if len(messages) != 1 || !strings.Contains(messages[0].Message, firstPath) ||
		!strings.Contains(messages[0].Message, "BD sin versiones aplicadas") {
		t.Fatalf("mensaje final inesperado: %+v", messages)
	}

	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))
	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	events = nil

	secondPath, err := runner.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if secondPath != firstPath {
		t.Fatalf("el segundo backup usó otra ruta: %q y %q", firstPath, secondPath)
	}

	if sets := backupSetCount(t, runner, secondPath); sets != 1 {
		t.Fatalf("el .bak tiene %d backups, esperado 1 (WITH INIT)", sets)
	}

	messages = eventsOfKind(events, "backup")
	if len(messages) != 1 || !strings.Contains(messages[0].Message, "BD en V0001 antes de actualizar") {
		t.Fatalf("mensaje final inesperado: %+v", messages)
	}
}

func TestApplyWithoutPendingSkipsBackup(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	backupPath, err := runner.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if backupPath != "" {
		t.Fatalf("sin pendientes no debe haber backup, got %q", backupPath)
	}
}

func TestBackupFailureStopsBeforeMigrating(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	// A path below a regular file can never be created as a folder.
	blocker := filepath.Join(t.TempDir(), "fichero")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := newTestRunner(t, dir, nil)
	runner.backupDir = filepath.Join(blocker, "Backups")

	if _, err := runner.Apply(context.Background()); err == nil {
		t.Fatal("se esperaba error de backup")
	}

	if tableExists(t, runner, "dbo.Clientes") || tableExists(t, runner, "dbo.SchemaVersions") {
		t.Fatal("se migró aunque el backup falló")
	}
}

func TestBaselineRegisteredWithoutRunning(t *testing.T) {
	dir := t.TempDir()
	// V0001 would fail if executed: dbo.Clientes already exists.
	writeScript(t, dir, "V0001__baseline.sql", []byte(createClientes))
	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	execSQL(t, runner, "CREATE TABLE dbo.Clientes (Id int NOT NULL PRIMARY KEY, Nombre nvarchar(50) NOT NULL)")

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !status.NeedsBaseline {
		t.Fatal("se esperaba NeedsBaseline")
	}

	if _, err := runner.Apply(ctx); err == nil {
		t.Fatal("Apply debe negarse hasta registrar la versión base")
	}

	if err := runner.RegisterBaseline(ctx); err != nil {
		t.Fatal(err)
	}

	var events []Event
	runner.emit = func(event Event) { events = append(events, event) }

	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	started := eventsOfKind(events, "script-start")
	if len(started) != 1 || started[0].Version != 2 {
		t.Fatalf("scripts ejecutados: %+v, esperado solo V2", started)
	}

	if rows := countRows(t, runner, "dbo.SchemaVersions"); rows != 2 {
		t.Fatalf("filas en SchemaVersions: %d, esperado 2", rows)
	}

	if rows := countRows(t, runner, "dbo.Clientes"); rows != 0 {
		t.Fatal("la baseline se ejecutó en lugar de registrarse")
	}
}

func TestEmptyDatabaseDoesNotNeedBaseline(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	runner := newTestRunner(t, dir, nil)

	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if status.NeedsBaseline {
		t.Fatal("una BD vacía no necesita versión base")
	}

	if err := runner.RegisterBaseline(context.Background()); err == nil {
		t.Fatal("RegisterBaseline debe fallar en una BD vacía")
	}
}
