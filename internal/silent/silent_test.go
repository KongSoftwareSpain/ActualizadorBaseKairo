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
