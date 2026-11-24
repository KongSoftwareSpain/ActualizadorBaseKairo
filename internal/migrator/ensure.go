package migrator

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// openMaster connects to master with the login of params, to ask about or
// create the database that params names (which may not exist yet).
func openMaster(params ConnectionParams) (*sql.DB, error) {
	masterParams := params
	masterParams.Database = "master"

	dsn, err := buildDSN(masterParams)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, describeConnectError(masterParams, err)
	}

	return db, nil
}

func databaseExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var id sql.NullInt32

	if err := db.QueryRowContext(ctx, `SELECT DB_ID(@p1)`, name).Scan(&id); err != nil {
		return false, err
	}

	return id.Valid, nil
}

// DatabaseExists reports whether the database named in params exists on its
// server. Nothing is created or changed.
func DatabaseExists(params ConnectionParams) (bool, error) {
	db, err := openMaster(params)
	if err != nil {
		return false, err
	}
	defer db.Close()

	return databaseExists(context.Background(), db, strings.TrimSpace(params.Database))
}

// EnsureDatabase creates the database named in params, with the default files
// of the server, when it does not exist. It does nothing when it exists.
func EnsureDatabase(params ConnectionParams) (isCreated bool, err error) {
	db, err := openMaster(params)
	if err != nil {
		return false, err
	}
	defer db.Close()

	ctx := context.Background()
	name := strings.TrimSpace(params.Database)

	exists, err := databaseExists(ctx, db, name)
	if err != nil || exists {
		return false, err
	}

	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+quoteName(name)); err != nil {
		return false, fmt.Errorf("no se pudo crear la base de datos %q: %w (el login necesita el permiso CREATE ANY DATABASE)", name, err)
	}

	return true, nil
}
