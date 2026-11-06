// Package migrator applies versioned SQL scripts to a SQL Server database.
package migrator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
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
		hash := sha256.Sum256([]byte(normalizeLineEndings(content)))

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

// normalizeLineEndings makes the checksum independent of the Git autocrlf
// setting: the same script has the same checksum on every machine.
func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

// decodeText converts a script to UTF-8, honouring UTF-8 and UTF-16 BOMs.
// SSMS exports UTF-16 LE by default. Anything that is not valid text in one
// of those encodings (ANSI, UTF-16 without BOM) is rejected: guessing would
// silently corrupt accents inside string literals.
func decodeText(raw []byte) (string, error) {
	var text string

	switch {
	case len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF:
		text = string(raw[3:])
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE:
		decoded, err := decodeUTF16(raw[2:], false)
		if err != nil {
			return "", err
		}
		text = decoded
	case len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF:
		decoded, err := decodeUTF16(raw[2:], true)
		if err != nil {
			return "", err
		}
		text = decoded
	default:
		if bytes.IndexByte(raw, 0) >= 0 {
			return "", errors.New("parece UTF-16 sin BOM: guárdalo como UTF-8 o UTF-16 con BOM")
		}
		text = string(raw)
	}

	if !utf8.ValidString(text) {
		return "", errors.New("no es UTF-8 válido (¿está en ANSI?): guárdalo como UTF-8 o UTF-16")
	}

	if strings.ContainsRune(text, utf8.RuneError) {
		return "", errors.New("contiene caracteres no válidos (U+FFFD): revisa la codificación")
	}

	return text, nil
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
