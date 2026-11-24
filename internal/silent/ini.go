package silent

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

const iniSection = "BaseDatos"

// ReadConnection reads [BaseDatos] Servidor and BaseDatos from the Kairo
// config.ini (the file Kairo itself reads, see modConfigBD.bas). Sections and
// keys are case-insensitive, like GetPrivateProfileString.
func ReadConnection(path string) (Connection, error) {
	var connection Connection

	if strings.TrimSpace(path) == "" {
		return connection, errors.New("no se indicó el config.ini de Kairo (--ini)")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return connection, fmt.Errorf("no se puede leer %s: %w", path, err)
	}

	values := parseIni(decodeIni(raw))[strings.ToLower(iniSection)]
	connection.Server = values["servidor"]
	connection.Database = values["basedatos"]
	connection.Username = values["usuario"]

	if connection.Server == "" || connection.Database == "" {
		return connection, fmt.Errorf("%s no tiene [%s] Servidor y BaseDatos", path, iniSection)
	}

	return connection, nil
}

// decodeIni returns the text of an ini that is ANSI (what VB6 writes), UTF-8
// or UTF-16 with BOM. Server and database names are plain ASCII in practice.
func decodeIni(raw []byte) string {
	switch {
	case len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF:
		return string(raw[3:])
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE:
		return decodeUTF16(raw[2:], false)
	case len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF:
		return decodeUTF16(raw[2:], true)
	default:
		return string(raw)
	}
}

func decodeUTF16(raw []byte, isBigEndian bool) string {
	units := make([]uint16, len(raw)/2)

	for index := range units {
		low, high := raw[2*index], raw[2*index+1]
		if isBigEndian {
			low, high = high, low
		}

		units[index] = uint16(low) | uint16(high)<<8
	}

	return string(utf16.Decode(units))
}

// parseIni returns section -> key -> value, all lower-case names. Lines that
// start with ";" or "#" are comments.
func parseIni(text string) map[string]map[string]string {
	sections := map[string]map[string]string{}
	current := ""

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}

		if name, isSection := strings.CutPrefix(line, "["); isSection {
			if name, _, hasClose := strings.Cut(name, "]"); hasClose {
				current = strings.ToLower(strings.TrimSpace(name))
				if sections[current] == nil {
					sections[current] = map[string]string{}
				}
			}

			continue
		}

		key, value, hasEquals := strings.Cut(line, "=")
		if !hasEquals || current == "" {
			continue
		}

		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		// Como GetPrivateProfileString: gana la primera aparicion de una clave y se quita un par de comillas que envuelva el valor
		if _, isDuplicate := sections[current][key]; isDuplicate {
			continue
		}

		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}

		sections[current][key] = value
	}

	return sections
}
