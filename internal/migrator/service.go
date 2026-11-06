package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

// EventName is the Wails event that carries progress to the UI.
const EventName = "migrator:event"

const loginFailedErrorNumber = 18456

// ConnectionParams are the connection fields typed in the UI. The password
// lives only in memory: it is never written to disk.
type ConnectionParams struct {
	Server        string `json:"server"`
	Database      string `json:"database"`
	IsWindowsAuth bool   `json:"isWindowsAuth"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	// BackupDir is optional: empty means Backups next to the executable.
	BackupDir string `json:"backupDir"`
}

// ApplyResult is the outcome of Apply. BackupPath is empty when nothing was
// pending, so no backup was made.
type ApplyResult struct {
	Status     Status `json:"status"`
	BackupPath string `json:"backupPath"`
}

// Service is the Wails-bound entry point: connect, read status, apply.
type Service struct {
	baseDir string
	emit    func(Event)

	mutex     sync.Mutex
	db        *sql.DB
	runner    *Runner
	isRunning bool
}

// NewService expects scripts in <baseDir>\Scripts and writes logs to
// <baseDir>\Logs. emit forwards events to the UI and may be nil.
func NewService(baseDir string, emit func(Event)) *Service {
	return &Service{baseDir: baseDir, emit: emit}
}

// DefaultBaseDir is the folder that holds the executable.
func DefaultBaseDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}

	return filepath.Dir(exe), nil
}

// Connect opens the database and returns its status. Any previous
// connection is closed first.
func (service *Service) Connect(params ConnectionParams) (Status, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()

	if service.isRunning {
		return Status{}, errors.New("hay una actualización en curso")
	}

	service.closeLocked()

	scripts, err := LoadScripts(filepath.Join(service.baseDir, "Scripts"))
	if err != nil {
		return Status{}, err
	}

	dsn, err := buildDSN(params)
	if err != nil {
		return Status{}, err
	}

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return Status{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return Status{}, describeConnectError(params, err)
	}

	service.db = db
	service.runner = NewRunner(conn, scripts, service.resolveBackupDir(params.BackupDir), service.publish)

	status, err := service.runner.Status(context.Background())
	if err != nil {
		service.closeLocked()
		return Status{}, err
	}

	service.publish(Event{Kind: "info", Message: fmt.Sprintf("Conectado a %s / %s. Versión actual: V%04d, pendientes: %d.",
		params.Server, params.Database, status.CurrentVersion, len(status.Pending))})

	return status, nil
}

// Status re-reads the state of the connected database.
func (service *Service) Status() (Status, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()

	if service.runner == nil {
		return Status{}, errNotConnected
	}

	return service.runner.Status(context.Background())
}

// Apply backs up and runs every pending script. It blocks until done, with
// no timeout: data migrations can take minutes.
func (service *Service) Apply() (ApplyResult, error) {
	runner, release, err := service.acquireRunner()
	if err != nil {
		return ApplyResult{}, err
	}
	defer release()

	ctx := context.Background()

	backupPath, err := runner.Apply(ctx)
	if err != nil {
		return ApplyResult{BackupPath: backupPath}, err
	}

	status, err := runner.Status(ctx)

	return ApplyResult{Status: status, BackupPath: backupPath}, err
}

// RegisterBaseline marks V0001 as applied on a database migrated from Access.
func (service *Service) RegisterBaseline() (Status, error) {
	runner, release, err := service.acquireRunner()
	if err != nil {
		return Status{}, err
	}
	defer release()

	ctx := context.Background()

	if err := runner.RegisterBaseline(ctx); err != nil {
		return Status{}, err
	}

	return runner.Status(ctx)
}

// acquireRunner marks an operation as running, so Connect, Disconnect and a
// second operation cannot interfere. Call release when done.
func (service *Service) acquireRunner() (*Runner, func(), error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()

	if service.runner == nil {
		return nil, nil, errNotConnected
	}
	if service.isRunning {
		return nil, nil, errors.New("hay una actualización en curso")
	}

	service.isRunning = true

	release := func() {
		service.mutex.Lock()
		service.isRunning = false
		service.mutex.Unlock()
	}

	return service.runner, release, nil
}

func (service *Service) resolveBackupDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "Backups"
	}

	if filepath.IsAbs(dir) {
		return dir
	}

	return filepath.Join(service.baseDir, dir)
}

// Disconnect closes the connection.
func (service *Service) Disconnect() {
	service.mutex.Lock()
	defer service.mutex.Unlock()

	if service.isRunning {
		return
	}

	service.closeLocked()
}

// ServiceShutdown is called by Wails when the app exits.
func (service *Service) ServiceShutdown() error {
	service.Disconnect()
	return nil
}

var errNotConnected = errors.New("no hay conexión: pulsa Conectar")

func (service *Service) closeLocked() {
	if service.runner != nil {
		service.runner.Close()
		service.runner = nil
	}

	if service.db != nil {
		service.db.Close()
		service.db = nil
	}
}

// publish writes the event to the daily log file and forwards it to the UI.
func (service *Service) publish(event Event) {
	service.writeLog(event)

	if service.emit != nil {
		service.emit(event)
	}
}

func (service *Service) writeLog(event Event) {
	dir := filepath.Join(service.baseDir, "Logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	now := time.Now()
	file, err := os.OpenFile(filepath.Join(dir, now.Format("2006-01-02")+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()

	fmt.Fprintf(file, "%s [%s] %s\n", now.Format("15:04:05"), event.Kind, event.Message)
}

func buildDSN(params ConnectionParams) (string, error) {
	server := strings.TrimSpace(params.Server)
	database := strings.TrimSpace(params.Database)

	if server == "" {
		return "", errors.New("falta el servidor")
	}
	if database == "" {
		return "", errors.New("falta la base de datos")
	}

	host, instance, _ := strings.Cut(server, `\`)

	query := url.Values{}
	query.Set("database", database)
	query.Set("connection timeout", "15")
	query.Set("TrustServerCertificate", "true")

	dsn := url.URL{Scheme: "sqlserver", Host: host}
	if instance != "" {
		dsn.Path = "/" + instance
	}

	if params.IsWindowsAuth {
		query.Set("trusted_connection", "yes")
	} else {
		if params.Username == "" {
			return "", errors.New("falta el usuario de SQL")
		}
		dsn.User = url.UserPassword(params.Username, params.Password)
	}

	dsn.RawQuery = query.Encode()

	return dsn.String(), nil
}

func describeConnectError(params ConnectionParams, err error) error {
	var sqlErr mssql.Error
	if errors.As(err, &sqlErr) && sqlErr.Number == loginFailedErrorNumber {
		return fmt.Errorf("credenciales rechazadas o sin acceso a la base de datos %q: %w", params.Database, err)
	}

	return fmt.Errorf("no se pudo conectar a %q: %w. Revisa que el servicio SQL Server esté iniciado, "+
		"que el protocolo TCP/IP esté habilitado en el Administrador de configuración de SQL Server "+
		"y, si es una instancia con nombre, que el servicio SQL Server Browser esté en marcha",
		params.Server, err)
}
