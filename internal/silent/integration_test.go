package silent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"actualizador/internal/migrator"
)

// TestRunAgainstRealServer applies the real scripts to an empty database. It is
// skipped unless SILENT_TEST_SERVER, SILENT_TEST_DATABASE and SILENT_TEST_SCRIPTS
// are set. SILENT_TEST_USER and SILENT_TEST_PASSWORD are optional: without a
// user the connection uses Windows authentication. Use a throwaway database.
func TestRunAgainstRealServer(t *testing.T) {
	server := os.Getenv("SILENT_TEST_SERVER")
	database := os.Getenv("SILENT_TEST_DATABASE")
	scripts := os.Getenv("SILENT_TEST_SCRIPTS")

	if server == "" || database == "" || scripts == "" {
		t.Skip("define SILENT_TEST_SERVER, SILENT_TEST_DATABASE y SILENT_TEST_SCRIPTS para probar contra un SQL Server")
	}

	baseDir := t.TempDir()
	scriptsDir := filepath.Join(baseDir, "Scripts")

	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(scripts)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") && !strings.HasSuffix(strings.ToLower(entry.Name()), ".accept") {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(scripts, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(scriptsDir, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	options := Options{
		IsEnabled: true,
		IniPath:   writeIni(t, "[BaseDatos]\nServidor="+server+"\nBaseDatos="+database+"\n"),
		Username:  os.Getenv("SILENT_TEST_USER"),
		Password:  os.Getenv("SILENT_TEST_PASSWORD"),
	}

	if options.Username == "" {
		options.Password = "windows-authentication"
	}

	service := migrator.NewService(baseDir, nil)

	for round := 1; round <= 2; round++ {
		if _, err := Run(service, options); err != nil {
			t.Fatalf("vuelta %d: %v", round, err)
		}

		status, err := service.Connect(migrator.ConnectionParams{Server: server, Database: database, Username: options.Username, Password: options.Password})
		if err != nil {
			t.Fatal(err)
		}

		service.Disconnect()

		if len(status.Pending) != 0 || status.CurrentVersion != len(status.Scripts) || len(status.Modified) != 0 {
			t.Fatalf("vuelta %d: la BD no quedó actualizada: versión %d de %d, pendientes %d, modificados %d",
				round, status.CurrentVersion, len(status.Scripts), len(status.Pending), len(status.Modified))
		}
	}
}

// TestRunRefusesDatabaseFromAccess needs SILENT_TEST_ACCESS_DATABASE: a database
// that has user tables but no dbo.SchemaVersions. The silent run must stop
// without touching it, so that a technician registers the base version.
func TestRunRefusesDatabaseFromAccess(t *testing.T) {
	server := os.Getenv("SILENT_TEST_SERVER")
	database := os.Getenv("SILENT_TEST_ACCESS_DATABASE")
	scripts := os.Getenv("SILENT_TEST_SCRIPTS")

	if server == "" || database == "" || scripts == "" {
		t.Skip("define SILENT_TEST_SERVER, SILENT_TEST_ACCESS_DATABASE y SILENT_TEST_SCRIPTS")
	}

	baseDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, "Scripts"), 0o755); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(scripts, "V0001__kairo.sql"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(baseDir, "Scripts", "V0001__kairo.sql"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	options := Options{
		IsEnabled: true,
		IniPath:   writeIni(t, "[BaseDatos]\nServidor="+server+"\nBaseDatos="+database+"\n"),
		Username:  os.Getenv("SILENT_TEST_USER"),
		Password:  os.Getenv("SILENT_TEST_PASSWORD"),
	}

	service := migrator.NewService(baseDir, nil)

	_, runErr := Run(service, options)
	if runErr == nil || !strings.Contains(runErr.Error(), "SchemaVersions") {
		t.Fatalf("debía rechazar la BD de Access, devolvió: %v", runErr)
	}
}
