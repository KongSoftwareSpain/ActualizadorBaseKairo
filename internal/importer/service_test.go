package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"actualizador/internal/migrator"
)

func TestPlanReadsServerDatabaseAndAlwaysKong(t *testing.T) {
	kairo := writeKairo(t, `localhost\SQLEXPRESS`, "Tecnicos", "Usuario=otro\r\n")
	sqlPath := writeSQL(t, "SELECT 1\nGO\n")

	planned := newService(t).Plan([]Job{{ID: "a", KairoPath: kairo, SqlPath: sqlPath}})

	if len(planned) != 1 || planned[0].Error != "" {
		t.Fatalf("planned = %+v", planned)
	}

	got := planned[0]
	if got.ID != "a" || got.Server != `localhost\SQLEXPRESS` || got.Database != "Tecnicos" || got.Username != "kong" {
		t.Fatalf("got %+v", got)
	}

	if got.SizeBytes != int64(len("SELECT 1\nGO\n")) {
		t.Fatalf("SizeBytes = %d", got.SizeBytes)
	}
}

func TestPlanAcceptsConfigIniPathAndFolder(t *testing.T) {
	kairo := writeKairo(t, "localhost", "Tecnicos", "")
	sqlPath := writeSQL(t, "SELECT 1\nGO\n")

	planned := newService(t).Plan([]Job{
		{ID: "folder", KairoPath: kairo, SqlPath: sqlPath},
		{ID: "ini", KairoPath: filepath.Join(kairo, "config.ini"), SqlPath: sqlPath},
	})

	if planned[0].Error != "" {
		t.Fatalf("folder: %s", planned[0].Error)
	}

	// Same database through the ini path: the duplicate rule applies, not a read error.
	if !strings.Contains(planned[1].Error, "misma base de datos") {
		t.Fatalf("ini: %q", planned[1].Error)
	}
}

func TestPlanRejectsRemoteServer(t *testing.T) {
	kairo := writeKairo(t, `10.0.0.5\SQL`, "Tecnicos", "")

	planned := newService(t).Plan([]Job{{ID: "a", KairoPath: kairo, SqlPath: writeSQL(t, "SELECT 1\nGO\n")}})

	if !strings.Contains(planned[0].Error, "no es local") {
		t.Fatalf("Error = %q", planned[0].Error)
	}
}

func TestPlanRejectsMissingIniAndMissingSection(t *testing.T) {
	sqlPath := writeSQL(t, "SELECT 1\nGO\n")
	noIni := t.TempDir()

	noSection := t.TempDir()
	writeFile(t, filepath.Join(noSection, "config.ini"), "[Otra]\r\nX=1\r\n")

	planned := newService(t).Plan([]Job{
		{ID: "a", KairoPath: noIni, SqlPath: sqlPath},
		{ID: "b", KairoPath: noSection, SqlPath: sqlPath},
		{ID: "c", KairoPath: "", SqlPath: sqlPath},
	})

	if !strings.Contains(planned[0].Error, "config.ini") {
		t.Fatalf("a: %q", planned[0].Error)
	}

	if !strings.Contains(planned[1].Error, "BaseDatos") {
		t.Fatalf("b: %q", planned[1].Error)
	}

	if planned[2].Error == "" {
		t.Fatal("c: una carpeta vacía debe dar error")
	}
}

func TestPlanRejectsMissingSqlFile(t *testing.T) {
	kairo := writeKairo(t, "localhost", "Tecnicos", "")

	planned := newService(t).Plan([]Job{
		{ID: "a", KairoPath: kairo, SqlPath: filepath.Join(t.TempDir(), "no-existe.sql")},
		{ID: "b", KairoPath: kairo, SqlPath: ""},
	})

	if !strings.Contains(planned[0].Error, ".sql") || planned[1].Error == "" {
		t.Fatalf("errors: %q, %q", planned[0].Error, planned[1].Error)
	}
}

