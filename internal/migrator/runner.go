package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
}

// Runner holds one fixed connection: session options and locks depend on it.
type Runner struct {
	conn    *sql.Conn
	emit    func(Event)
	scripts []Script
}

// NewRunner takes ownership of conn. Call Close when done.
func NewRunner(conn *sql.Conn, scripts []Script, emit func(Event)) *Runner {
	if emit == nil {
		emit = func(Event) {}
	}

	return &Runner{conn: conn, scripts: scripts, emit: emit}
}

func (runner *Runner) Close() error {
	return runner.conn.Close()
}

// EnsureVersionsTable creates dbo.SchemaVersions when missing.
func (runner *Runner) EnsureVersionsTable(ctx context.Context) error {
	_, err := runner.conn.ExecContext(ctx, createVersionsTable)
	return err
}

// Status lists applied and pending scripts. It fails if the database holds
// a version that has no script, or if the names differ.
func (runner *Runner) Status(ctx context.Context) (Status, error) {
	status := Status{}

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

// Apply runs every pending script in order, one transaction per script.
// It stops at the first failure; earlier scripts stay committed.
func (runner *Runner) Apply(ctx context.Context) error {
	status, err := runner.Status(ctx)
	if err != nil {
		return err
	}

	if len(status.Pending) == 0 {
		runner.emit(Event{Kind: "info", Message: "La base de datos ya está actualizada."})
		return nil
	}

	for _, script := range status.Pending {
		if err := runner.applyScript(ctx, script); err != nil {
			runner.emit(Event{Kind: "error", Version: script.Version, Message: fmt.Sprintf("%s: %v", script.Name, err)})
			return fmt.Errorf("%s: %w", script.Name, err)
		}
	}

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
