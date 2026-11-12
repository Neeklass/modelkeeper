package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"modelkeeper/internal/appdir"
	"modelkeeper/internal/backup"
	"modelkeeper/internal/db"
	"modelkeeper/internal/msg"
)

// startupEnv is a data folder with a catalog and 20 old daily snapshots
// (more than the rotation keeps).
func startupEnv(t *testing.T) appdir.Layout {
	t.Helper()
	layout := appdir.NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Open(layout.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ { // enough pages to damage
		if _, err := conn.Exec(`INSERT INTO model (uid, name, notes, created_at, updated_at) VALUES (?, 'm', ?, 0, 0)`,
			i, string(make([]byte, 500))); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().AddDate(0, -2, 0)
	for i := 0; i < 20; i++ {
		if _, err := backup.Create(context.Background(), layout.Backups, backup.Source{DB: conn}, backup.ReasonDaily,
			old.AddDate(0, 0, -i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(conn); err != nil {
		t.Fatal(err)
	}
	return layout
}

func folderNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func damage(t *testing.T, file string) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for i := 4096; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func closeApp(a *App) {
	a.bg.Wait()
	if a.db != nil {
		db.Close(a.db)
	}
}

// A healthy catalog gets its daily snapshot, and old ones are rotated.
func TestStartupBacksUpAndRotates(t *testing.T) {
	layout := startupEnv(t)
	a := &App{layout: layout}
	if err := a.openCatalog(false); err != nil {
		t.Fatal(err)
	}
	closeApp(a)
	if !backup.HasDaily(layout.Backups, time.Now()) {
		t.Error("no daily snapshot after start")
	}
	if list, _ := backup.List(layout.Backups); len(list) != backup.KeepDaily {
		t.Errorf("%d snapshots after rotation, want %d", len(list), backup.KeepDaily)
	}
}

// A damaged catalog stops the start: no rotation, no new snapshot, and the
// message names the newest snapshot (J2).
func TestDamagedCatalogBlocksStartAndRotation(t *testing.T) {
	layout := startupEnv(t)
	before := folderNames(t, layout.Backups)
	newest, _ := backup.Latest(layout.Backups)
	damage(t, layout.Catalog)

	a := &App{layout: layout}
	err := a.openCatalog(false)
	closeApp(a)
	m := msg.From(err)
	if m.Key != "error.catalogCorrupt" || m.Params["backup"] != newest.Path {
		t.Fatalf("openCatalog = %+v, want error.catalogCorrupt with backup %s", m, newest.Path)
	}
	if a.db != nil {
		t.Error("damaged catalog was kept open")
	}
	if after := folderNames(t, layout.Backups); len(after) != len(before) {
		t.Errorf("backups changed: %d folders before, %d after", len(before), len(after))
	}
	if a.CreateBackup(); backup.HasDaily(layout.Backups, time.Now()) {
		t.Error("snapshot written over a damaged catalog")
	}
}

// A missing catalog with snapshots present does not silently start empty.
func TestMissingCatalogAsksBeforeStartingEmpty(t *testing.T) {
	layout := startupEnv(t)
	newest, _ := backup.Latest(layout.Backups)
	os.Remove(layout.Catalog)

	a := &App{layout: layout}
	err := a.openCatalog(false)
	if m := msg.From(err); m.Key != "error.catalogMissing" || m.Params["backup"] != newest.Path {
		t.Fatalf("openCatalog = %+v", m)
	}
	if _, err := os.Stat(layout.Catalog); err == nil {
		t.Fatal("an empty catalog was created")
	}
	if err := a.openCatalog(true); err != nil {
		t.Fatal(err)
	}
	closeApp(a)
	if _, err := os.Stat(filepath.Join(newest.Path, "catalog.db")); err != nil {
		t.Errorf("snapshot touched: %v", err)
	}
}
