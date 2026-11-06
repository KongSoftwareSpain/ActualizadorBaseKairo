package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/microsoft/go-mssqldb/batch"
)

const createVersionsTable = `
IF OBJECT_ID(N'dbo.SchemaVersions', N'U') IS NULL
CREATE TABLE dbo.SchemaVersions (
    Version      int            NOT NULL PRIMARY KEY,
    ScriptName   nvarchar(255)  NOT NULL,
    Checksum     varchar(64)    NULL,
    AppliedOn    datetime2      NOT NULL DEFAULT SYSDATETIME(),
    AppliedBy    nvarchar(128)  NOT NULL DEFAULT SUSER_SNAME(),
    DurationMs   int            NULL
)`

// Event reports progress to the caller (the UI log).
type Event struct {
	Kind    string `json:"kind"` // "info", "script-start", "script-done", "error"
	Version int    `json:"version"`
	Message string `json:"message"`
}

// Status describes the database against the available scripts.
type Status struct {
	CurrentVersion int      `json:"currentVersion"`
	AppliedCount   int      `json:"appliedCount"`
	Pending        []Script `json:"pending"`
	// NeedsBaseline is true for a database with user tables and no
	// SchemaVersions: it comes from Access and must be registered, not built.
	NeedsBaseline bool `json:"needsBaseline"`
}

// Runner holds one fixed connection: session options and locks depend on it.
type Runner struct {
	conn      *sql.Conn
	emit      func(Event)
	scripts   []Script
	backupDir string
}

// NewRunner takes ownership of conn. Call Close when done. backupDir must be
// writable by the SQL Server service account.
func NewRunner(conn *sql.Conn, scripts []Script, backupDir string, emit func(Event)) *Runner {
	if emit == nil {
		emit = func(Event) {}
	}

	return &Runner{conn: conn, scripts: scripts, backupDir: backupDir, emit: emit}
}

func (runner *Runner) Close() error {
	return runner.conn.Close()
}

// EnsureVersionsTable creates dbo.SchemaVersions when missing.
func (runner *Runner) EnsureVersionsTable(ctx context.Context) error {
	_, err := runner.conn.ExecContext(ctx, createVersionsTable)
	return err
}

// HasVersionsTable reports whether dbo.SchemaVersions exists.
func (runner *Runner) HasVersionsTable(ctx context.Context) (bool, error) {
	var id sql.NullInt64
	err := runner.conn.QueryRowContext(ctx, `SELECT OBJECT_ID(N'dbo.SchemaVersions', N'U')`).Scan(&id)

	return id.Valid, err
}

