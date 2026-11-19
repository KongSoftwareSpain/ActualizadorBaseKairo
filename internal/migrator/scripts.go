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
	// Description is the leading block of "--" comments of the file: why the
	// script exists. Empty when the file does not start with a comment.
	Description string `json:"description"`
	// AcceptedChecksums are earlier versions of this script that count as
	// applied: see loadAcceptedChecksums.
	AcceptedChecksums []string `json:"-"`
}

const acceptedSuffix = ".accept"

var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// loadAcceptedChecksums reads "<script>.sql.accept", the escape hatch for a
// script that had to be corrected after some databases already applied it: one
// SHA-256 per line (as the script list shows it), "#" starts a comment. It lives
// outside the script so adding a line never changes the script's own checksum.
// The corrected script must have the same effect as the accepted versions.
func loadAcceptedChecksums(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	text, err := decodeText(raw)
	if err != nil {
		return nil, err
	}

	var checksums []string

	for number, line := range strings.Split(normalizeLineEndings(text), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if !checksumPattern.MatchString(line) {
			return nil, fmt.Errorf("línea %d: se esperaba un SHA-256 en minúsculas (64 hex), no %q", number+1, line)
		}

		checksums = append(checksums, line)
	}

	return checksums, nil
}

// extractDescription returns the "--" comment lines at the top of a script,
// up to the first blank or code line. Leading blank lines are skipped.
func extractDescription(content string) string {
	var lines []string

	for line := range strings.SplitSeq(normalizeLineEndings(content), "\n") {
		line = strings.TrimSpace(line)

		if line == "" && len(lines) == 0 {
			continue
		}

		text, isComment := strings.CutPrefix(line, "--")
		if !isComment {
			break
		}

		lines = append(lines, strings.TrimSpace(text))
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
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
		if entry.IsDir() {
			continue
		}

		if base, isAccept := strings.CutSuffix(entry.Name(), acceptedSuffix); isAccept && strings.EqualFold(filepath.Ext(base), ".sql") {
			if _, err := os.Stat(filepath.Join(dir, base)); err != nil {
				invalidNames = append(invalidNames, entry.Name()+" (no hay script "+base+")")
			}
			continue
		}

		if !strings.EqualFold(filepath.Ext(entry.Name()), ".sql") {
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

		accepted, err := loadAcceptedChecksums(filepath.Join(dir, entry.Name()+acceptedSuffix))
		if err != nil {
			return nil, fmt.Errorf("%s%s: %w", entry.Name(), acceptedSuffix, err)
		}

		version, _ := strconv.Atoi(match[1])
		hash := sha256.Sum256([]byte(normalizeLineEndings(content)))

		scripts = append(scripts, Script{
			Version:  version,
			Name:     entry.Name(),
			Content:  content,
			Checksum: hex.EncodeToString(hash[:]),

			Description:       extractDescription(content),
			AcceptedChecksums: accepted,
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
