package migrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeTextRejectsAmbiguousEncodings(t *testing.T) {
	cases := map[string][]byte{
		"ANSI (Windows-1252)":  []byte("SELECT N'a\xf1o'"),
		"UTF-16 sin BOM":       {'S', 0, 'E', 0, 'L', 0},
		"UTF-16 LE incompleto": {0xFF, 0xFE, 'S'},
		"sustituto suelto":     {0xFF, 0xFE, 0x00, 0xD8, 'a', 0},
		"U+FFFD literal":       []byte("SELECT '\xef\xbf\xbd'"),
		"UTF-8 BOM inválido":   {0xEF, 0xBB, 0xBF, 0xff, 0xfe},
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeText(raw); err == nil {
				t.Fatal("se esperaba error")
			}
		})
	}
}

func TestDecodeTextUTF16BigEndian(t *testing.T) {
	raw := []byte{0xFE, 0xFF, 0x00, 'a', 0x00, 0xF1, 0x00, 'o'}

	text, err := decodeText(raw)
	if err != nil {
		t.Fatal(err)
	}

	if text != "año" {
		t.Fatalf("texto: %q", text)
	}
}

func TestChecksumIgnoresLineEndings(t *testing.T) {
	lf := t.TempDir()
	crlf := t.TempDir()
	writeScript(t, lf, "V0001__a.sql", []byte("SELECT 1\nGO\nSELECT 2\n"))
	writeScript(t, crlf, "V0001__a.sql", []byte("SELECT 1\r\nGO\r\nSELECT 2\r\n"))

	first, err := LoadScripts(lf)
	if err != nil {
		t.Fatal(err)
	}

	second, err := LoadScripts(crlf)
	if err != nil {
		t.Fatal(err)
	}

	if first[0].Checksum != second[0].Checksum {
		t.Fatal("el checksum depende de los saltos de línea")
	}
}

func TestSplitServer(t *testing.T) {
	cases := []struct{ input, host, port, instance string }{
		{`localhost\SQLEXPRESS`, "localhost", "", "SQLEXPRESS"},
		{"192.168.1.10,1433", "192.168.1.10", "1433", ""},
		{`srv,1434\INST`, "srv", "1434", "INST"},
		{" srv ", "srv", "", ""},
	}

	for _, testCase := range cases {
		host, port, instance := splitServer(testCase.input)
		if host != testCase.host || port != testCase.port || instance != testCase.instance {
			t.Errorf("splitServer(%q) = %q %q %q", testCase.input, host, port, instance)
		}
	}
}

func TestBuildDSNWithPort(t *testing.T) {
	dsn, err := buildDSN(ConnectionParams{Server: "192.168.1.10,1433", Database: "Kairo"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(dsn, "sqlserver://192.168.1.10:1433") {
		t.Errorf("el puerto debe ir como host:puerto: %q", dsn)
	}

	if strings.Contains(dsn, "TrustServerCertificate") {
		t.Errorf("un servidor remoto no debe confiar a ciegas en el certificado: %q", dsn)
	}

	local, err := buildDSN(ConnectionParams{Server: `localhost\SQLEXPRESS`, Database: "Kairo"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(local, "TrustServerCertificate=true") {
		t.Errorf("el servidor local sí debe confiar en el certificado: %q", local)
	}
}

func TestIsLocalServer(t *testing.T) {
	hostname, _ := os.Hostname()

	for _, server := range []string{"localhost", `localhost\SQLEXPRESS`, "(local)", ".", "127.0.0.1,1433", "tcp:localhost", hostname} {
		if !isLocalServer(server) {
			t.Errorf("%q debería ser local", server)
		}
	}

	for _, server := range []string{"192.168.1.10", "srv-clientes", `otro\SQLEXPRESS`} {
		if isLocalServer(server) {
			t.Errorf("%q no debería ser local", server)
		}
	}
}

func TestScriptWithUseIsRejectedAndSessionRestored(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__usa_use.sql", []byte(
		"CREATE TABLE dbo.Antes (Id int)\nGO\nUSE master\nGO\nCREATE TABLE dbo.TmpUseProbe (Id int)\n"))
	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Status(ctx); err != nil {
		t.Fatal(err)
	}

	original := runner.database

	_, err := runner.Apply(ctx, ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "USE") {
		t.Fatalf("se esperaba un error que mencione USE: %v", err)
	}

	var current string
	if err := runner.conn.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&current); err != nil {
		t.Fatal(err)
	}

	if current != original {
		t.Fatalf("la sesión se quedó en %q, esperado %q", current, original)
	}

	var leaked int
	if err := runner.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM master.sys.tables WHERE name = N'TmpUseProbe'").Scan(&leaked); err != nil {
		t.Fatal(err)
	}

	if leaked != 0 {
		t.Fatal("el lote posterior al USE se ejecutó en master")
	}

	if tableExists(t, runner, "dbo.Antes") || tableExists(t, runner, "dbo.Pedidos") {
		t.Fatal("el script con USE debe revertirse y no seguir con el siguiente")
	}
}

