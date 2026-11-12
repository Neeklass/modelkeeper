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
	"modelkeeper/internal/scanner"
	"modelkeeper/internal/settings"
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
	conn, err := db.Open(layout.Catalog) // a change since the last snapshot
	if err != nil {
		t.Fatal(err)
	}
	conn.Exec(`UPDATE model SET notes = 'changed' WHERE id = 1`)
	db.Close(conn)
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

// Without changes since the newest snapshot, the day gets no copy, and
// later starts that day do not even try again.
func TestStartupSkipsUnchangedDay(t *testing.T) {
	layout := startupEnv(t)
	before, _ := backup.List(layout.Backups)
	for i := 0; i < 2; i++ {
		a := &App{layout: layout}
		if err := a.openCatalog(false); err != nil {
			t.Fatal(err)
		}
		closeApp(a)
	}
	after, _ := backup.List(layout.Backups)
	if len(after) != len(before) || backup.HasDaily(layout.Backups, time.Now()) {
		t.Errorf("snapshots %d -> %d; an unchanged day got a copy", len(before), len(after))
	}
	if b, err := os.ReadFile(filepath.Join(layout.Backups, ".last-daily")); err != nil || len(b) == 0 {
		t.Errorf("daily marker: %q, %v", b, err)
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

// Permanently deleting a root takes a snapshot first; if that fails, the root
// and everything below it stay (E9, J2).
func TestPurgeRootStopsWhenBackupFails(t *testing.T) {
	layout := appdir.NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	a := &App{layout: layout, ctx: context.Background()}
	if err := a.openCatalog(false); err != nil {
		t.Fatal(err)
	}
	defer closeApp(a)
	a.bg.Wait()
	scans, err := scanner.NewManager(a.db, settings.Defaults, nil, scanner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a.scans = scans
	if _, err := a.db.Exec(`INSERT INTO root (id, path, kind, created_at, removed_at) VALUES (1, 'C:\old', 'local', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	a.db.Exec(`INSERT INTO location (root_id, path, ext, category, size, mtime, first_seen_at) VALUES (1, 'a.stl', 'stl', 'model', 1, 0, 0)`)

	// data\backups cannot be created: a file is in the way.
	os.RemoveAll(layout.Backups)
	os.WriteFile(layout.Backups, []byte("not a folder"), 0o644)
	if err := a.PurgeRoot(1, 0); msg.From(err).Key != "error.backupFailed" {
		t.Fatalf("PurgeRoot = %v, want error.backupFailed", err)
	}
	var n int
	a.db.QueryRow(`SELECT count(*) FROM root WHERE id = 1`).Scan(&n)
	var locs int
	a.db.QueryRow(`SELECT count(*) FROM location WHERE root_id = 1`).Scan(&locs)
	if n != 1 || locs != 1 {
		t.Fatalf("root deleted although the backup failed (root %d, locations %d)", n, locs)
	}

	// With a working backup folder it goes through, after a snapshot.
	os.Remove(layout.Backups)
	if err := a.PurgeRoot(1, 0); err != nil {
		t.Fatal(err)
	}
	if s, ok := backup.Latest(layout.Backups); !ok || s.Manifest.Reason != backup.ReasonRemoveRoot {
		t.Errorf("no pre-remove-root snapshot: %+v", s)
	}
}

// A restore chosen in the app is applied at the next start, before the
// catalog is opened, and reported once.
func TestRestoreAtStart(t *testing.T) {
	layout := startupEnv(t) // 50 models, 20 snapshots of that state
	conn, _ := db.Open(layout.Catalog)
	conn.Exec(`DELETE FROM model`) // the mistake to undo
	db.Close(conn)
	newest, _ := backup.Latest(layout.Backups)

	a := &App{layout: layout}
	if err := a.RequestRestore(newest.Name); err != nil {
		t.Fatal(err)
	}
	if s := a.GetStatus(); s.PendingRestore != newest.Name {
		t.Fatalf("pending = %q", s.PendingRestore)
	}

	b := &App{layout: layout} // next start
	b.applyRestore()
	if b.notice == nil || b.notice.Key != "backup.restored" {
		t.Fatalf("notice = %+v", b.notice)
	}
	if err := b.openCatalog(false); err != nil {
		t.Fatal(err)
	}
	defer closeApp(b)
	var n int
	b.db.QueryRow(`SELECT count(*) FROM model`).Scan(&n)
	if n != 50 {
		t.Errorf("%d models after restore, want 50", n)
	}
	if s := b.GetStatus(); s.PendingRestore != "" {
		t.Error("restore still pending")
	}
}