func TestPlanRejectsDuplicateDatabase(t *testing.T) {
	sqlPath := writeSQL(t, "SELECT 1\nGO\n")

	planned := newService(t).Plan([]Job{
		{ID: "a", KairoPath: writeKairo(t, `.\SQLEXPRESS`, "Tecnicos", ""), SqlPath: sqlPath},
		{ID: "b", KairoPath: writeKairo(t, `localhost\SQLEXPRESS`, "tecnicos", ""), SqlPath: sqlPath},
		{ID: "c", KairoPath: writeKairo(t, `(local)\SQLEXPRESS`, "Otra", ""), SqlPath: sqlPath},
	})

	if planned[0].Error != "" {
		t.Fatalf("la primera no debe fallar: %q", planned[0].Error)
	}

	if !strings.Contains(planned[1].Error, "misma base de datos que la fila 1") {
		t.Fatalf("b: %q", planned[1].Error)
	}

	if planned[2].Error != "" {
		t.Fatalf("c: %q", planned[2].Error)
	}
}

func TestRunRefusesEmptyPassword(t *testing.T) {
	service := NewService(t.TempDir(), "", "", nil)

	_, err := service.Run(nil, migratorOptions())
	if err == nil || !strings.Contains(err.Error(), "contraseña de kong") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDoesNotTouchDatabaseWhenScriptIsEmpty(t *testing.T) {
	name, master := createDatabase(t)
	execOn(t, master, name, "CREATE TABLE dbo.Viejo (A int)")

	kairo := writeKairo(t, testServer(), name, "")

	results, err := windowsAuthService(t).Run([]Job{{ID: "a", KairoPath: kairo, SqlPath: writeSQL(t, "\nGO\n")}}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if results[0].IsDone || !strings.Contains(results[0].Error, "no contiene sentencias") {
		t.Fatalf("result = %+v", results[0])
	}

	if !tableExists(t, master, name, "Viejo") {
		t.Fatal("un script vacío no debe limpiar la base de datos")
	}
}

func TestRunFailedJobDoesNotStopTheNext(t *testing.T) {
	nameBad, _ := createDatabase(t)
	nameGood, master := createDatabase(t)
	execOn(t, master, nameGood, "CREATE TABLE dbo.Viejo (A int)")

	good := "SET ANSI_NULLS ON\nGO\nCREATE TABLE dbo.Nueva (A int)\nGO\n" +
		"INSERT dbo.Nueva VALUES (1)\nGO\n" +
		"CREATE USER [UsuarioQueNoExiste] FOR LOGIN [UsuarioQueNoExiste]\nGO\n"

	var events []Event
	service := windowsAuthService(t)
	service.emit = func(event Event) { events = append(events, event) }

	results, err := service.Run([]Job{
		{ID: "bad", KairoPath: writeKairo(t, testServer(), nameBad, ""), SqlPath: writeSQL(t, "CREATE TABLE dbo.T (\nGO\nSELECT 1\nGO\n")},
		{ID: "good", KairoPath: writeKairo(t, testServer(), nameGood, ""), SqlPath: writeSQL(t, good)},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if results[0].IsDone || !strings.Contains(results[0].Error, "lote 1") {
		t.Fatalf("bad = %+v", results[0])
	}

	if !results[1].IsDone || results[1].Error != "" {
		t.Fatalf("good = %+v", results[1])
	}

	if len(results[1].Warnings) != 1 || !strings.Contains(results[1].Warnings[0], "UsuarioQueNoExiste") {
		t.Fatalf("warnings = %q", results[1].Warnings)
	}

	if tableExists(t, master, nameGood, "Viejo") || !tableExists(t, master, nameGood, "Nueva") {
		t.Fatal("la base de datos debe quedar limpia y con la tabla nueva")
	}

	var kinds []string
	for _, event := range events {
		if event.JobID == "good" {
			kinds = append(kinds, event.Kind)
		}
	}

	if len(kinds) == 0 || kinds[0] != "job-start" || kinds[len(kinds)-1] != "job-done" {
		t.Fatalf("eventos de good: %v", kinds)
	}
}

func TestRunRealScript(t *testing.T) {
	const path = `C:\Users\MIGUEL\Desktop\Tecnicos\script.sql`

	if _, err := os.Stat(path); err != nil {
		t.Skip("no está el script real")
	}

	name, master := createDatabase(t)

	results, err := windowsAuthService(t).Run([]Job{
		{ID: "real", KairoPath: writeKairo(t, testServer(), name, ""), SqlPath: path},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone {
		t.Fatalf("result = %+v", results[0])
	}

	var tables int
	if err := master.QueryRow("SELECT COUNT(*) FROM [" + name + "].sys.tables").Scan(&tables); err != nil {
		t.Fatal(err)
	}

	if tables < 364 {
		t.Fatalf("tablas: %d", tables)
	}

	var articles int
	if err := master.QueryRow("SELECT COUNT(*) FROM [" + name + "].dbo.Articulos").Scan(&articles); err != nil || articles == 0 {
		t.Fatalf("Articulos: %d, %v", articles, err)
	}
}

func TestRunKicksOtherSessionsOnlyWhenAsked(t *testing.T) {
	name, master := createDatabase(t)

	other, err := master.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	if _, err := other.ExecContext(context.Background(), "USE ["+name+"]"); err != nil {
		t.Fatal(err)
	}

	jobs := []Job{{ID: "a", KairoPath: writeKairo(t, testServer(), name, ""), SqlPath: writeSQL(t, "CREATE TABLE dbo.T (A int)\nGO\n")}}
	service := windowsAuthService(t)

	results, err := service.Run(jobs, migrator.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if results[0].IsDone || !strings.Contains(results[0].Error, "sesión") {
		t.Fatalf("sin expulsar debe negarse por las sesiones abiertas: %+v", results[0])
	}

	results, err = service.Run(jobs, migrator.ApplyOptions{KickSessions: true})
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone || !tableExists(t, master, name, "T") {
		t.Fatalf("expulsando debe importar: %+v", results[0])
	}
}

// missingDatabase returns a name that does not exist and drops it when the test ends.
func missingDatabase(t *testing.T) string {
	t.Helper()

	master := openMaster(t)
	name := fmt.Sprintf("ImporterNew_%d", time.Now().UnixNano())

	t.Cleanup(func() {
		master.Exec("ALTER DATABASE [" + name + "] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		master.Exec("DROP DATABASE [" + name + "]")
		master.Close()
	})

	return name
}

func TestPlanMarksDatabaseToCreate(t *testing.T) {
	existing, _ := createDatabase(t)
	missing := missingDatabase(t)
	sqlPath := writeSQL(t, "SELECT 1\nGO\n")

	planned := windowsAuthService(t).Plan([]Job{
		{ID: "old", KairoPath: writeKairo(t, testServer(), existing, ""), SqlPath: sqlPath},
		{ID: "new", KairoPath: writeKairo(t, testServer(), missing, ""), SqlPath: sqlPath},
	})

	if planned[0].Error != "" || planned[0].IsNew {
		t.Fatalf("existente: %+v", planned[0])
	}

	if planned[1].Error != "" || !planned[1].IsNew {
		t.Fatalf("inexistente: %+v", planned[1])
	}
}

func TestRunCreatesMissingDatabase(t *testing.T) {
	missing := missingDatabase(t)
	master := openMaster(t)
	defer master.Close()

	results, err := windowsAuthService(t).Run([]Job{
		{ID: "new", KairoPath: writeKairo(t, testServer(), missing, ""), SqlPath: writeSQL(t, "CREATE TABLE dbo.Nueva (A int)\nGO\nINSERT dbo.Nueva VALUES (1)\nGO\n")},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone || results[0].Error != "" {
		t.Fatalf("result = %+v", results[0])
	}

	if !tableExists(t, master, missing, "Nueva") {
		t.Fatal("la base de datos debe crearse y contener la tabla del script")
	}
}

func TestRunDoesNotCreateDatabaseWhenScriptIsEmpty(t *testing.T) {
	missing := missingDatabase(t)
	master := openMaster(t)
	defer master.Close()

	results, err := windowsAuthService(t).Run([]Job{
		{ID: "new", KairoPath: writeKairo(t, testServer(), missing, ""), SqlPath: writeSQL(t, "\nGO\n")},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if results[0].IsDone {
		t.Fatalf("result = %+v", results[0])
	}

	var id *int
	if err := master.QueryRow("SELECT DB_ID(@p1)", missing).Scan(&id); err != nil || id != nil {
		t.Fatalf("un script vacío no debe crear la base de datos (id=%v, err=%v)", id, err)
	}
}

func TestRunGivesOwnLoginAccessToTheDatabase(t *testing.T) {
	name, master := createDatabase(t)

	// Another owner, so the login that imports has no user of its own.
	if _, err := master.Exec("ALTER AUTHORIZATION ON DATABASE::[" + name + "] TO sa"); err != nil {
		t.Fatal(err)
	}

	results, err := windowsAuthService(t).Run([]Job{
		{ID: "a", KairoPath: writeKairo(t, testServer(), name, ""), SqlPath: writeSQL(t, "CREATE TABLE dbo.T (A int)\nGO\n")},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone {
		t.Fatalf("result = %+v", results[0])
	}

	var users int
	if err := master.QueryRow(`SELECT COUNT(*) FROM [` + name + `].sys.database_principals WHERE sid = SUSER_SID()`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("el login debe tener un usuario en la BD: users=%d err=%v", users, err)
	}
}

func TestPlanReadsAppUser(t *testing.T) {
	sqlPath := writeSQL(t, "SELECT 1\nGO\n")

	planned := newService(t).Plan([]Job{
		{ID: "a", KairoPath: writeKairo(t, "localhost", "UnaBd", "Usuario=viasa\r\n"), SqlPath: sqlPath},
		{ID: "b", KairoPath: writeKairo(t, "localhost", "OtraBd", ""), SqlPath: sqlPath},
	})

	if planned[0].AppUser != "viasa" || planned[1].AppUser != "kairo" {
		t.Fatalf("AppUser: %q, %q", planned[0].AppUser, planned[1].AppUser)
	}
}

func TestRunCreatesLoginFromIniBeforeTheScript(t *testing.T) {
	name, master := createDatabase(t)
	login := newLoginName(t, master)

	script := "CREATE TABLE dbo.T (A int)\nGO\n" +
		"CREATE USER [" + login + "] FOR LOGIN [" + login + "]\nGO\n" +
		"ALTER ROLE [db_datareader] ADD MEMBER [" + login + "]\nGO\n"

	results, err := windowsAuthService(t).Run([]Job{
		{ID: "a", KairoPath: writeKairo(t, testServer(), name, "Usuario="+login+"\r\n"), SqlPath: writeSQL(t, script)},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone || len(results[0].Warnings) != 0 {
		t.Fatalf("el CREATE USER del script debe funcionar sin avisos: %+v", results[0])
	}

	if !loginExists(t, master, login) || !userExistsInDatabase(t, master, name, login) {
		t.Fatal("deben existir el login y su usuario en la BD")
	}
}

func TestRunGivesAppLoginAccessWhenScriptHasNoUserForIt(t *testing.T) {
	name, master := createDatabase(t)
	login := newLoginName(t, master)

	results, err := windowsAuthService(t).Run([]Job{
		{ID: "a", KairoPath: writeKairo(t, testServer(), name, "Usuario="+login+"\r\n"), SqlPath: writeSQL(t, "CREATE TABLE dbo.T (A int)\nGO\n")},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone || !userExistsInDatabase(t, master, name, login) {
		t.Fatalf("el login de la app debe quedar con usuario en la BD: %+v", results[0])
	}
}

func TestRunWithoutAppPasswordWarnsAndStillImports(t *testing.T) {
	name, master := createDatabase(t)
	login := newLoginName(t, master)

	service := windowsAuthService(t)
	service.appPassword = ""

	results, err := service.Run([]Job{
		{ID: "a", KairoPath: writeKairo(t, testServer(), name, "Usuario="+login+"\r\n"), SqlPath: writeSQL(t, "CREATE TABLE dbo.T (A int)\nGO\n")},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone || len(results[0].Warnings) == 0 || !strings.Contains(strings.Join(results[0].Warnings, "\n"), "contraseña de Kairo") {
		t.Fatalf("result = %+v", results[0])
	}

	if loginExists(t, master, login) {
		t.Fatal("sin contraseña no se debe crear el login")
	}
}

func TestRunDoesNotCreateWindowsAppLogin(t *testing.T) {
	name, _ := createDatabase(t)

	results, err := windowsAuthService(t).Run([]Job{
		{ID: "a", KairoPath: writeKairo(t, testServer(), name, `Usuario=BUILTIN\Usuarios`+"\r\n"), SqlPath: writeSQL(t, "CREATE TABLE dbo.T (A int)\nGO\n")},
	}, migratorOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !results[0].IsDone || len(results[0].Warnings) != 0 {
		t.Fatalf("un login de Windows no se crea ni avisa: %+v", results[0])
	}
}
