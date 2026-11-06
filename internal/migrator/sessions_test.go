package migrator

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// openOtherSession simulates the ERP left open: another connection to the
// same database.
func openOtherSession(t *testing.T, runner *Runner) *sql.Conn {
	t.Helper()

	db := openDB(t, runner.database)
	t.Cleanup(func() { db.Close() })

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	return conn
}

func TestStatusListsOtherSessions(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !status.CanSeeSessions {
		t.Skip("el login de prueba no tiene VIEW SERVER STATE")
	}

	if len(status.Sessions) != 0 {
		t.Fatalf("no debería haber otras sesiones: %+v", status.Sessions)
	}

	openOtherSession(t, runner)

	status, err = runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(status.Sessions) != 1 {
		t.Fatalf("se esperaba 1 sesión: %+v", status.Sessions)
	}
}

func TestApplyRefusesWhileOtherSessionsUnlessAllowed(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !status.CanSeeSessions {
		t.Skip("el login de prueba no tiene VIEW SERVER STATE")
	}

	openOtherSession(t, runner)

	if _, err := runner.Apply(ctx, ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "sesión") {
		t.Fatalf("se esperaba rechazo por sesiones: %v", err)
	}

	if tableExists(t, runner, "dbo.Clientes") {
		t.Fatal("no debe aplicarse nada al rechazar")
	}

	if _, err := runner.Apply(ctx, ApplyOptions{AllowOtherSessions: true}); err != nil {
		t.Fatal(err)
	}

	if !tableExists(t, runner, "dbo.Clientes") {
		t.Fatal("con confirmación debe aplicarse")
	}
}

func TestKickSessionsClosesOthersAndReopensDatabase(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	// The database name is known only after the runner touched the connection.
	if err := runner.pinDatabase(ctx); err != nil {
		t.Fatal(err)
	}

	other := openOtherSession(t, runner)

	if _, err := runner.Apply(ctx, ApplyOptions{KickSessions: true}); err != nil {
		t.Fatal(err)
	}

	// The server cuts the connection; its row in the DMV lingers a moment.
	var one int
	if err := other.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err == nil {
		t.Fatal("la sesión expulsada no debería poder consultar")
	}

	if !tableExists(t, runner, "dbo.Clientes") {
		t.Fatal("el script debe aplicarse")
	}

	var access int
	if err := runner.conn.QueryRowContext(ctx, `SELECT user_access FROM sys.databases WHERE database_id = DB_ID()`).Scan(&access); err != nil {
		t.Fatal(err)
	}

	if access != 0 {
		t.Fatalf("la BD debe volver a MULTI_USER, user_access=%d", access)
	}
}

func TestKickedDatabaseReopensWhenScriptFails(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__malo.sql", []byte("SELECT 1/0"))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx, ApplyOptions{KickSessions: true}); err == nil {
		t.Fatal("se esperaba el fallo del script")
	}

	var access int
	if err := runner.conn.QueryRowContext(ctx, `SELECT user_access FROM sys.databases WHERE database_id = DB_ID()`).Scan(&access); err != nil {
		t.Fatal(err)
	}

	if access != 0 {
		t.Fatalf("tras un fallo la BD debe volver a MULTI_USER, user_access=%d", access)
	}
}

func TestLockTimeoutStopsScriptBlockedByOtherProgram(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))
	writeScript(t, dir, "V0002__columna.sql", []byte("ALTER TABLE dbo.Clientes ADD Telefono nvarchar(20) NULL"))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)
	runner.lockTimeoutMs = 500

	// Apply V0001 only, then let another program hold the table.
	runner.scripts = runner.scripts[:1]
	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	other := openOtherSession(t, runner)
	if _, err := other.ExecContext(ctx, `BEGIN TRANSACTION; UPDATE dbo.Clientes SET Nombre = Nombre`); err != nil {
		t.Fatal(err)
	}

	scripts, err := LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}
	runner.scripts = scripts

	_, err = runner.Apply(ctx, ApplyOptions{AllowOtherSessions: true})
	if err == nil || !strings.Contains(err.Error(), "bloqueo") {
		t.Fatalf("se esperaba error por bloqueo: %v", err)
	}
}