// Status lists applied and pending scripts. It fails if the database holds
// a version that has no script, or if the names differ.
func (runner *Runner) Status(ctx context.Context) (Status, error) {
	status := Status{}

	hasTable, err := runner.HasVersionsTable(ctx)
	if err != nil {
		return status, err
	}

	if !hasTable {
		status.Pending = runner.scripts

		var userTables int
		err := runner.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.tables WHERE is_ms_shipped = 0`).Scan(&userTables)
		status.NeedsBaseline = userTables > 0

		return status, err
	}

	rows, err := runner.conn.QueryContext(ctx, `SELECT Version, ScriptName FROM dbo.SchemaVersions ORDER BY Version`)
	if err != nil {
		return status, err
	}
	defer rows.Close()

	applied := map[int]string{}
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return status, err
		}
		applied[version] = name
		status.CurrentVersion = version
	}
	if err := rows.Err(); err != nil {
		return status, err
	}

	known := map[int]Script{}
	for _, script := range runner.scripts {
		known[script.Version] = script
	}

	for version, name := range applied {
		script, isKnown := known[version]
		if !isKnown {
			return status, fmt.Errorf("la BD tiene la versión V%04d (%s) que no existe en Scripts: BD más nueva que el actualizador", version, name)
		}
		if script.Name != name {
			return status, fmt.Errorf("V%04d aplicada como %q pero el fichero ahora se llama %q", version, name, script.Name)
		}
	}

	status.AppliedCount = len(applied)

	for _, script := range runner.scripts {
		if _, isApplied := applied[script.Version]; !isApplied {
			status.Pending = append(status.Pending, script)
		}
	}

	return status, nil
}

// Apply backs up the database and runs every pending script in order, one
// transaction per script. It stops at the first failure; earlier scripts stay
// committed. It returns the backup path, empty when nothing was pending.
func (runner *Runner) Apply(ctx context.Context) (backupPath string, err error) {
	status, err := runner.Status(ctx)
	if err != nil {
		return "", err
	}

	if status.NeedsBaseline {
		return "", errors.New("la BD tiene tablas pero no SchemaVersions: registra primero la versión base")
	}

	// No backup without pending scripts: it would overwrite the useful one.
	if len(status.Pending) == 0 {
		runner.emit(Event{Kind: "info", Message: "La base de datos ya está actualizada."})
		return "", nil
	}

	runner.emit(Event{Kind: "info", Message: "Creando backup previo..."})

	backupPath, err = runner.backup(ctx)
	if err != nil {
		runner.emit(Event{Kind: "error", Message: err.Error()})
		return "", err
	}

	if err := runner.EnsureVersionsTable(ctx); err != nil {
		return backupPath, err
	}

	for _, script := range status.Pending {
		if err := runner.applyScript(ctx, script); err != nil {
			message := fmt.Sprintf("%s: %v. Backup previo para volver atrás: %s", script.Name, err, backupPath)
			runner.emit(Event{Kind: "error", Version: script.Version, Message: message})

			return backupPath, errors.New(message)
		}
	}

	before := "BD sin versiones aplicadas"
	if status.CurrentVersion > 0 {
		before = fmt.Sprintf("BD en V%04d antes de actualizar", status.CurrentVersion)
	}

	runner.emit(Event{Kind: "backup", Message: fmt.Sprintf("Backup previo: %s (%s).", backupPath, before)})

	return backupPath, nil
}

// backup writes <backupDir>\<database>_preupdate.bak with INIT, so only one
// file per database ever remains. It runs outside any transaction.
func (runner *Runner) backup(ctx context.Context) (string, error) {
	var name string
	if err := runner.conn.QueryRowContext(ctx, `SELECT DB_NAME()`).Scan(&name); err != nil {
		return "", err
	}

	dir, err := filepath.Abs(runner.backupDir)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("no se pudo crear la carpeta de backups %q: %w", dir, err)
	}

	path := filepath.Join(dir, name+"_preupdate.bak")
	statement := "BACKUP DATABASE [" + strings.ReplaceAll(name, "]", "]]") + "] TO DISK = @p1 WITH INIT, CHECKSUM"

	if _, err := runner.conn.ExecContext(ctx, statement, path); err != nil {
		return "", fmt.Errorf("no se pudo crear el backup en %q: %w. La carpeta debe ser escribible por la "+
			"cuenta del servicio de SQL Server (por ejemplo NT Service\\MSSQL$SQLEXPRESS), no solo por tu usuario", path, err)
	}

	return path, nil
}

// RegisterBaseline records the first script as applied without running it.
func (runner *Runner) RegisterBaseline(ctx context.Context) error {
	status, err := runner.Status(ctx)
	if err != nil {
		return err
	}

	if !status.NeedsBaseline {
		return errors.New("la BD no necesita registrar versión base")
	}

	if len(runner.scripts) == 0 {
		return errors.New("no hay scripts: falta V0001__baseline.sql")
	}

	if err := runner.EnsureVersionsTable(ctx); err != nil {
		return err
	}

	baseline := runner.scripts[0]

	_, err = runner.conn.ExecContext(ctx,
		`INSERT INTO dbo.SchemaVersions (Version, ScriptName, Checksum, DurationMs) VALUES (@p1, @p2, @p3, 0)`,
		baseline.Version, baseline.Name, baseline.Checksum)
	if err != nil {
		return err
	}

	runner.emit(Event{Kind: "info", Version: baseline.Version, Message: fmt.Sprintf("Versión base registrada sin ejecutar: %s", baseline.Name)})

	return nil
}

func (runner *Runner) applyScript(ctx context.Context, script Script) (err error) {
	runner.emit(Event{Kind: "script-start", Version: script.Version, Message: script.Name})
	started := time.Now()

	tx, err := runner.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w (rollback falló: %v)", err, rollbackErr)
			}
		}
	}()

	for index, text := range batch.Split(script.Content, "GO") {
		if strings.TrimSpace(text) == "" {
			continue
		}

		if _, err = tx.ExecContext(ctx, text); err != nil {
			return fmt.Errorf("lote %d: %w", index+1, err)
		}
	}

	durationMs := int(time.Since(started).Milliseconds())

	_, err = tx.ExecContext(ctx,
		`INSERT INTO dbo.SchemaVersions (Version, ScriptName, Checksum, DurationMs) VALUES (@p1, @p2, @p3, @p4)`,
		script.Version, script.Name, script.Checksum, durationMs)
	if err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	runner.emit(Event{Kind: "script-done", Version: script.Version, Message: fmt.Sprintf("%s (%d ms)", script.Name, durationMs)})

	return nil
}
