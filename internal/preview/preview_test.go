package preview

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"modelkeeper/internal/db"
)

// fixture: a root with a disk STL, an archive with an OBJ member, a text file,
// a missing file and a file recorded as too large.
type fixture struct {
	db                                   *sql.DB
	stl, member, txt, gone, huge, remote int64
}

func setup(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.MkdirAll(filepath.Join(root, "Orc"), 0o755)
	os.WriteFile(filepath.Join(root, "Orc", "orc.stl"), []byte("solid orc"), 0o644)
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("parts/body.obj")
	w.Write([]byte("v 0 0 0"))
	zw.Close()
	os.WriteFile(filepath.Join(root, "Orc", "orc.zip"), zbuf.Bytes(), 0o644)

	conn, err := db.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := conn.Exec(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	r := exec(`INSERT INTO root (path, kind, status, created_at) VALUES (?, 'local', 'online', 0)`, root)
	off := exec(`INSERT INTO root (path, kind, status, created_at) VALUES (?, 'nas', 'offline', 0)`, filepath.Join(dir, "nas"))
	loc := func(rootID int64, archiveID any, path, ext, cat string, size int64, present int) int64 {
		return exec(`INSERT INTO location (root_id, archive_id, path, ext, category, size, mtime, present, first_seen_at)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, 0)`, rootID, archiveID, path, ext, cat, size, present)
	}
	f := fixture{db: conn}
	f.stl = loc(r, nil, "Orc/orc.stl", "stl", "model", 9, 1)
	arch := loc(r, nil, "Orc/orc.zip", "zip", "archive", int64(zbuf.Len()), 1)
	f.member = loc(r, arch, "parts/body.obj", "obj", "model", 7, 1)
	f.txt = loc(r, nil, "Orc/readme.txt", "txt", "supplementary", 3, 1)
	f.gone = loc(r, nil, "Orc/old.stl", "stl", "model", 3, 0)
	f.huge = loc(r, nil, "Orc/huge.stl", "stl", "model", MaxBytes+1, 1)
	f.remote = loc(off, nil, "x.stl", "stl", "model", 3, 1)
	return f
}

func read(t *testing.T, s Source) string {
	t.Helper()
	rc, _, err := s.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestLookupAndOpen(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	s, err := Lookup(ctx, f.db, f.stl)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "orc.stl" || read(t, s) != "solid orc" {
		t.Errorf("disk file: %+v", s)
	}

	s, err = Lookup(ctx, f.db, f.member)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "body.obj" || read(t, s) != "v 0 0 0" {
		t.Errorf("archive member: %+v", s)
	}

	for name, c := range map[string]struct {
		id   int64
		want error
	}{
		"unknown":     {9999, ErrNotFound},
		"unsupported": {f.txt, ErrUnsupported},
		"not present": {f.gone, ErrNotFound},
		"too large":   {f.huge, ErrTooLarge},
		"offline":     {f.remote, ErrOffline},
	} {
		if _, err := Lookup(ctx, f.db, c.id); !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, want %v", name, err, c.want)
		}
	}
}

func TestOpenVanishedFile(t *testing.T) {
	f := setup(t)
	s, err := Lookup(context.Background(), f.db, f.stl)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(s.rootPath, filepath.FromSlash(s.path)))
	if _, _, err := s.Open(); !errors.Is(err, ErrNotFound) {
		t.Errorf("err %v, want ErrNotFound", err)
	}
}
