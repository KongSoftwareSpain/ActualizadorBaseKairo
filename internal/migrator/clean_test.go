package migrator

import (
	"context"
	"strings"
	"testing"
)

const objectsOutsideDbo = `
CREATE SCHEMA ventas;
GO
CREATE TABLE ventas.Pedidos (Id int PRIMARY KEY, ClienteId int NOT NULL REFERENCES dbo.Clientes (Id));
GO
CREATE TABLE dbo.Hijos (Id int PRIMARY KEY, PadreId int NULL);
ALTER TABLE dbo.Hijos ADD CONSTRAINT FK_Hijos_Hijos FOREIGN KEY (PadreId) REFERENCES dbo.Hijos (Id);
GO
CREATE FUNCTION dbo.Doble (@n int) RETURNS int WITH SCHEMABINDING AS BEGIN RETURN @n * 2 END;
GO
CREATE VIEW ventas.VistaPedidos WITH SCHEMABINDING AS SELECT Id, ClienteId FROM ventas.Pedidos;
GO
CREATE TABLE dbo.ConCalculada (Id int PRIMARY KEY, Valor int, Doble AS dbo.Doble(Valor) PERSISTED);
GO
CREATE TYPE dbo.Codigo FROM varchar(10);
GO
CREATE SEQUENCE dbo.Numeros AS int;
GO
CREATE SYNONYM dbo.SinonimoClientes FOR dbo.Clientes;
GO
CREATE PROCEDURE dbo.ListarClientes AS SELECT * FROM dbo.Clientes;
`

func userObjectCount(t *testing.T, runner *Runner) int {
	t.Helper()

	var count int
	err := runner.conn.QueryRowContext(context.Background(), `
		SELECT (SELECT COUNT(*) FROM sys.objects WHERE is_ms_shipped = 0 AND parent_object_id = 0)
		     + (SELECT COUNT(*) FROM sys.types WHERE is_user_defined = 1)
		     + (SELECT COUNT(*) FROM sys.schemas WHERE schema_id > 4 AND schema_id < 16384)`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	return count
}

func TestCleanDropsEverythingAndAllowsRebuild(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))
	writeScript(t, dir, "V0002__otros.sql", []byte(objectsOutsideDbo))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	if userObjectCount(t, runner) == 0 {
		t.Fatal("el script debería haber creado objetos")
	}

	if err := runner.setRollbackBackup(ctx, `C:\viejo.bak`); err != nil {
		t.Fatal(err)
	}

	if err := runner.Clean(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	if count := userObjectCount(t, runner); count != 0 {
		t.Fatalf("quedan %d objetos", count)
	}

	if previous, err := runner.rollbackBackup(ctx); err != nil || previous != "" {
		t.Fatalf("la marca de backup debe borrarse: %q, %v", previous, err)
	}

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if status.CurrentVersion != 0 || len(status.Pending) != 2 || status.NeedsBaseline {
		t.Fatalf("tras limpiar todo debe estar pendiente: %+v", status)
	}

	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatalf("la BD limpia debe poder reconstruirse: %v", err)
	}

	if !tableExists(t, runner, "dbo.Clientes") {
		t.Fatal("la reconstrucción debe crear dbo.Clientes")
	}
}

func TestCleanRefusesWhileOtherSessionsUnlessAllowed(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !status.CanSeeSessions {
		t.Skip("el login de prueba no tiene VIEW SERVER STATE")
	}

	openOtherSession(t, runner)

	if err := runner.Clean(ctx, ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "sesión") {
		t.Fatalf("se esperaba rechazo por sesiones: %v", err)
	}

	if !tableExists(t, runner, "dbo.Clientes") {
		t.Fatal("no debe borrarse nada al rechazar")
	}

	if err := runner.Clean(ctx, ApplyOptions{KickSessions: true}); err != nil {
		t.Fatal(err)
	}

	if tableExists(t, runner, "dbo.Clientes") {
		t.Fatal("con confirmación debe limpiarse")
	}

	var access int
	if err := runner.conn.QueryRowContext(ctx, `SELECT user_access FROM sys.databases WHERE database_id = DB_ID()`).Scan(&access); err != nil {
		t.Fatal(err)
	}

	if access != 0 {
		t.Fatalf("la BD debe volver a MULTI_USER, user_access=%d", access)
	}
}
