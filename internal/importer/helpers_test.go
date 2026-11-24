package importer

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

	"actualizador/internal/migrator"
)

func migratorOptions() migrator.ApplyOptions { return migrator.ApplyOptions{} }

func testServer() string {
	if server := os.Getenv("MIGRATOR_TEST_SERVER"); server != "" {
		return server
	}

	return `localhost\SQLEXPRESS`
}

func writeFile(t *testing.T, path string, content string) string {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

// writeKairo makes a Kairo folder with a config.ini and returns the folder.
func writeKairo(t *testing.T, server, database, extra string) string {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.ini"), "[BaseDatos]\r\nServidor="+server+"\r\nBaseDatos="+database+"\r\n"+extra)

	return dir
}

func writeSQL(t *testing.T, content string) string {
	t.Helper()

	return writeFile(t, filepath.Join(t.TempDir(), "script.sql"), content)
}

func newService(t *testing.T) *Service {
	t.Helper()

	return NewService(t.TempDir(), "secreto", nil)
}

// windowsAuthService uses the Windows login of the test machine instead of kong.
func windowsAuthService(t *testing.T) *Service {
	t.Helper()

	service := NewService(t.TempDir(), "", nil)
	service.username = ""

	return service
}

func openMaster(t *testing.T) *sql.DB {
	t.Helper()

	query := url.Values{}
	query.Set("database", "master")
	query.Set("trusted_connection", "yes")
	query.Set("TrustServerCertificate", "true")

	host, instance, _ := strings.Cut(testServer(), `\`)
	dsn := url.URL{Scheme: "sqlserver", Host: host, RawQuery: query.Encode()}
	if instance != "" {
		dsn.Path = "/" + instance
	}

	db, err := sql.Open("sqlserver", dsn.String())
	if err != nil {
		t.Fatal(err)
	}

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		t.Skipf("SQL Server no disponible: %v", err)
	}

	return db
}

// createDatabase makes a throwaway database and drops it when the test ends.
func createDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()

	master := openMaster(t)
	name := fmt.Sprintf("ImporterTest_%d", time.Now().UnixNano())

	if _, err := master.Exec("CREATE DATABASE [" + name + "]"); err != nil {
		master.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		master.Exec("ALTER DATABASE [" + name + "] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.Exec("DROP DATABASE [" + name + "]")
		master.Close()
	})

	return name, master
}

func execOn(t *testing.T, master *sql.DB, database, statement string) {
	t.Helper()

	if _, err := master.Exec("USE [" + database + "]; " + statement + "; USE master"); err != nil {
		t.Fatal(err)
	}
}

func tableExists(t *testing.T, master *sql.DB, database, table string) bool {
	t.Helper()

	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM [%s].sys.tables WHERE name = @p1", database)

	if err := master.QueryRow(query, table).Scan(&count); err != nil {
		t.Fatal(err)
	}

	return count > 0
}
