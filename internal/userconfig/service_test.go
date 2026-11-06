package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "sub", "config.json")

	return &Service{path: path}, path
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	service, _ := newTestService(t)

	config, err := service.LoadFromFile()
	if err != nil {
		t.Fatal(err)
	}

	if config != NewConfig() {
		t.Fatalf("config: %+v", config)
	}
}

func TestSaveThenLoad(t *testing.T) {
	service, path := newTestService(t)

	saved := NewConfig()
	saved.IsDark = true
	saved.Database = "Kairo"
	saved.Username = "sa"

	if err := service.SaveToFile(saved); err != nil {
		t.Fatal(err)
	}

	loaded, err := service.LoadFromFile()
	if err != nil {
		t.Fatal(err)
	}

	if loaded != saved {
		t.Fatalf("config: %+v, esperado %+v", loaded, saved)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("quedó el fichero temporal")
	}
}

func TestSaveOverwritesExistingFile(t *testing.T) {
	service, _ := newTestService(t)

	for _, database := range []string{"Uno", "Dos"} {
		config := NewConfig()
		config.Database = database

		if err := service.SaveToFile(config); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := service.LoadFromFile()
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Database != "Dos" {
		t.Fatalf("base de datos: %q", loaded.Database)
	}
}

func TestLoadCorruptFileGivesDefaultsAndKeepsCopy(t *testing.T) {
	service, path := newTestService(t)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(`{"isDark": tru`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := service.LoadFromFile()
	if err != nil {
		t.Fatalf("un config corrupto no debe impedir arrancar: %v", err)
	}

	if config != NewConfig() {
		t.Fatalf("config: %+v", config)
	}

	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("falta la copia del fichero dañado: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("el fichero dañado debe apartarse")
	}
}

func TestLoadOldFileKeepsDefaultsForNewFields(t *testing.T) {
	service, path := newTestService(t)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(`{"isDark": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := service.LoadFromFile()
	if err != nil {
		t.Fatal(err)
	}

	if !config.IsDark || config.Server != NewConfig().Server || config.ScriptsPath != "./Scripts" {
		t.Fatalf("config: %+v", config)
	}
}
