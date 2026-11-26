package diag_test

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modelkeeper/internal/diag"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"language":"de"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "catalog.db"), []byte("secret catalog"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sync"), 0o755)
	os.WriteFile(filepath.Join(dir, "sync", "state.json"), []byte(`{}`), 0o644)
	os.WriteFile(filepath.Join(dir, "sync", "base-x.json"), []byte(`{"models":[]}`), 0o644)
	log := filepath.Join(dir, "modelkeeper.log")
	os.WriteFile(log, bytes.Repeat([]byte("line\n"), diag.MaxFileBytes/2), 0o644)
	var buf bytes.Buffer
	names, err := diag.Write(&buf, diag.Input{DataDir: dir, Log: log, System: map[string]string{"os": "test"}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names, ",")
	if got != "settings.json,sync/state.json,modelkeeper.log,system.json,README.txt" {
		t.Errorf("names %s", got)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if f.Name == "modelkeeper.log" && f.UncompressedSize64 > diag.MaxFileBytes {
			t.Errorf("log %d bytes", f.UncompressedSize64)
		}
	}
}
