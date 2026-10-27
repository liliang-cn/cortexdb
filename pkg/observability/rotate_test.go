package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The trace file is cut at its size and only keep old files stay; a record
// is never split across two files.
func TestRotatingFileCutsAndKeeps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	f, err := OpenRotatingFile(path, 100, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: two fit, the third rotates
	for i := 0; i < 9; i++ {
		if _, err := f.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(b) == 0 || len(b) > 100 || len(b)%len(line) != 0 {
			t.Fatalf("%s holds %d bytes", name, len(b))
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("kept more than 2 old files: %v", err)
	}
}

func TestRotatingFileWithoutKeepTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	f, err := OpenRotatingFile(path, 10, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.Write([]byte("12345678\n"))
	_, _ = f.Write([]byte("abcdefgh\n"))
	b, _ := os.ReadFile(path)
	if string(b) != "abcdefgh\n" {
		t.Fatalf("got %q", b)
	}
	if m, _ := filepath.Glob(path + ".*"); len(m) != 0 {
		t.Fatalf("kept %v with keep=0", m)
	}
}
