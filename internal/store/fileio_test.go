package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	for _, content := range []string{"a", "b"} {
		if err := writeFileAtomic(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "b" {
		t.Fatalf("content = %q, %v; want %q", got, err, "b")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "x.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only x.json", names)
	}
}

func TestWriteFileExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := writeFileExclusive(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileExclusive(path, []byte("second")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second write: got %v, want fs.ErrExist", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "first" {
		t.Fatalf("content = %q, %v; want %q", got, err, "first")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want 1 (temp file left behind?)", len(entries))
	}
}
