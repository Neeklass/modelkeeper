package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The sync state and base files are written into data/sync, which does not
// exist before the first sync with another PC.
func TestWriteAtomicCreatesFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data", "sync", "base-x.json")
	if err := writeAtomic(file, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != `{}` {
		t.Fatalf("%q %v", b, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(file))
	if len(entries) != 1 {
		t.Errorf("temporary file left: %v", entries)
	}
}
