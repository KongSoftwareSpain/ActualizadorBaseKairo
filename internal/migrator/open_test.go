package migrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOpenRejectsMissingServer(t *testing.T) {
	runner, closeAll, err := Open(ConnectionParams{Database: "x"}, nil)

	if err == nil || !strings.Contains(err.Error(), "falta el servidor") {
		t.Fatalf("err = %v", err)
	}

	if runner != nil || closeAll != nil {
		t.Fatal("en error no debe devolver runner ni closeAll")
	}
}

// Windows authentication (no user, no password) against the local test server.
func TestOpenAndExecBatchRepinsDatabase(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("MigradorOpen_%d", time.Now().UnixNano())

	master := openDB(t, "master")
	if err := master.PingContext(ctx); err != nil {
		master.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}

	if _, err := master.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		master.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		master.ExecContext(ctx, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.ExecContext(ctx, "DROP DATABASE ["+name+"]")
		master.Close()
	})

	runner, closeAll, err := Open(ConnectionParams{Server: testServer(), Database: name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()

	if runner.Database() != name {
		t.Fatalf("Database() = %q, esperado %q", runner.Database(), name)
	}

	if err := runner.ExecBatch(ctx, "CREATE TABLE dbo.T (A int)"); err != nil {
		t.Fatal(err)
	}

	if err := runner.ExecBatch(ctx, "USE master"); err != nil {
		t.Fatal(err)
	}

	var current string
	if err := runner.conn.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&current); err != nil {
		t.Fatal(err)
	}

	if current != name {
		t.Fatalf("la sesión se quedó en %q, esperado %q", current, name)
	}

	if err := runner.ExecBatch(ctx, "SELECT * FROM dbo.NoExiste"); err == nil {
		t.Fatal("un lote erróneo debe devolver error")
	}
}

func TestEnsureDatabaseCreatesMissingAndKeepsExisting(t *testing.T) {
	ctx := context.Background()
	// The closing bracket in the name checks the quoting.
	name := fmt.Sprintf("MigradorEnsure]_%d", time.Now().UnixNano())

	master := openDB(t, "master")
	if err := master.PingContext(ctx); err != nil {
		master.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}

	t.Cleanup(func() {
		quoted := quoteName(name)
		master.ExecContext(ctx, "ALTER DATABASE "+quoted+" SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.ExecContext(ctx, "DROP DATABASE "+quoted)
		master.Close()
	})

	params := ConnectionParams{Server: testServer(), Database: name}

	exists, err := DatabaseExists(params)
	if err != nil || exists {
		t.Fatalf("antes de crear: exists=%v err=%v", exists, err)
	}

	isCreated, err := EnsureDatabase(params)
	if err != nil || !isCreated {
		t.Fatalf("primera llamada: isCreated=%v err=%v", isCreated, err)
	}

	exists, err = DatabaseExists(params)
	if err != nil || !exists {
		t.Fatalf("después de crear: exists=%v err=%v", exists, err)
	}

	isCreated, err = EnsureDatabase(params)
	if err != nil || isCreated {
		t.Fatalf("segunda llamada no debe crear: isCreated=%v err=%v", isCreated, err)
	}
}

func TestEnsureDatabaseRejectsMissingServer(t *testing.T) {
	if _, err := EnsureDatabase(ConnectionParams{Database: "x"}); err == nil || !strings.Contains(err.Error(), "falta el servidor") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureOwnAccessCreatesUserWhenLoginIsNotOwner(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("MigradorAccess_%d", time.Now().UnixNano())

	master := openDB(t, "master")
	if err := master.PingContext(ctx); err != nil {
		master.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}

	if _, err := master.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		master.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		master.ExecContext(ctx, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.ExecContext(ctx, "DROP DATABASE ["+name+"]")
		master.Close()
	})

	// The test login created the database, so it is dbo already: nothing to do.
	runner, closeAll, err := Open(ConnectionParams{Server: testServer(), Database: name}, nil)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := runner.EnsureOwnAccess(ctx)
	if err != nil || changed {
		t.Fatalf("dueño de la BD: changed=%v err=%v", changed, err)
	}

	closeAll()

	// Another owner: the login is only a sysadmin passing through, with no user of its own.
	if _, err := master.ExecContext(ctx, "ALTER AUTHORIZATION ON DATABASE::["+name+"] TO sa"); err != nil {
		t.Fatal(err)
	}

	runner, closeAll, err = Open(ConnectionParams{Server: testServer(), Database: name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()

	changed, err = runner.EnsureOwnAccess(ctx)
	if err != nil || !changed {
		t.Fatalf("otro dueño: changed=%v err=%v", changed, err)
	}

	var isOwner int
	err = runner.conn.QueryRowContext(ctx,
		`SELECT IS_ROLEMEMBER(N'db_owner', (SELECT name FROM sys.database_principals WHERE sid = SUSER_SID()))`).Scan(&isOwner)
	if err != nil || isOwner != 1 {
		t.Fatalf("el login debe tener usuario en la BD y ser db_owner: isOwner=%d err=%v", isOwner, err)
	}

	changed, err = runner.EnsureOwnAccess(ctx)
	if err != nil || changed {
		t.Fatalf("segunda llamada no debe cambiar nada: changed=%v err=%v", changed, err)
	}
}
