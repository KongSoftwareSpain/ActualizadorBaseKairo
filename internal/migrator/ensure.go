package migrator

import (
	"context"
	"database/sql"
	"errors"
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

// ensureAccess gives a login (the one of the connection when @p1 is empty) a user in the database
// and the db_owner role when it has none (a login that is not the owner and
// only passes through as sysadmin has no user, and the ERP connecting with
// it would be refused). It returns 1 when it changed something.
const ensureAccess = `
DECLARE @login sysname = ISNULL(NULLIF(@p1, N''), SUSER_SNAME()), @user sysname, @changed int = 0, @sql nvarchar(max);
SELECT @user = name FROM sys.database_principals WHERE sid = SUSER_SID(@login);
IF @user IS NULL
BEGIN
    SET @user = @login;
    SET @sql = N'CREATE USER ' + QUOTENAME(@user) + N' FOR LOGIN ' + QUOTENAME(@login);
    EXEC(@sql);
    SET @changed = 1;
END
IF @user <> N'dbo' AND ISNULL(IS_ROLEMEMBER(N'db_owner', @user), 0) <> 1
BEGIN
    SET @sql = N'ALTER ROLE db_owner ADD MEMBER ' + QUOTENAME(@user);
    EXEC(@sql);
    SET @changed = 1;
END
SELECT @changed`

// EnsureAccess makes sure the login has a user with db_owner in the database
// of the connection. An empty login means the one of the connection. changed
// is false when it already had both.
func (runner *Runner) EnsureAccess(ctx context.Context, login string) (changed bool, err error) {
	if err := runner.pinDatabase(ctx); err != nil {
		return false, err
	}

	var flag int
	if err := runner.conn.QueryRowContext(ctx, ensureAccess, login).Scan(&flag); err != nil {
		return false, err
	}

	return flag == 1, nil
}

// EnsureLogin creates the server login with that password when it does not
// exist (CHECK_POLICY off, like the fixed password of Kairo needs). An
// existing login is never touched. The creating login needs sysadmin or
// securityadmin.
func EnsureLogin(params ConnectionParams, login, password string) (isCreated bool, err error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return false, errors.New("falta el nombre del login")
	}

	db, err := openMaster(params)
	if err != nil {
		return false, err
	}
	defer db.Close()

	ctx := context.Background()

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.server_principals WHERE name = @p1`, login).Scan(&count); err != nil {
		return false, err
	}

	if count > 0 {
		return false, nil
	}

	if password == "" {
		return false, fmt.Errorf("el login %q no existe y esta compilación no incluye la contraseña de Kairo para crearlo", login)
	}

	// CREATE LOGIN does not take parameters: the password goes in as a literal.
	statement := "CREATE LOGIN " + quoteName(login) + " WITH PASSWORD = N'" + strings.ReplaceAll(password, "'", "''") + "', CHECK_POLICY = OFF"

	if _, err := db.ExecContext(ctx, statement); err != nil {
		return false, fmt.Errorf("no se pudo crear el login %q: %w (el login que importa necesita ser sysadmin o securityadmin)", login, err)
	}

	return true, nil
}
