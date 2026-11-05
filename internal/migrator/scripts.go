// Package migrator applies versioned SQL scripts to a SQL Server database.
package migrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

var scriptNamePattern = regexp.MustCompile(`^V(\d{4})__(.+)\.sql$`)

// Script is one migration file read from the Scripts folder.
type Script struct {
	Version  int    `json:"version"`
	Name     string `json:"name"`
	Content  string `json:"-"`
	Checksum string `json:"checksum"`
}

// LoadScripts reads dir, validates the file names and returns the scripts
// ordered by version. Versions must start at 1 with no duplicates or gaps.
func LoadScripts(dir string) ([]Script, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no se puede leer la carpeta de scripts %q: %w", dir, err)
	}

	var scripts []Script
	var invalidNames []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".sql") {
			continue
		}

		match := scriptNamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			invalidNames = append(invalidNames, entry.Name())
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("no se puede leer %s: %w", entry.Name(), err)
		}

		content, err := decodeText(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}

		version, _ := strconv.Atoi(match[1])
		hash := sha256.Sum256([]byte(content))

		scripts = append(scripts, Script{
			Version:  version,
			Name:     entry.Name(),
			Content:  content,
			Checksum: hex.EncodeToString(hash[:]),
		})
	}

	if len(invalidNames) > 0 {
		return nil, fmt.Errorf("nombres de script inválidos (esperado VNNNN__descripcion.sql): %s", strings.Join(invalidNames, ", "))
	}

	sort.Slice(scripts, func(i, j int) bool { return scripts[i].Version < scripts[j].Version })

	expected := 1
	for index, script := range scripts {
		if index > 0 && script.Version == scripts[index-1].Version {
			return nil, fmt.Errorf("versión duplicada V%04d: %s y %s", script.Version, scripts[index-1].Name, script.Name)
		}

		if script.Version != expected {
			return nil, fmt.Errorf("falta el script V%04d (antes de %s)", expected, script.Name)
		}

		expected++
	}

	return scripts, nil
}

// decodeText converts a script to UTF-8, honouring UTF-8 and UTF-16 BOMs.
// SSMS exports UTF-16 LE by default.
func decodeText(raw []byte) (string, error) {
	switch {
	case len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF:
		return string(raw[3:]), nil
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE:
		return decodeUTF16(raw[2:], false)
	case len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF:
		return decodeUTF16(raw[2:], true)
	default:
		return string(raw), nil
	}
}

func decodeUTF16(raw []byte, isBigEndian bool) (string, error) {
	if len(raw)%2 != 0 {
		return "", fmt.Errorf("fichero UTF-16 con longitud impar")
	}

	units := make([]uint16, len(raw)/2)
	for index := range units {
		low, high := raw[2*index], raw[2*index+1]
		if isBigEndian {
			low, high = high, low
		}
		units[index] = uint16(low) | uint16(high)<<8
	}

	return string(utf16.Decode(units)), nil
}
