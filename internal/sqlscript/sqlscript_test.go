package sqlscript

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

func collect(t *testing.T, script string) []Batch {
	t.Helper()

	var batches []Batch

	err := ReadBatches(strings.NewReader(script), func(batch Batch) error {
		batches = append(batches, batch)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadBatches: %v", err)
	}

	return batches
}

func texts(batches []Batch) []string {
	var result []string

	for _, batch := range batches {
		result = append(result, strings.TrimSpace(batch.Text))
	}

	return result
}

func TestReadBatchesSplitsOnGo(t *testing.T) {
	batches := collect(t, "A\r\nGO\r\nB\r\ngo 2\r\nGOTO x\r\n")

	got := texts(batches)
	want := []string{"A", "B", "GOTO x"}

	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("batch %d: got %q, want %q", index, got[index], want[index])
		}
	}

	if batches[0].Number != 1 || batches[2].Number != 3 {
		t.Fatalf("numbers: %d, %d", batches[0].Number, batches[2].Number)
	}
}

func TestReadBatchesIgnoresGoInsideString(t *testing.T) {
	batches := collect(t, "INSERT t VALUES (N'it''s\r\nGO\r\nlinea2')\r\nGO\r\nSELECT 1\r\n")

	if len(batches) != 2 {
		t.Fatalf("got %d batches: %q", len(batches), texts(batches))
	}

	if !strings.Contains(batches[0].Text, "linea2") {
		t.Fatalf("string was split: %q", batches[0].Text)
	}
}

func utf16Bytes(text string, isBigEndian bool, bom bool) []byte {
	var buffer bytes.Buffer

	write := func(unit uint16) {
		if isBigEndian {
			buffer.Write([]byte{byte(unit >> 8), byte(unit)})
		} else {
			buffer.Write([]byte{byte(unit), byte(unit >> 8)})
		}
	}

	if bom {
		write(0xFEFF)
	}

	for _, unit := range utf16.Encode([]rune(text)) {
		write(unit)
	}

	return buffer.Bytes()
}

func TestNewReaderDecodesUTF16LEBOMUTF16BEBOMUTF8BOMAndPlain(t *testing.T) {
	const text = "SELECT 'ñ'"

	sources := map[string][]byte{
		"utf16le": utf16Bytes(text, false, true),
		"utf16be": utf16Bytes(text, true, true),
		"utf8bom": append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...),
		"plain":   []byte(text),
	}

	for name, raw := range sources {
		reader, err := NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		decoded, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if string(decoded) != text {
			t.Fatalf("%s: got %q", name, decoded)
		}
	}
}

func TestHeaderIsSkipped(t *testing.T) {
	script := "USE [master]\nGO\nCREATE DATABASE [X]\n ON PRIMARY (NAME=N'X')\nGO\n" +
		"IF (1 = FULLTEXTSERVICEPROPERTY('IsFullTextInstalled')) begin EXEC [X].[dbo].[sp_fulltext_database] @action = 'enable' end\nGO\n" +
		"ALTER DATABASE [X] SET RECOVERY SIMPLE\nGO\nUSE [X]\nGO\nCREATE TABLE t (a int)\nGO\nUSE [master]\nGO\nALTER DATABASE [X] SET READ_WRITE\nGO\n"

	var executed []string

	for _, batch := range collect(t, script) {
		if !batch.IsSkipped {
			executed = append(executed, strings.TrimSpace(batch.Text))
		}
	}

	if len(executed) != 1 || executed[0] != "CREATE TABLE t (a int)" {
		t.Fatalf("executed %q", executed)
	}
}

func TestScriptWithoutCreateDatabaseOnlySkipsUseAndAlterDatabase(t *testing.T) {
	var executed []string

	for _, batch := range collect(t, "CREATE TABLE a (x int)\nGO\nUSE [Z]\nGO\nCREATE TABLE b (x int)\nGO\n") {
		if !batch.IsSkipped {
			executed = append(executed, strings.TrimSpace(batch.Text))
		}
	}

	if len(executed) != 2 || executed[0] != "CREATE TABLE a (x int)" || executed[1] != "CREATE TABLE b (x int)" {
		t.Fatalf("executed %q", executed)
	}
}

func TestPrincipalBatches(t *testing.T) {
	cases := map[string]bool{
		"CREATE USER [k] FOR LOGIN [k]":                     true,
		"ALTER ROLE [db_datareader] ADD MEMBER [k]":         true,
		"ALTER AUTHORIZATION ON SCHEMA::x TO y":             true,
		"/****** Objeto: User [k] ******/\nCREATE USER [k]": true,
		"-- nota\r\n/* otra */ ALTER ROLE r ADD MEMBER m":   true,
		"CREATE TABLE t (a int)":                            false,
		"INSERT t VALUES ('CREATE USER x')":                 false,
	}

	for text, want := range cases {
		batches := collect(t, text+"\nGO\n")

		if len(batches) != 1 || batches[0].IsPrincipal != want {
			t.Fatalf("%q: IsPrincipal=%v, want %v", text, batches[0].IsPrincipal, want)
		}
	}
}

func TestEmptyScript(t *testing.T) {
	for _, script := range []string{"", "GO\r\n", "\r\n  \r\nGO\r\nGO\r\n"} {
		err := ReadBatches(strings.NewReader(script), func(Batch) error { return nil })
		if !errors.Is(err, ErrNoStatements) {
			t.Fatalf("%q: got %v", script, err)
		}
	}
}

func TestLongLine(t *testing.T) {
	long := strings.Repeat("x", 200000)
	batches := collect(t, "SELECT '"+long+"'\nGO\n")

	if len(batches) != 1 || !strings.Contains(batches[0].Text, long) {
		t.Fatal("long line was truncated")
	}
}

func TestHandlerErrorStops(t *testing.T) {
	stop := errors.New("stop")
	calls := 0

	err := ReadBatches(strings.NewReader("A\nGO\nB\nGO\n"), func(Batch) error {
		calls++
		return stop
	})

	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRealScript(t *testing.T) {
	const path = `C:\Users\MIGUEL\Desktop\Tecnicos\script.sql`

	file, err := os.Open(path)
	if err != nil {
		t.Skip("no está el script real")
	}
	defer file.Close()

	tables := 0
	total := 0

	err = ReadBatches(file, func(batch Batch) error {
		total++

		upper := strings.ToUpper(batch.Text)

		if !batch.IsSkipped {
			if strings.Contains(upper, "CREATE DATABASE") || strings.Contains(upper, "ALTER DATABASE") {
				t.Fatalf("lote %d de servidor sin omitir: %.80s", batch.Number, batch.Text)
			}

			if strings.Contains(upper, "CREATE TABLE") {
				tables++
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if tables != 364 {
		t.Fatalf("tablas: %d", tables)
	}

	if total < 5600 || total > 5700 {
		t.Fatalf("lotes: %d", total)
	}
}
