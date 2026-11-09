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

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/batch"
)

const lockTimeoutErrorNumber = 1222

// defaultLockTimeoutMs bounds how long a script waits for a lock held by
// another program (the ERP left open). Without it the update hangs forever.
const defaultLockTimeoutMs = 60000

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

// rollbackProperty is a database-level extended property that remembers the
// backup taken before an update that did not finish. While it exists, a retry
// keeps that backup instead of overwriting it with a newer state.
const rollbackProperty = "Actualizador.RollbackBackup"

// appLockResource is locked for the whole session, so that two instances of
// the tool cannot update the same database at once (locks are per database).
const appLockResource = "Actualizador"

// Event reports progress to the caller (the UI log).
type Event struct {
	Kind    string `json:"kind"` // "info", "warning", "script-start", "script-done", "backup", "error"
	Version int    `json:"version"`
	Message string `json:"message"`
}

// Status describes the database against the available scripts.
type Status struct {
	CurrentVersion int `json:"currentVersion"`
	AppliedCount   int `json:"appliedCount"`
	// Scripts lists every script found, applied or not, in version order.
	Scripts []Script `json:"scripts"`
	Pending []Script `json:"pending"`
	// Modified lists applied scripts whose file changed after being applied.
	// An applied script is never edited: the fix is a new script.
	Modified []Script `json:"modified"`
	// NeedsBaseline is true for a database with user tables and no
	// SchemaVersions: it comes from Access and must be registered, not built.
	NeedsBaseline bool `json:"needsBaseline"`
	// Sessions are the other connections using the database (the ERP left
	// open). CanSeeSessions is false when the login lacks VIEW SERVER STATE:
	// an empty list then means "unknown", not "nobody".
	Sessions       []Session `json:"sessions"`
	CanSeeSessions bool      `json:"canSeeSessions"`
}

// Session is another connection to the database.
type Session struct {
	ID      int    `json:"id"`
	Host    string `json:"host"`
	Program string `json:"program"`
	Login   string `json:"login"`
}

// ApplyOptions says what to do about other sessions on the database.
type ApplyOptions struct {
	// AllowOtherSessions continues although other sessions are connected.
	AllowOtherSessions bool `json:"allowOtherSessions"`
	// KickSessions closes them for the duration of the update (single-user
	// mode, rolling back what they have open). It implies AllowOtherSessions.
	KickSessions bool `json:"kickSessions"`
}

// Runner holds one fixed connection: session options and locks depend on it.
type Runner struct {
	conn      *sql.Conn
	emit      func(Event)
	scripts   []Script
	backupDir string
	// isRemote is true when SQL Server runs on another machine: backupDir is
	// then a path on that machine and this process cannot create it.
	isRemote bool
	// database is the database of the connection. Scripts must not leave it.
	database      string
	isLocked      bool
	lockTimeoutMs int
}

// NewRunner takes ownership of conn. Call Close when done. backupDir must be
// writable by the SQL Server service account.
func NewRunner(conn *sql.Conn, scripts []Script, backupDir string, emit func(Event)) *Runner {
	if emit == nil {
		emit = func(Event) {}
	}

	return &Runner{conn: conn, scripts: scripts, backupDir: backupDir, emit: emit, lockTimeoutMs: defaultLockTimeoutMs}
}

func (runner *Runner) Close() error {
	if runner.isLocked {
		// Best effort: closing the session releases the lock anyway.
		runner.conn.ExecContext(context.Background(),
			`EXEC sp_releaseapplock @Resource = N'`+appLockResource+`', @LockOwner = N'Session'`)
		runner.isLocked = false
	}

	return runner.conn.Close()
}

// Lock takes the session lock of the database. It fails at once, without
// waiting, when another instance holds it.
func (runner *Runner) Lock(ctx context.Context) error {
	if err := runner.pinDatabase(ctx); err != nil {
		return err
	}

	var code int
	err := runner.conn.QueryRowContext(ctx,
		`DECLARE @result int;
		EXEC @result = sp_getapplock @Resource = N'`+appLockResource+`', @LockMode = N'Exclusive', @LockOwner = N'Session', @LockTimeout = 0;
		SELECT @result`).Scan(&code)
	if err != nil {
		return err
	}

	if code < 0 {
		return errors.New("otra instancia del actualizador está usando esta base de datos")
	}

	runner.isLocked = true

	return nil
}

