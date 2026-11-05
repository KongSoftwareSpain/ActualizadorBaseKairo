package migrator

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
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
	dsn := dsnURL.String()

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatal(err)
	}

	return db
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

	if _, err := master.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		master.Close()
		t.Fatal(err)
	}

	db := openDB(t, name)
	t.Cleanup(func() {
		db.Close()
		master.ExecContext(ctx, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.ExecContext(ctx, "DROP DATABASE ["+name+"]")
		master.Close()
	})

	return newRunnerFor(t, db, scriptsDir, events)
}

func newRunnerFor(t *testing.T, db *sql.DB, scriptsDir string, events *[]Event) *Runner {
	t.Helper()

	scripts, err := LoadScripts(scriptsDir)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(conn, scripts, func(event Event) {
		if events != nil {
			*events = append(*events, event)
		}
	})
	t.Cleanup(func() { runner.Close() })

	if err := runner.EnsureVersionsTable(context.Background()); err != nil {
		t.Fatal(err)
	}

	return runner
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

	if err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	if rows := countRows(t, runner, "dbo.SchemaVersions"); rows != 2 {
		t.Fatalf("filas en SchemaVersions: %d, esperado 2", rows)
	}

	var events []Event
	runner.emit = func(event Event) { events = append(events, event) }

	if err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	for _, event := range events {
		if event.Kind == "script-start" {
			t.Fatalf("la segunda ejecución aplicó %s", event.Message)
		}
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

	err := runner.Apply(ctx)
	if err == nil {
		t.Fatal("se esperaba error")
	}

	if want := "V0002__roto.sql"; !strings.Contains(err.Error(), want) {
		t.Fatalf("el error no indica el fichero %s: %v", want, err)
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

	if err := runner.Apply(ctx); err != nil {
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

	if err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	var started []int
	for _, event := range events {
		if event.Kind == "script-start" {
			started = append(started, event.Version)
		}
	}

	if len(started) != 1 || started[0] != 2 {
		t.Fatalf("scripts aplicados: %v, esperado [2]", started)
	}
}

func TestApplyKeepsAccents(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if err := runner.Apply(ctx); err != nil {
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

	_, err := runner.conn.ExecContext(ctx,
		"INSERT INTO dbo.SchemaVersions (Version, ScriptName) VALUES (7, N'V0007__futuro.sql')")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Status(ctx); err == nil {
		t.Fatal("se esperaba error por BD más nueva")
	}
}
