package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/microsoft/go-mssqldb/batch"
)

func writeScript(t *testing.T, dir, name string, content []byte) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadScriptsOrdersByVersion(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "V0002__b.sql", []byte("SELECT 2"))
	writeScript(t, dir, "V0001__a.sql", []byte("SELECT 1"))
	writeScript(t, dir, "notas.txt", []byte("ignorado"))

	scripts, err := LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(scripts) != 2 || scripts[0].Version != 1 || scripts[1].Version != 2 {
		t.Fatalf("orden inesperado: %+v", scripts)
	}
}

func TestLoadScriptsRejectsInvalidSets(t *testing.T) {
	cases := map[string][]string{
		"hueco":      {"V0001__a.sql", "V0003__c.sql"},
		"sin inicio": {"V0002__b.sql"},
		"duplicado":  {"V0001__a.sql", "V0001__otra.sql"},
		"nombre":     {"V0001__a.sql", "V2__mal.sql"},
	}

	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, file := range files {
				writeScript(t, dir, file, []byte("SELECT 1"))
			}

			if _, err := LoadScripts(dir); err == nil {
				t.Fatal("se esperaba error")
			}
		})
	}
}

func TestLoadScriptsDecodesEncodings(t *testing.T) {
	const text = "-- acción: año\nSELECT 'ñandú'"

	utf16Bytes := []byte{0xFF, 0xFE}
	for _, unit := range utf16.Encode([]rune(text)) {
		utf16Bytes = append(utf16Bytes, byte(unit), byte(unit>>8))
	}

	dir := t.TempDir()
	writeScript(t, dir, "V0001__utf8.sql", []byte(text))
	writeScript(t, dir, "V0002__utf8bom.sql", append([]byte{0xEF, 0xBB, 0xBF}, text...))
	writeScript(t, dir, "V0003__utf16.sql", utf16Bytes)

	scripts, err := LoadScripts(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, script := range scripts {
		if script.Content != text {
			t.Errorf("%s: contenido %q", script.Name, script.Content)
		}
	}
}

// The executor relies on batch.Split ignoring GO inside comments and strings.
func TestSplitHandlesAwkwardGO(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want int
	}{
		{"mayúsculas y espacios", "SELECT 1\n  GO  \nSELECT 2", 2},
		{"minúsculas", "SELECT 1\ngo\nSELECT 2", 2},
		{"en cadena", "SELECT '\nGO\n'", 1},
		{"en comentario de bloque", "/*\nGO\n*/\nSELECT 1", 1},
		{"en comentario de línea", "-- GO\nSELECT 1", 1},
		{"dentro de palabra", "SELECT 1 AS GOLF", 1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var nonBlank int
			for _, part := range batch.Split(testCase.sql, "GO") {
				if strings.TrimSpace(part) != "" {
					nonBlank++
				}
			}

			if nonBlank != testCase.want {
				t.Fatalf("lotes: %d, esperado %d", nonBlank, testCase.want)
			}
		})
	}
}

func TestExtractDescription(t *testing.T) {
	cases := map[string]struct{ content, want string }{
		"single line":     {"-- Agrega Ruta_Id\n\nSET ANSI_NULLS ON\nGO", "Agrega Ruta_Id"},
		"multi line crlf": {"-- Primera\r\n--  Segunda\r\nGO", "Primera\nSegunda"},
		"leading blanks":  {"\n\n-- Motivo\nGO", "Motivo"},
		"stops at code":   {"SET NOCOUNT ON\n-- no es descripción", ""},
		"none":            {"GO", ""},
		"empty":           {"", ""},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := extractDescription(test.content); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}