// pinDatabase remembers the database of the connection the first time, and
// afterwards moves the session back to it if a script switched with USE.
// USE is not transactional: a rollback does not undo it.
func (runner *Runner) pinDatabase(ctx context.Context) error {
	var current string
	if err := runner.conn.QueryRowContext(ctx, `SELECT DB_NAME()`).Scan(&current); err != nil {
		return err
	}

	if runner.database == "" {
		runner.database = current
		return nil
	}

	if current == runner.database {
		return nil
	}

	runner.emit(Event{Kind: "warning", Message: fmt.Sprintf("La sesión estaba en %q: se vuelve a %q.", current, runner.database)})

	_, err := runner.conn.ExecContext(ctx, "USE "+quoteName(runner.database))

	return err
}

// safeFileName replaces what Windows does not allow in a file name. SQL Server
// accepts database names that it would reject.
func safeFileName(name string) string {
	return strings.Map(func(char rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, char) || char < 32 {
			return '_'
		}

		return char
	}, name)
}

func quoteName(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
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

type appliedRow struct {
	name     string
	checksum string
}

// Status lists applied and pending scripts. It fails if the database holds
// a version that has no script, or if the names differ.
func (runner *Runner) Status(ctx context.Context) (Status, error) {
	status := Status{Scripts: runner.scripts}

	if err := runner.pinDatabase(ctx); err != nil {
		return status, err
	}

	runner.readSessions(ctx, &status)

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

	rows, err := runner.conn.QueryContext(ctx, `SELECT Version, ScriptName, Checksum FROM dbo.SchemaVersions ORDER BY Version`)
	if err != nil {
		return status, err
	}
	defer rows.Close()

	applied := map[int]appliedRow{}
	for rows.Next() {
		var version int
		var name string
		var checksum sql.NullString
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return status, err
		}
		applied[version] = appliedRow{name: name, checksum: checksum.String}
		status.CurrentVersion = version
	}
	if err := rows.Err(); err != nil {
		return status, err
	}

	known := map[int]Script{}
	for _, script := range runner.scripts {
		known[script.Version] = script
	}

	for version, row := range applied {
		script, isKnown := known[version]
		if !isKnown {
			return status, fmt.Errorf("la BD tiene la versión V%04d (%s) que no existe en Scripts: BD más nueva que el actualizador", version, row.name)
		}
		if script.Name != row.name {
			return status, fmt.Errorf("V%04d aplicada como %q pero el fichero ahora se llama %q", version, row.name, script.Name)
		}
	}

	status.AppliedCount = len(applied)

	for _, script := range runner.scripts {
		row, isApplied := applied[script.Version]
		if !isApplied {
			// Applying below the current version would run scripts out of order.
			if script.Version < status.CurrentVersion {
				return status, fmt.Errorf("%s está pendiente pero la BD ya tiene V%04d: no se aplican scripts fuera de orden",
					script.Name, status.CurrentVersion)
			}

			status.Pending = append(status.Pending, script)
			continue
		}

		// A missing checksum (NULL) means it was never recorded: nothing to compare.
		if row.checksum != "" && row.checksum != script.Checksum {
			status.Modified = append(status.Modified, script)
		}
	}

	return status, nil
}

