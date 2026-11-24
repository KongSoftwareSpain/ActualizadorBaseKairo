package migrator

import (
	"context"
	"database/sql"
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

func TestEnsureAccessCreatesUserWhenLoginIsNotOwner(t *testing.T) {
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

	changed, err := runner.EnsureAccess(ctx, "")
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

	changed, err = runner.EnsureAccess(ctx, "")
	if err != nil || !changed {
		t.Fatalf("otro dueño: changed=%v err=%v", changed, err)
	}

	var isOwner int
	err = runner.conn.QueryRowContext(ctx,
		`SELECT IS_ROLEMEMBER(N'db_owner', (SELECT name FROM sys.database_principals WHERE sid = SUSER_SID()))`).Scan(&isOwner)
	if err != nil || isOwner != 1 {
		t.Fatalf("el login debe tener usuario en la BD y ser db_owner: isOwner=%d err=%v", isOwner, err)
	}

	changed, err = runner.EnsureAccess(ctx, "")
	if err != nil || changed {
		t.Fatalf("segunda llamada no debe cambiar nada: changed=%v err=%v", changed, err)
	}
}

func dropLoginOnCleanup(t *testing.T, master *sql.DB, login string) {
	t.Helper()

	t.Cleanup(func() { master.ExecContext(context.Background(), "DROP LOGIN "+quoteName(login)) })
}

func loginExists(t *testing.T, master *sql.DB, login string) bool {
	t.Helper()

	var count int
	if err := master.QueryRow(`SELECT COUNT(*) FROM sys.server_principals WHERE name = @p1`, login).Scan(&count); err != nil {
		t.Fatal(err)
	}

	return count > 0
}

func TestEnsureLoginCreatesMissingLoginAndKeepsExisting(t *testing.T) {
	master := openDB(t, "master")
	if err := master.PingContext(context.Background()); err != nil {
		master.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}
	defer master.Close()

	// Quote, backslash and braces in the password check the escaping.
	const password = `a'b\c}%x:`
	login := fmt.Sprintf("login]prueba_%d", time.Now().UnixNano())
	dropLoginOnCleanup(t, master, login)

	params := ConnectionParams{Server: testServer(), Database: "master"}

	isCreated, err := EnsureLogin(params, login, password)
	if err != nil || !isCreated || !loginExists(t, master, login) {
		t.Fatalf("primera llamada: isCreated=%v err=%v", isCreated, err)
	}

	isCreated, err = EnsureLogin(params, login, "")
	if err != nil || isCreated {
		t.Fatalf("un login que ya existe no se toca ni pide contraseña: isCreated=%v err=%v", isCreated, err)
	}
}

func TestEnsureLoginNeedsPasswordWhenMissing(t *testing.T) {
	master := openDB(t, "master")
	if err := master.PingContext(context.Background()); err != nil {
		master.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}
	defer master.Close()

	login := fmt.Sprintf("login_sin_clave_%d", time.Now().UnixNano())

	_, err := EnsureLogin(ConnectionParams{Server: testServer(), Database: "master"}, login, "")
	if err == nil || !strings.Contains(err.Error(), "contraseña") || loginExists(t, master, login) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureAccessForAnotherLogin(t *testing.T) {
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("MigradorAccess2_%d", stamp)
	login := fmt.Sprintf("app_prueba_%d", stamp)

	master := openDB(t, "master")
	if err := master.PingContext(ctx); err != nil {
		master.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}

	if _, err := master.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		master.Close()
		t.Fatal(err)
	}

	dropLoginOnCleanup(t, master, login)

	t.Cleanup(func() {
		master.ExecContext(ctx, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.ExecContext(ctx, "DROP DATABASE ["+name+"]")
		master.Close()
	})

	if _, err := EnsureLogin(ConnectionParams{Server: testServer(), Database: name}, login, "Clave-123456!"); err != nil {
		t.Fatal(err)
	}

	runner, closeAll, err := Open(ConnectionParams{Server: testServer(), Database: name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()

	changed, err := runner.EnsureAccess(ctx, login)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}

	var isOwner int
	err = runner.conn.QueryRowContext(ctx, `SELECT IS_ROLEMEMBER(N'db_owner', @p1)`, login).Scan(&isOwner)
	if err != nil || isOwner != 1 {
		t.Fatalf("el usuario %q debe ser db_owner: %d, %v", login, isOwner, err)
	}
}
