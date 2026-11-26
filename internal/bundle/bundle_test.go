package bundle_test

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"modelkeeper/internal/bundle"
	"modelkeeper/internal/db"
)

func TestBuildAndWrite(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	files := map[string]string{"Atlas/Orc/orc.stl": "solid orc", "Atlas/Orc/sup/orc_sup.stl": "solid sup",
		"Atlas/elf.zip": "PK zip bytes", "Atlas/Gone/gone.stl": ""}
	for p, c := range files {
		full := filepath.Join(lib, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if c != "" {
			os.WriteFile(full, []byte(c), 0o644)
		}
	}
	conn, err := db.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(conn)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO root (id, path, kind, status, created_at) VALUES (1, ?, 'local', 'online', 0)`, lib)
	exec(`INSERT INTO model (id, uid, name, created_at, updated_at) VALUES (1, 'a', 'Orc: Warrior', 0, 0), (2, 'b', 'Elf', 0, 0)`)
	exec(`INSERT INTO model_anchor (model_id, root_id, path, kind) VALUES (1, 1, 'Atlas/Orc', 'folder'), (2, 1, 'Atlas/elf.zip', 'archive')`)
	loc := func(id int64, archive any, path, ext, cat string, model any, present int) {
		exec(`INSERT INTO location (id, root_id, archive_id, path, ext, category, size, mtime, present, first_seen_at, model_id)
			VALUES (?, 1, ?, ?, ?, ?, 9, 0, ?, 0, ?)`, id, archive, path, ext, cat, present, model)
	}
	loc(1, nil, "Atlas/Orc/orc.stl", "stl", "model", 1, 1)
	loc(2, nil, "Atlas/Orc/sup/orc_sup.stl", "stl", "model", 1, 1)
	loc(3, nil, "Atlas/elf.zip", "zip", "archive", nil, 1)
	loc(4, 3, "elf/elf.stl", "stl", "model", 2, 1)
	loc(5, 3, "elf/elf2.stl", "stl", "model", 2, 1)
	loc(6, nil, "Atlas/Gone/gone.stl", "stl", "model", 1, 0)

	p, err := bundle.Build(ctx, conn, []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range p.Files {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "Elf/elf.zip,Orc_ Warrior/orc.stl,Orc_ Warrior/sup/orc_sup.stl" {
		t.Errorf("files %s", got)
	}
	if p.Skipped != 1 {
		t.Errorf("skipped %d", p.Skipped)
	}
	out := filepath.Join(t.TempDir(), "c.zip")
	if err := bundle.Write(ctx, p, out, nil); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if f.Name == "Orc_ Warrior/orc.stl" && string(b) != "solid orc" {
			t.Errorf("content %q", b)
		}
	}
	// The library is unchanged.
	if b, _ := os.ReadFile(filepath.Join(lib, "Atlas/Orc/orc.stl")); string(b) != "solid orc" {
		t.Error("source changed")
	}
	// Cancelled: no file left behind.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	out2 := filepath.Join(t.TempDir(), "d.zip")
	if err := bundle.Write(cctx, p, out2, nil); err == nil {
		t.Error("cancelled write succeeded")
	}
	if _, err := os.Stat(out2 + ".part"); err == nil {
		t.Error("partial file left")
	}
}
