package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDSN(t *testing.T) {
	dsn, err := buildDSN(ConnectionParams{Server: `localhost\SQLEXPRESS`, Database: "Kairo"})
	if err != nil {
		t.Fatal(err)
	}

	for _, part := range []string{"sqlserver://localhost/SQLEXPRESS", "database=Kairo", "trusted_connection=yes"} {
		if !strings.Contains(dsn, part) {
			t.Errorf("dsn %q no contiene %q", dsn, part)
		}
	}

	dsn, err = buildDSN(ConnectionParams{Server: "srv", Database: "Kairo", Username: "sa", Password: "p@ss:/word"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(dsn, "p@ss:/word") || !strings.Contains(dsn, "sa:") {
		t.Errorf("la contraseña debe ir escapada: %q", dsn)
	}
}

func TestBuildDSNValidates(t *testing.T) {
	cases := []ConnectionParams{
		{Database: "Kairo"},
		{Server: "srv"},
		{Server: "srv", Database: "Kairo", Password: "sola"},
	}

	for _, params := range cases {
		if _, err := buildDSN(params); err == nil {
			t.Errorf("se esperaba error para %+v", params)
		}
	}
}

func TestServiceConnectAndApply(t *testing.T) {
	baseDir := t.TempDir()
	scriptsDir := filepath.Join(baseDir, "Scripts")
	if err := os.Mkdir(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, scriptsDir, "V0001__clientes.sql", []byte(createClientes))

	// Reuse the test helper only to create and drop a throwaway database.
	probe := newTestRunner(t, scriptsDir, nil)

	var databaseName string
	if err := probe.conn.QueryRowContext(t.Context(), "SELECT DB_NAME()").Scan(&databaseName); err != nil {
		t.Fatal(err)
	}

	var events []Event
	service := NewService(baseDir, func(event Event) { events = append(events, event) })
	t.Cleanup(func() { service.Disconnect() })

	status, err := service.Connect(ConnectionParams{Server: testServer(), Database: databaseName, BackupDir: probe.backupDir})
	if err != nil {
		t.Fatal(err)
	}

	if len(status.Pending) != 1 {
		t.Fatalf("pendientes: %d, esperado 1", len(status.Pending))
	}

	// The probe connection is still open: it counts as another session.
	result, err := service.Apply(ApplyOptions{AllowOtherSessions: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Status.Pending) != 0 || result.Status.CurrentVersion != 1 {
		t.Fatalf("estado tras aplicar: %+v", result.Status)
	}

	if !strings.HasSuffix(result.BackupPath, databaseName+"_preupdate.bak") {
		t.Fatalf("ruta de backup inesperada: %q", result.BackupPath)
	}

	logs, err := filepath.Glob(filepath.Join(baseDir, "Logs", "*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("log esperado en Logs: %v %v", logs, err)
	}

	if len(events) == 0 {
		t.Fatal("no se emitieron eventos")
	}
}

func TestServiceConnectFailureHint(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(baseDir, "Scripts"), 0o755); err != nil {
		t.Fatal(err)
	}

	service := NewService(baseDir, nil)

	_, err := service.Connect(ConnectionParams{Server: `localhost\NoExisteInstancia`, Database: "x"})
	if err == nil || !strings.Contains(err.Error(), "TCP/IP") {
		t.Fatalf("se esperaba error con pista de TCP/IP, got %v", err)
	}
}

func TestResolveDir(t *testing.T) {
	baseDir := t.TempDir()
	service := NewService(baseDir, nil)
	absolute := filepath.Join(t.TempDir(), "otros")

	cases := []struct{ input, fallback, want string }{
		{"", "Scripts", filepath.Join(baseDir, "Scripts")},
		{"./Scripts", "Scripts", filepath.Join(baseDir, "Scripts")},
		{"../ScriptsRepo", "Scripts", filepath.Join(filepath.Dir(baseDir), "ScriptsRepo")},
		{absolute, "Scripts", absolute},
	}

	for _, testCase := range cases {
		if got := service.resolveDir(testCase.input, testCase.fallback); got != testCase.want {
			t.Errorf("resolveDir(%q) = %q, esperado %q", testCase.input, got, testCase.want)
		}
	}
}

func TestBuildDSNWhitespaceUserIsWindows(t *testing.T) {
	dsn, err := buildDSN(ConnectionParams{Server: "srv", Database: "Kairo", Username: "  "})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(dsn, "trusted_connection=yes") {
		t.Errorf("se esperaba autenticación de Windows: %q", dsn)
	}
}