func TestFailedUpdateKeepsRollbackBackup(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))
	writeScript(t, dir, "V0002__roto.sql", []byte("INSERT INTO dbo.NoExiste VALUES (1)\n"))

	ctx := context.Background()
	var events []Event
	runner := newTestRunner(t, dir, &events)

	firstPath, err := runner.Apply(ctx, ApplyOptions{})
	if err == nil {
		t.Fatal("se esperaba error")
	}

	marked, err := runner.rollbackBackup(ctx)
	if err != nil || marked != firstPath {
		t.Fatalf("la marca del backup: %q (%v), esperado %q", marked, err, firstPath)
	}

	// The script is fixed and the update is retried.
	writeScript(t, dir, "V0002__roto.sql", []byte(createPedidos))
	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	events = nil

	secondPath, err := runner.Apply(ctx, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if secondPath != firstPath {
		t.Fatalf("el reintento usó otro backup: %q y %q", firstPath, secondPath)
	}

	for _, event := range events {
		if strings.Contains(event.Message, "Creando backup previo") {
			t.Fatal("el reintento sobrescribió el backup que servía para volver atrás")
		}
	}

	kept := eventsOfKind(events, "backup")
	if len(kept) != 1 || !strings.Contains(kept[0].Message, "conservado") {
		t.Fatalf("mensaje final inesperado: %+v", kept)
	}

	if marked, err := runner.rollbackBackup(ctx); err != nil || marked != "" {
		t.Fatalf("la marca debe borrarse al terminar: %q (%v)", marked, err)
	}
}

func TestNewBackupAfterCompletedUpdate(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	var events []Event
	runner := newTestRunner(t, dir, &events)

	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))

	var err error
	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	events = nil

	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	var created bool
	for _, event := range events {
		created = created || strings.Contains(event.Message, "Creando backup previo")
	}

	if !created {
		t.Fatal("tras una actualización completa, la siguiente debe crear su propio backup")
	}
}

func TestModifiedAppliedScriptBlocksApply(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	// Same content with Windows line endings: not a modification.
	writeScript(t, dir, "V0001__clientes.sql", []byte(strings.ReplaceAll(createClientes, "\n", "\r\n")))

	var err error
	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(status.Modified) != 0 {
		t.Fatalf("los saltos de línea no son una modificación: %+v", status.Modified)
	}

	// A real edit is.
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes+"\nINSERT INTO dbo.Clientes VALUES (2, N'Otro')\n"))

	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))

	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	status, err = runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(status.Modified) != 1 || status.Modified[0].Version != 1 {
		t.Fatalf("scripts modificados: %+v", status.Modified)
	}

	if _, err := runner.Apply(ctx, ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "V0001__clientes.sql") {
		t.Fatalf("Apply debe negarse y nombrar el script: %v", err)
	}

	if tableExists(t, runner, "dbo.Pedidos") {
		t.Fatal("se aplicó un script pese a haber uno modificado")
	}
}

