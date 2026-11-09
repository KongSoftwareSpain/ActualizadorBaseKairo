// Package userconfig loads and saves the user settings as JSON.
package userconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config holds the settings remembered between sessions. Never add
// passwords or other secrets: the file is plain JSON.
type Config struct {
	IsDark   bool   `json:"isDark"`
	Server   string `json:"server"`
	Database string `json:"database"`
	Username string `json:"username"`
	// ScriptsPath and BackupDir accept relative paths, resolved against the
	// folder of the executable. An empty BackupDir means the backup folder of
	// the SQL Server instance.
	ScriptsPath string `json:"scriptsPath"`
	BackupDir   string `json:"backupDir"`
}

func NewConfig() Config {
	return Config{
		IsDark:      false,
		Server:      `localhost\SQLEXPRESS`,
		ScriptsPath: "./Scripts",
		BackupDir:   "",
	}
}

type Service struct {
	mutex sync.RWMutex

	// path overrides the default file location (used by tests).
	path string
}

// LoadFromFile returns the saved settings. A missing file gives the
// defaults. A corrupt one is set aside as config.json.corrupt and also gives
// the defaults: a damaged settings file must never keep the app from opening.
func (service *Service) LoadFromFile() (Config, error) {
	config := NewConfig()

	path, err := service.getJsonPath()
	if err != nil {
		return config, err
	}

	service.mutex.Lock()
	defer service.mutex.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}

		return config, err
	}

	loaded := NewConfig()

	if err := json.Unmarshal(data, &loaded); err != nil {
		// Best effort: keep the damaged file for inspection.
		_ = os.Rename(path, path+".corrupt")

		return config, nil
	}

	return loaded, nil
}

// SaveToFile writes the settings through a temporary file and a rename, so
// that a crash in the middle never leaves a half-written config.json.
func (service *Service) SaveToFile(config Config) error {
	path, err := service.getJsonPath()
	if err != nil {
		return err
	}

	service.mutex.Lock()
	defer service.mutex.Unlock()

	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		return err
	}

	data, err := json.Marshal(config)
	if err != nil {
		return err
	}

	temporary := path + ".tmp"

	err = os.WriteFile(temporary, data, 0o644)
	if err != nil {
		return err
	}

	err = renameWithRetry(temporary, path)
	if err != nil {
		_ = os.Remove(temporary)
	}

	return err
}

// renameWithRetry covers the short moments in which Windows antivirus or
// indexers hold the destination file open.
func renameWithRetry(from, to string) error {
	var err error

	for range 5 {
		err = os.Rename(from, to)
		if err == nil {
			return nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return err
}

func (service *Service) getJsonPath() (string, error) {
	if service.path != "" {
		return service.path, nil
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "Actualizador", "config.json"), nil
}
