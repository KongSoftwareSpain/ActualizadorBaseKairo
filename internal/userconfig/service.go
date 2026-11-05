// Package userconfig loads and saves the user settings as JSON.
package userconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Config holds the settings remembered between sessions. Never add
// passwords or other secrets: the file is plain JSON.
type Config struct {
	IsDark bool `json:"isDark"`
}

func NewConfig() Config {
	return Config{
		IsDark: false,
	}
}

type Service struct {
	mutex sync.RWMutex
}

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

	err = json.Unmarshal(data, &config)
	if err != nil {
		return config, err
	}

	return config, nil
}

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

	return os.WriteFile(path, data, 0o644)
}

func (service *Service) getJsonPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "MigradorKairo", "config.json"), nil
}
