package migrator

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// maxCleanFailuresShown bounds how many undroppable objects the error lists.
const maxCleanFailuresShown = 5

// listDropStatements returns one DROP per user object, in an order that
// usually works at the first pass: database triggers, foreign keys, views,
// routines, tables, synonyms and sequences, types, XML schema collections and
// schemas last. Anything that still depends on something is retried by Clean.
const listDropStatements = `
SELECT statement FROM (
    SELECT 0 AS rank, N'DROP TRIGGER ' + QUOTENAME(name) + N' ON DATABASE' AS statement
    FROM sys.triggers WHERE parent_class = 0 AND is_ms_shipped = 0
  UNION ALL
    SELECT 1, N'ALTER TABLE ' + QUOTENAME(SCHEMA_NAME(t.schema_id)) + N'.' + QUOTENAME(t.name)
        + N' DROP CONSTRAINT ' + QUOTENAME(fk.name)
    FROM sys.foreign_keys fk JOIN sys.tables t ON t.object_id = fk.parent_object_id
    WHERE t.is_ms_shipped = 0
  UNION ALL
    SELECT CASE o.type
             WHEN 'V' THEN 2
             WHEN 'U' THEN 5
             WHEN 'SN' THEN 6
             WHEN 'SO' THEN 6
             WHEN 'AF' THEN 4
             WHEN 'P' THEN 3 WHEN 'PC' THEN 3 WHEN 'RF' THEN 3 WHEN 'X' THEN 3
             ELSE 4 END,
           CASE o.type
             WHEN 'V' THEN N'DROP VIEW '
             WHEN 'U' THEN N'DROP TABLE '
             WHEN 'SN' THEN N'DROP SYNONYM '
             WHEN 'SO' THEN N'DROP SEQUENCE '
             WHEN 'AF' THEN N'DROP AGGREGATE '
             WHEN 'P' THEN N'DROP PROCEDURE ' WHEN 'PC' THEN N'DROP PROCEDURE '
             WHEN 'RF' THEN N'DROP PROCEDURE ' WHEN 'X' THEN N'DROP PROCEDURE '
             ELSE N'DROP FUNCTION ' END
           + QUOTENAME(SCHEMA_NAME(o.schema_id)) + N'.' + QUOTENAME(o.name)
    FROM sys.objects o
    WHERE o.is_ms_shipped = 0
      AND o.type IN ('V','U','SN','SO','AF','P','PC','RF','X','FN','IF','TF','FS','FT')
  UNION ALL
    SELECT 7, N'DROP TYPE ' + QUOTENAME(SCHEMA_NAME(schema_id)) + N'.' + QUOTENAME(name)
    FROM sys.types WHERE is_user_defined = 1
  UNION ALL
    SELECT 8, N'DROP XML SCHEMA COLLECTION ' + QUOTENAME(SCHEMA_NAME(schema_id)) + N'.' + QUOTENAME(name)
    FROM sys.xml_schema_collections WHERE schema_id <> SCHEMA_ID(N'sys')
  UNION ALL
    SELECT 9, N'DROP SCHEMA ' + QUOTENAME(name)
    FROM sys.schemas WHERE schema_id > 4 AND schema_id < 16384
) AS drops
ORDER BY rank, statement`

// Clean drops every user object of the database (tables, views, routines,
// types, schemas...), SchemaVersions included, leaving it empty and ready for
// a full Apply. The database itself, its users and its files stay. It makes no
// backup: the data is gone for good.
func (runner *Runner) Clean(ctx context.Context, options ApplyOptions) (err error) {
	if err := runner.pinDatabase(ctx); err != nil {
		return err
	}

	// Cleaning master or msdb would break the server, not just a project.
	var databaseID int
	if err := runner.conn.QueryRowContext(ctx, `SELECT DB_ID()`).Scan(&databaseID); err != nil {
		return err
	}

	if databaseID <= 4 {
		return fmt.Errorf("%q es una base de datos del sistema: no se limpia", runner.database)
	}

	var status Status
	runner.readSessions(ctx, &status)

	release, err := runner.holdSessions(ctx, status, options)
	if err != nil {
		return err
	}
	defer release(&err)

	if _, err := runner.conn.ExecContext(ctx, fmt.Sprintf("SET LOCK_TIMEOUT %d", runner.lockTimeoutMs)); err != nil {
		return err
	}

	runner.emit(Event{Kind: "warning", Message: fmt.Sprintf("Limpiando la base de datos %s...", runner.database)})

	statements, err := runner.dropStatements(ctx)
	if err != nil {
		return err
	}

	total := len(statements)

	// One pass drops what nothing depends on any more; repeat while it helps.
	// A failure inside a pass is not final: the dependency may go in the next.
	for len(statements) > 0 {
		var failed []string
		var failures []string

		for _, statement := range statements {
			if _, execErr := runner.conn.ExecContext(ctx, statement); execErr != nil {
				failed = append(failed, statement)
				failures = append(failures, fmt.Sprintf("%s: %v", statement, execErr))
			}
		}

		if len(failed) == len(statements) {
			if len(failures) > maxCleanFailuresShown {
				failures = append(failures[:maxCleanFailuresShown], fmt.Sprintf("... y %d más", len(failures)-maxCleanFailuresShown))
			}

			err := fmt.Errorf("no se pudo eliminar %d objeto(s) de %d:\n%s", len(failed), total, strings.Join(failures, "\n"))
			runner.emit(Event{Kind: "error", Message: err.Error()})

			return err
		}

		statements = failed
	}

	if err := runner.clearRollbackBackup(ctx); err != nil {
		runner.emit(Event{Kind: "warning", Message: fmt.Sprintf("No se pudo limpiar la marca de backup: %v", err)})
	}

	runner.emit(Event{Kind: "info", Message: fmt.Sprintf("Base de datos limpia: %d objeto(s) eliminados. Pulsa Actualizar para reconstruirla desde V0001.", total)})

	return nil
}

// dropStatements reads the whole list before any DROP runs: the single
// connection cannot execute while a result set is open.
func (runner *Runner) dropStatements(ctx context.Context) ([]string, error) {
	rows, err := runner.conn.QueryContext(ctx, listDropStatements)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statements []string
	for rows.Next() {
		var statement sql.NullString
		if err := rows.Scan(&statement); err != nil {
			return nil, err
		}

		if statement.Valid {
			statements = append(statements, statement.String)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return statements, nil
}