// Apply backs up the database and runs every pending script in order, one
// transaction per script. It stops at the first failure; earlier scripts stay
// committed. It returns the backup path, empty when nothing was pending.
func (runner *Runner) Apply(ctx context.Context, options ApplyOptions) (backupPath string, err error) {
	status, err := runner.Status(ctx)
	if err != nil {
		return "", err
	}

	if status.NeedsBaseline {
		return "", errors.New("la BD tiene tablas pero no SchemaVersions: registra primero la versión base")
	}

	if len(status.Modified) > 0 {
		names := make([]string, len(status.Modified))
		for index, script := range status.Modified {
			names[index] = script.Name
		}

		return "", fmt.Errorf("scripts ya aplicados modificados: %s. Un script aplicado nunca se edita: restaura su contenido y crea uno nuevo",
			strings.Join(names, ", "))
	}

	// No backup without pending scripts: it would overwrite the useful one.
	if len(status.Pending) == 0 {
		runner.emit(Event{Kind: "info", Message: "La base de datos ya está actualizada."})
		return "", nil
	}

	release, err := runner.holdSessions(ctx, status, options)
	if err != nil {
		return "", err
	}
	defer release(&err)

	backupPath, isKept, err := runner.prepareBackup(ctx)
	if err != nil {
		runner.emit(Event{Kind: "error", Message: err.Error()})
		return "", err
	}

	if err := runner.EnsureVersionsTable(ctx); err != nil {
		return backupPath, err
	}

	if _, err := runner.conn.ExecContext(ctx, fmt.Sprintf("SET LOCK_TIMEOUT %d", runner.lockTimeoutMs)); err != nil {
		return backupPath, err
	}

	for _, script := range status.Pending {
		if err := runner.applyScript(ctx, script); err != nil {
			var sqlErr mssql.Error
			if errors.As(err, &sqlErr) && sqlErr.Number == lockTimeoutErrorNumber {
				err = fmt.Errorf("%w. Esperó más de %d s por un bloqueo: otro programa (¿el ERP abierto?) está usando la base de datos",
					err, runner.lockTimeoutMs/1000)
			}

			message := fmt.Sprintf("%s: %v. Backup previo para volver atrás: %s", script.Name, err, backupPath)
			runner.emit(Event{Kind: "error", Version: script.Version, Message: message})

			return backupPath, errors.New(message)
		}
	}

	// The update is complete: the backup no longer has to be protected.
	if err := runner.clearRollbackBackup(ctx); err != nil {
		runner.emit(Event{Kind: "warning", Message: fmt.Sprintf("No se pudo limpiar la marca de backup: %v", err)})
	}

	switch {
	case isKept:
		runner.emit(Event{Kind: "backup", Message: fmt.Sprintf("Backup previo: %s (conservado de un intento anterior que no terminó).", backupPath)})
	case status.CurrentVersion > 0:
		runner.emit(Event{Kind: "backup", Message: fmt.Sprintf("Backup previo: %s (BD en V%04d antes de actualizar).", backupPath, status.CurrentVersion)})
	default:
		runner.emit(Event{Kind: "backup", Message: fmt.Sprintf("Backup previo: %s (BD sin versiones aplicadas).", backupPath)})
	}

	return backupPath, nil
}

// holdSessions applies the policy for other sessions: refuse, allow, or kick
// them. The returned function must be deferred with a pointer to the caller's
// error: it reopens the database when it was closed, and adds a failure to
// reopen to that error.
func (runner *Runner) holdSessions(ctx context.Context, status Status, options ApplyOptions) (release func(*error), err error) {
	if !options.KickSessions {
		if len(status.Sessions) > 0 && !options.AllowOtherSessions {
			err := fmt.Errorf("hay %d sesión(es) más conectadas a la base de datos (¿el programa abierto en algún equipo?): ciérralas, o confirma continuar o expulsarlas", len(status.Sessions))
			runner.emit(Event{Kind: "error", Message: err.Error()})

			return nil, err
		}

		return func(*error) {}, nil
	}

	if err := runner.setSingleUser(ctx, len(status.Sessions)); err != nil {
		runner.emit(Event{Kind: "error", Message: err.Error()})
		return nil, err
	}

	// The database must not stay closed to the ERP, whatever happens next.
	return func(target *error) {
		restoreErr := runner.restoreMultiUser()
		if restoreErr == nil {
			return
		}

		runner.emit(Event{Kind: "error", Message: restoreErr.Error()})

		if *target == nil {
			*target = restoreErr
		} else {
			*target = fmt.Errorf("%w (%v)", *target, restoreErr)
		}
	}, nil
}

