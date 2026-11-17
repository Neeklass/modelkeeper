package opencopy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAndCleanup(t *testing.T) {
	dir := t.TempDir()
	then := time.Now().Add(-Keep - time.Hour)
	p, err := Write(dir, 7, `parts\orc:body.stl`, strings.NewReader("solid orc"), then)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "orc_body.stl" {
		t.Errorf("name = %s", filepath.Base(p))
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "solid orc" {
		t.Fatalf("content %q, %v", b, err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("copy is writable: %v", fi.Mode())
	}
	// A second open gets its own copy.
	p2, err := Write(dir, 7, "orc.stl", strings.NewReader("x"), time.Now())
	if err != nil || filepath.Dir(p2) == filepath.Dir(p) {
		t.Fatalf("second copy %s, %v", p2, err)
	}
	// Something the user saved next to the old copy stays.
	own := filepath.Join(filepath.Dir(p), "project.chitubox")
	os.WriteFile(own, []byte("mine"), 0o644)
	os.Chtimes(filepath.Dir(p), then, then) // saving there made the folder young again
	// A folder that is not ours stays as well.
	foreign := filepath.Join(dir, "foreign")
	os.Mkdir(foreign, 0o755)
	os.Chtimes(foreign, then, then)

	n, err := Cleanup(dir, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("cleanup removed %d, %v", n, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("old copy still there")
	}
	if _, err := os.Stat(own); err != nil {
		t.Error("user's file was removed")
	}
	if _, err := os.Stat(p2); err != nil {
		t.Error("new copy was removed")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign folder was removed")
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{"a/b/c.stl": "c.stl", "..": "model", "x?.stl": "x_.stl", "name. ": "name"} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
