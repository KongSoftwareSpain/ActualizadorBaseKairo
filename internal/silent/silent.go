// Package silent runs the update without a window, for the installer: it reads
// the server and database from the Kairo config.ini, connects with the
// installer login and applies the pending scripts. Any problem returns an
// error so that the caller can open the normal window for a technician.
package silent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"actualizador/internal/migrator"
	"actualizador/internal/userconfig"
)

const defaultUsername = "kong"

// Options come from the command line (--silent --ini <path> [--user <name>]).
// Password is never a command-line argument: the caller injects it.
type Options struct {
	IsEnabled bool
	IniPath   string
	Username  string
	Password  string
}

// Connection is the non-secret part of the connection, read from the ini.
type Connection struct {
	Server   string
	Database string
}

// Updater is the part of migrator.Service that Run needs (a fake in tests).
type Updater interface {
	Connect(params migrator.ConnectionParams) (migrator.Status, error)
	Apply(options migrator.ApplyOptions) (migrator.ApplyResult, error)
	Disconnect()
}

var errNeedsBaseline = errors.New("la BD tiene tablas pero no SchemaVersions (viene de Access): un técnico debe registrar la versión base desde la ventana")

// ParseArgs reads --silent, --ini <path> and --user <name>. Unknown arguments
// are ignored so the Wails/WebView2 runtime can add its own.
func ParseArgs(args []string) Options {
	options := Options{Username: defaultUsername}

	for index := 0; index < len(args); index++ {
		switch strings.ToLower(args[index]) {
		case "--silent":
			options.IsEnabled = true
		case "--ini":
			if index+1 < len(args) {
				index++
				options.IniPath = args[index]
			}
		case "--user":
			if index+1 < len(args) {
				index++
				options.Username = args[index]
			}
		}
	}

	return options
}

// Run applies the pending scripts without opening a window. It never asks to
// close other sessions: if the ERP is open the update stops and a technician
// decides. The connection is always closed on return, so that the window that
// may open next can take the database lock.
func Run(updater Updater, options Options) (Connection, error) {
	connection, err := ReadConnection(options.IniPath)
	if err != nil {
		return connection, err
	}

	if options.Password == "" {
		return connection, errors.New("este ejecutable no lleva la contraseña del modo silencioso")
	}

	defer updater.Disconnect()

	status, err := updater.Connect(migrator.ConnectionParams{
		Server:   connection.Server,
		Database: connection.Database,
		Username: options.Username,
		Password: options.Password,
	})
	if err != nil {
		return connection, err
	}

	if status.NeedsBaseline {
		return connection, errNeedsBaseline
	}

	if _, err := updater.Apply(migrator.ApplyOptions{}); err != nil {
		return connection, err
	}

	return connection, nil
}

// Prefill saves the server and database in the user settings, so that the
// window that opens after a failed silent run shows them already filled in.
func Prefill(service *userconfig.Service, connection Connection) error {
	if connection.Server == "" && connection.Database == "" {
		return nil
	}

	config, err := service.LoadFromFile()
	if err != nil {
		return err
	}

	if connection.Server != "" {
		config.Server = connection.Server
	}

	if connection.Database != "" {
		config.Database = connection.Database
	}

	return service.SaveToFile(config)
}

// LogFailure appends why the silent run did not finish to <baseDir>\Logs\silent.log.
func LogFailure(baseDir string, failure error) error {
	dir := filepath.Join(baseDir, "Logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(filepath.Join(dir, "silent.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "%s modo silencioso no completado: %v\n", time.Now().Format("2006-01-02 15:04:05"), failure)

	return err
}