func TestStatusRejectsRenamedScript(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(filepath.Join(dir, "V0001__clientes.sql"), filepath.Join(dir, "V0001__otro_nombre.sql")); err != nil {
		t.Fatal(err)
	}

	var err error
	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Status(ctx); err == nil || !strings.Contains(err.Error(), "otro_nombre") {
		t.Fatalf("se esperaba error por script renombrado: %v", err)
	}
}

func TestSecondInstanceCannotLockSameDatabase(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__clientes.sql", []byte(createClientes))

	ctx := context.Background()
	first := newTestRunner(t, dir, nil)

	if err := first.Lock(ctx); err != nil {
		t.Fatal(err)
	}

	db := openDB(t, first.database)
	t.Cleanup(func() { db.Close() })

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}

	second := NewRunner(conn, first.scripts, first.backupDir, nil)
	t.Cleanup(func() { second.Close() })

	if err := second.Lock(ctx); err == nil || !strings.Contains(err.Error(), "otra instancia") {
		t.Fatalf("la segunda instancia debe rechazarse: %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	if err := second.Lock(ctx); err != nil {
		t.Fatalf("al cerrar la primera, la segunda debe poder bloquear: %v", err)
	}
}

func TestServiceRejectsRelativeBackupDirOnRemoteServer(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(baseDir, "Scripts"), 0o755); err != nil {
		t.Fatal(err)
	}

	service := NewService(baseDir, nil)

	_, err := service.Connect(ConnectionParams{Server: "servidor-remoto-inexistente", Database: "x", BackupDir: "./Backups"})
	if err == nil || !strings.Contains(err.Error(), "ruta absoluta") {
		t.Fatalf("se esperaba error de ruta absoluta, got %v", err)
	}
}

func TestServiceConnectLoginFailure(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(baseDir, "Scripts"), 0o755); err != nil {
		t.Fatal(err)
	}

	service := NewService(baseDir, nil)

	_, err := service.Connect(ConnectionParams{Server: testServer(), Database: "master", Username: "usuario_que_no_existe", Password: "x"})
	if err == nil {
		t.Fatal("se esperaba error de login")
	}

	if !strings.Contains(err.Error(), "credenciales") {
		t.Fatalf("se esperaba el mensaje de credenciales, got %v", err)
	}
}

func TestServiceStatusRefusedWhileRunning(t *testing.T) {
	service := NewService(t.TempDir(), nil)
	service.runner = &Runner{}
	service.isRunning = true

	if !service.IsRunning() {
		t.Fatal("IsRunning debería ser true")
	}

	if _, err := service.Status(); err != errBusy {
		t.Fatalf("Status durante una actualización: %v", err)
	}
}

func TestServiceWarnsOnceWhenLogCannotBeWritten(t *testing.T) {
	// A regular file where the Logs folder should be.
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "Logs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var events []Event
	service := NewService(baseDir, func(event Event) { events = append(events, event) })

	service.publish(Event{Kind: "info", Message: "uno"})
	service.publish(Event{Kind: "info", Message: "dos"})

	if warnings := eventsOfKind(events, "warning"); len(warnings) != 1 {
		t.Fatalf("avisos del log: %d, esperado 1", len(warnings))
	}

	if len(events) != 3 {
		t.Fatalf("eventos: %d, esperado 3 (aviso + 2 originales)", len(events))
	}
}

