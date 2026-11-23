package silent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"actualizador/internal/migrator"
)

type fakeUpdater struct {
	status        migrator.Status
	connectError  error
	applyError    error
	connectParams migrator.ConnectionParams
	applyOptions  migrator.ApplyOptions
	isApplied     bool
	isDisconnect  bool
}

func (updater *fakeUpdater) Connect(params migrator.ConnectionParams) (migrator.Status, error) {
	updater.connectParams = params
	return updater.status, updater.connectError
}

func (updater *fakeUpdater) Apply(options migrator.ApplyOptions) (migrator.ApplyResult, error) {
	updater.isApplied = true
	updater.applyOptions = options

	return migrator.ApplyResult{}, updater.applyError
}

func (updater *fakeUpdater) Disconnect() { updater.isDisconnect = true }

func writeIni(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestParseArgs(t *testing.T) {
	options := ParseArgs([]string{"--SILENT", "--ini", `C:\Kairo\config.ini`, "--otro"})
	if !options.IsEnabled || options.IniPath != `C:\Kairo\config.ini` || options.Username != "kong" {
		t.Fatalf("opciones inesperadas: %+v", options)
	}

	options = ParseArgs([]string{"--silent", "--user", "otro", "--ini"})
	if options.Username != "otro" || options.IniPath != "" {
		t.Fatalf("opciones inesperadas: %+v", options)
	}

	if ParseArgs(nil).IsEnabled {
		t.Fatal("sin --silent no debe activarse")
	}
}

func TestReadConnection(t *testing.T) {
	path := writeIni(t, "\xEF\xBB\xBF; comentario\r\n[Otra]\r\nServidor=no\r\n[BASEDATOS]\r\nservidor = srv\\SQLEXPRESS \r\nBaseDatos=Kairo_Empresa\r\nTimeout=15\r\n")

	connection, err := ReadConnection(path)
	if err != nil {
		t.Fatal(err)
	}

	if connection.Server != `srv\SQLEXPRESS` || connection.Database != "Kairo_Empresa" {
		t.Fatalf("conexión inesperada: %+v", connection)
	}
}

func TestReadConnectionErrors(t *testing.T) {
	if _, err := ReadConnection(""); err == nil {
		t.Fatal("sin ruta debe fallar")
	}

	if _, err := ReadConnection(filepath.Join(t.TempDir(), "no_existe.ini")); err == nil {
		t.Fatal("fichero inexistente debe fallar")
	}

	if _, err := ReadConnection(writeIni(t, "[BaseDatos]\nServidor=srv\n")); err == nil {
		t.Fatal("sin BaseDatos debe fallar")
	}
}

func TestRunAppliesWithoutKickingSessions(t *testing.T) {
	updater := &fakeUpdater{}
	options := Options{IsEnabled: true, IniPath: writeIni(t, "[BaseDatos]\nServidor=srv\nBaseDatos=K\n"), Username: "kong", Password: "secreto"}

	connection, err := Run(updater, options)
	if err != nil {
		t.Fatal(err)
	}

	if connection.Server != "srv" || !updater.isApplied || !updater.isDisconnect {
		t.Fatalf("no aplicó o no cerró la conexión: %+v", updater)
	}

	if updater.applyOptions.AllowOtherSessions || updater.applyOptions.KickSessions {
		t.Fatal("el modo silencioso nunca debe expulsar ni ignorar otras sesiones")
	}

	if updater.connectParams.Username != "kong" || updater.connectParams.Password != "secreto" || updater.connectParams.Database != "K" {
		t.Fatalf("parámetros de conexión inesperados: %+v", updater.connectParams)
	}
}

func TestRunFallsBackOnEveryFailure(t *testing.T) {
	ini := writeIni(t, "[BaseDatos]\nServidor=srv\nBaseDatos=K\n")
	base := Options{IsEnabled: true, IniPath: ini, Username: "kong", Password: "x"}

	cases := map[string]struct {
		updater *fakeUpdater
		options Options
	}{
		"sin contraseña":     {&fakeUpdater{}, Options{IsEnabled: true, IniPath: ini}},
		"sin ini":            {&fakeUpdater{}, Options{IsEnabled: true, Password: "x"}},
		"conexión rechazada": {&fakeUpdater{connectError: errors.New("login failed")}, base},
		"viene de Access":    {&fakeUpdater{status: migrator.Status{NeedsBaseline: true}}, base},
		"falla un script":    {&fakeUpdater{applyError: errors.New("lote 2")}, base},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			connection, err := Run(testCase.updater, testCase.options)
			if err == nil {
				t.Fatal("debe devolver error para abrir la ventana")
			}

			if name != "sin ini" && name != "sin contraseña" && name != "conexión rechazada" && connection.Server == "" {
				t.Fatal("debe devolver la conexión para rellenar la ventana")
			}

			if name == "viene de Access" && testCase.updater.isApplied {
				t.Fatal("no debe aplicar sobre una BD sin SchemaVersions")
			}
		})
	}
}

func TestRunStopsWhenOtherSessionsOrUnknownWithPendingScripts(t *testing.T) {
	ini := writeIni(t, "[BaseDatos]\nServidor=srv\nBaseDatos=K\n")
	pending := []migrator.Script{{Version: 1, Name: "V0001__a.sql"}}
	base := Options{IsEnabled: true, IniPath: ini, Username: "kong", Password: "x"}

	cases := map[string]struct {
		status  migrator.Status
		options Options
		isOk    bool
	}{
		"hay sesiones abiertas":     {migrator.Status{Pending: pending, CanSeeSessions: true, Sessions: []migrator.Session{{ID: 52, Program: "Kairo.exe"}}}, base, false},
		"no se pueden ver":          {migrator.Status{Pending: pending, CanSeeSessions: false}, base, false},
		"no se pueden ver, forzado": {migrator.Status{Pending: pending, CanSeeSessions: false}, Options{IsEnabled: true, IniPath: ini, Username: "kong", Password: "x", AllowUnknownSessions: true}, true},
		"nadie conectado":           {migrator.Status{Pending: pending, CanSeeSessions: true}, base, true},
		"nada pendiente":            {migrator.Status{CanSeeSessions: false}, base, true},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			updater := &fakeUpdater{status: testCase.status}

			_, err := Run(updater, testCase.options)
			if testCase.isOk && err != nil {
				t.Fatalf("debía aplicar: %v", err)
			}

			if !testCase.isOk {
				if err == nil {
					t.Fatal("debía parar y dejar paso a la ventana")
				}

				if updater.isApplied {
					t.Fatal("no debe aplicar nada")
				}
			}
		})
	}
}

func TestReadConnectionFirstKeyWinsAndQuotesAreRemoved(t *testing.T) {
	path := writeIni(t, "[BaseDatos]\nServidor=\"srv"+`\`+"SQL\"\nBaseDatos=Primera\nBaseDatos=Segunda\n")

	connection, err := ReadConnection(path)
	if err != nil {
		t.Fatal(err)
	}

	if connection.Server != `srv\SQL` || connection.Database != "Primera" {
		t.Fatalf("conexión inesperada: %+v", connection)
	}
}

func TestReadConnectionUTF16BigEndian(t *testing.T) {
	text := "[BaseDatos]\r\nServidor=srv\r\nBaseDatos=K\r\n"
	raw := []byte{0xFE, 0xFF}

	for _, r := range text {
		raw = append(raw, byte(r>>8), byte(r))
	}

	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	connection, err := ReadConnection(path)
	if err != nil || connection.Server != "srv" || connection.Database != "K" {
		t.Fatalf("UTF-16 BE: %+v, %v", connection, err)
	}
}