// readSessions fills the other sessions on the database. Without VIEW SERVER
// STATE the server hides them, so the list would be empty without meaning it.
func (runner *Runner) readSessions(ctx context.Context, status *Status) {
	var hasPermission sql.NullInt32
	if err := runner.conn.QueryRowContext(ctx, `SELECT HAS_PERMS_BY_NAME(NULL, NULL, 'VIEW SERVER STATE')`).Scan(&hasPermission); err != nil || hasPermission.Int32 != 1 {
		return
	}

	rows, err := runner.conn.QueryContext(ctx,
		`SELECT session_id, ISNULL(host_name, N''), ISNULL(program_name, N''), ISNULL(login_name, N'')
		FROM sys.dm_exec_sessions
		WHERE is_user_process = 1 AND database_id = DB_ID() AND session_id <> @@SPID
		ORDER BY session_id`)
	if err != nil {
		return
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.Host, &session.Program, &session.Login); err != nil {
			return
		}
		sessions = append(sessions, session)
	}

	if rows.Err() != nil {
		return
	}

	status.Sessions = sessions
	status.CanSeeSessions = true
}

// setSingleUser leaves only this connection in the database, rolling back
// what the others had open.
func (runner *Runner) setSingleUser(ctx context.Context, count int) error {
	if err := runner.pinDatabase(ctx); err != nil {
		return err
	}

	runner.emit(Event{Kind: "warning", Message: fmt.Sprintf("Se cierran las demás sesiones (%d visibles) para actualizar...", count)})

	_, err := runner.conn.ExecContext(ctx, "ALTER DATABASE "+quoteName(runner.database)+" SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
	if err != nil {
		return fmt.Errorf("no se pudo cerrar las demás sesiones: %w", err)
	}

	return nil
}

// restoreMultiUser reopens the database. It does not use the caller's context:
// it must run even if the update was cancelled.
func (runner *Runner) restoreMultiUser() error {
	ctx := context.Background()

	if err := runner.pinDatabase(ctx); err != nil {
		return fmt.Errorf("la base de datos puede haber quedado en modo un solo usuario; ejecuta ALTER DATABASE %s SET MULTI_USER: %w",
			quoteName(runner.database), err)
	}

	if _, err := runner.conn.ExecContext(ctx, "ALTER DATABASE "+quoteName(runner.database)+" SET MULTI_USER"); err != nil {
		return fmt.Errorf("la base de datos quedó en modo un solo usuario; ejecuta ALTER DATABASE %s SET MULTI_USER: %w",
			quoteName(runner.database), err)
	}

	runner.emit(Event{Kind: "info", Message: "Base de datos abierta de nuevo a todos los usuarios."})

	return nil
}

// prepareBackup returns the backup to fall back on. After an update that did
// not finish it keeps that backup, which is the state before the update:
// writing a new one would hide it behind a half-updated database.
func (runner *Runner) prepareBackup(ctx context.Context) (path string, isKept bool, err error) {
	previous, err := runner.rollbackBackup(ctx)
	if err != nil {
		return "", false, err
	}

	if previous != "" {
		_, statErr := os.Stat(previous)

		// Only a file that is certainly gone triggers a new backup. If the
		// state is unknown (no permission to look), keep the old one.
		if statErr == nil || !os.IsNotExist(statErr) || runner.isRemote {
			runner.emit(Event{Kind: "info", Message: fmt.Sprintf("Se conserva el backup de un intento anterior que no terminó: %s", previous)})
			return previous, true, nil
		}

		runner.emit(Event{Kind: "warning", Message: fmt.Sprintf("El backup de un intento anterior ya no existe (%s): se crea uno nuevo.", previous)})
	}

	runner.emit(Event{Kind: "info", Message: "Creando backup previo..."})

	path, err = runner.backup(ctx)
	if err != nil {
		return "", false, err
	}

	if err := runner.setRollbackBackup(ctx, path); err != nil {
		return "", false, fmt.Errorf("no se pudo marcar el backup como punto de retorno: %w", err)
	}

	return path, false, nil
}

func (runner *Runner) rollbackBackup(ctx context.Context) (string, error) {
	var value sql.NullString
	err := runner.conn.QueryRowContext(ctx,
		`SELECT CAST(value AS nvarchar(4000)) FROM sys.extended_properties WHERE class = 0 AND name = N'`+rollbackProperty+`'`).Scan(&value)

	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	return value.String, err
}

func (runner *Runner) setRollbackBackup(ctx context.Context, path string) error {
	_, err := runner.conn.ExecContext(ctx,
		`IF EXISTS (SELECT 1 FROM sys.extended_properties WHERE class = 0 AND name = N'`+rollbackProperty+`')
			EXEC sp_updateextendedproperty @name = N'`+rollbackProperty+`', @value = @p1
		ELSE
			EXEC sp_addextendedproperty @name = N'`+rollbackProperty+`', @value = @p1`, path)

	return err
}

func (runner *Runner) clearRollbackBackup(ctx context.Context) error {
	_, err := runner.conn.ExecContext(ctx,
		`IF EXISTS (SELECT 1 FROM sys.extended_properties WHERE class = 0 AND name = N'`+rollbackProperty+`')
			EXEC sp_dropextendedproperty @name = N'`+rollbackProperty+`'`)

	return err
}

// backup writes <backupDir>\<database>_preupdate.bak with INIT, so only one
// file per database ever remains. It runs outside any transaction.
func (runner *Runner) backup(ctx context.Context) (string, error) {
	dir := runner.backupDir

	if !runner.isRemote {
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}

		dir = absolute

		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("no se pudo crear la carpeta de backups %q: %w", dir, err)
		}
	}

	path := filepath.Join(dir, safeFileName(runner.database)+"_preupdate.bak")
	statement := "BACKUP DATABASE " + quoteName(runner.database) + " TO DISK = @p1 WITH INIT, CHECKSUM"

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

	// No checksum: the script was never run here, so its content says nothing
	// about this database and fixing the baseline later must not block it.
	_, err = runner.conn.ExecContext(ctx,
		`INSERT INTO dbo.SchemaVersions (Version, ScriptName, Checksum, DurationMs) VALUES (@p1, @p2, NULL, 0)`,
		baseline.Version, baseline.Name)
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
		if err == nil {
			return
		}

		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = fmt.Errorf("%w (rollback falló: %v)", err, rollbackErr)
		}

		// A USE inside the script outlives the rollback.
		if pinErr := runner.pinDatabase(context.Background()); pinErr != nil {
			err = fmt.Errorf("%w (no se pudo volver a la base de datos %q: %v)", err, runner.database, pinErr)
		}
	}()

	depth := 1

	for index, text := range batch.Split(script.Content, "GO") {
		if strings.TrimSpace(text) == "" {
			continue
		}

		if _, err = tx.ExecContext(ctx, text); err != nil {
			return fmt.Errorf("lote %d: %w", index+1, err)
		}

		// Checked after every batch, so the next one never runs elsewhere
		// nor outside the transaction.
		var current string
		if err = tx.QueryRowContext(ctx, `SELECT DB_NAME(), @@TRANCOUNT`).Scan(&current, &depth); err != nil {
			return err
		}

		if current != runner.database {
			err = fmt.Errorf("el lote %d cambia la base de datos activa a %q (USE): los scripts deben actuar solo sobre %q", index+1, current, runner.database)
			return err
		}

		if depth < 1 {
			err = fmt.Errorf("el lote %d cerró la transacción (COMMIT o ROLLBACK sin su BEGIN): el script no es atómico y lo ejecutado antes de ese lote puede haber quedado confirmado; "+
				"revisa la base de datos o restaura el backup", index+1)
			return err
		}
	}

	if depth != 1 {
		err = errors.New("el script dejó una transacción abierta (BEGIN TRANSACTION sin su COMMIT)")
		return err
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