func TestScriptThatBreaksTheTransactionIsRejected(t *testing.T) {
	cases := map[string]string{
		"COMMIT suelto":             "CREATE TABLE dbo.T1 (Id int)\nGO\nCOMMIT\nGO\nCREATE TABLE dbo.T2 (Id int)\n",
		"ROLLBACK suelto":           "CREATE TABLE dbo.T1 (Id int)\nGO\nROLLBACK\nGO\nCREATE TABLE dbo.T2 (Id int)\n",
		"BEGIN TRAN sin cierre":     "BEGIN TRANSACTION\nGO\nCREATE TABLE dbo.T1 (Id int)\n",
		"BEGIN TRAN y COMMIT pares": "",
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeScript(t, dir, "V0001__a.sql", []byte(content))

			ctx := context.Background()
			runner := newTestRunner(t, dir, nil)

			if content == "" {
				// Balanced explicit transactions are fine (SSMS table designer).
				writeScript(t, dir, "V0001__a.sql", []byte("BEGIN TRANSACTION\nGO\nCREATE TABLE dbo.T1 (Id int)\nGO\nCOMMIT\n"))

				var err error
				runner.scripts, err = LoadScripts(dir)
				if err != nil {
					t.Fatal(err)
				}

				if _, err := runner.Apply(ctx, ApplyOptions{}); err != nil {
					t.Fatalf("una transacción propia y equilibrada debe funcionar: %v", err)
				}

				return
			}

			if _, err := runner.Apply(ctx, ApplyOptions{}); err == nil {
				t.Fatal("se esperaba error")
			}

			// A stray COMMIT makes what came before it permanent: it can be
			// detected and stopped, not undone. Everything else is rolled back.
			if name != "COMMIT suelto" && tableExists(t, runner, "dbo.T1") {
				t.Fatal("el script dejó cambios aplicados")
			}

			if tableExists(t, runner, "dbo.T2") {
				t.Fatal("se ejecutó un lote posterior al que rompió la transacción")
			}

			if rows := countRows(t, runner, "dbo.SchemaVersions"); rows != 0 {
				t.Fatalf("no debe registrarse una versión de un script rechazado: %d filas", rows)
			}

			var depth int
			if err := runner.conn.QueryRowContext(ctx, "SELECT @@TRANCOUNT").Scan(&depth); err != nil {
				t.Fatal(err)
			}

			if depth != 0 {
				t.Fatalf("quedó una transacción abierta: @@TRANCOUNT = %d", depth)
			}
		})
	}
}

func TestPendingBelowCurrentVersionIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__a.sql", []byte("SELECT 1"))
	writeScript(t, dir, "V0002__b.sql", []byte("SELECT 2"))
	writeScript(t, dir, "V0003__c.sql", []byte("SELECT 3"))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	if err := runner.EnsureVersionsTable(ctx); err != nil {
		t.Fatal(err)
	}

	// V0002 was skipped by hand.
	execSQL(t, runner, "INSERT INTO dbo.SchemaVersions (Version, ScriptName) VALUES (1, N'V0001__a.sql'), (3, N'V0003__c.sql')")

	if _, err := runner.Apply(ctx, ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "fuera de orden") {
		t.Fatalf("se esperaba error por orden: %v", err)
	}
}

func TestBaselineChecksumIsNotVerified(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0001__baseline.sql", []byte(createClientes))
	writeScript(t, dir, "V0002__pedidos.sql", []byte(createPedidos))

	ctx := context.Background()
	runner := newTestRunner(t, dir, nil)

	execSQL(t, runner, "CREATE TABLE dbo.Clientes (Id int NOT NULL PRIMARY KEY, Nombre nvarchar(50) NOT NULL)")

	if err := runner.RegisterBaseline(ctx); err != nil {
		t.Fatal(err)
	}

	// The baseline is cleaned up later: that must not block clients.
	writeScript(t, dir, "V0001__baseline.sql", []byte(createClientes+"\n-- limpieza\n"))

	var err error
	runner.scripts, err = LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(status.Modified) != 0 {
		t.Fatalf("la baseline registrada no debe verificarse: %+v", status.Modified)
	}
}

func TestSafeFileName(t *testing.T) {
	if got := safeFileName(`Kairo:2026/prod*`); got != "Kairo_2026_prod_" {
		t.Fatalf("safeFileName = %q", got)
	}

	if got := safeFileName("Kairo"); got != "Kairo" {
		t.Fatalf("safeFileName = %q", got)
	}
}

func TestDescribeConnectErrorCertificate(t *testing.T) {
	err := describeConnectError(ConnectionParams{Server: "srv", Database: "x"}, errors.New("x509: certificate signed by unknown authority"))

	if !strings.Contains(err.Error(), "certificado") {
		t.Fatalf("se esperaba la pista del certificado: %v", err)
	}
}
