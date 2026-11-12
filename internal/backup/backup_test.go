package backup_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"modelkeeper/internal/backup"
	"modelkeeper/internal/db"
)

type env struct {
	t       *testing.T
	data    string
	backups string
	conn    *sql.DB
	src     backup.Source
}

func newEnv(t *testing.T) *env {
	t.Helper()
	data := t.TempDir()
	conn, err := db.Open(filepath.Join(data, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	thumbs := filepath.Join(data, "thumbnails")
	os.MkdirAll(thumbs, 0o755)
	settings := filepath.Join(data, "settings.json")
	os.WriteFile(settings, []byte(`{"language":"de"}`), 0o644)
	return &env{t: t, data: data, backups: filepath.Join(data, "backups"), conn: conn,
		src: backup.Source{DB: conn, Settings: settings, Thumbnails: thumbs, AppVersion: "test"}}
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.conn.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func count(t *testing.T, conn *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *env) create(reason string, at time.Time) backup.Snapshot {
	e.t.Helper()
	s, err := backup.Create(context.Background(), e.backups, e.src, reason, at)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestSnapshotMatchesCatalog(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 5; i++ {
		e.exec(`INSERT INTO model (id, uid, name, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`, i, fmt.Sprint("u", i), fmt.Sprint("Model ", i))
	}
	e.exec(`INSERT INTO tag (id, name) VALUES (1, 'orc'), (2, 'elf')`)
	e.exec(`INSERT INTO model_tag (model_id, tag_id) VALUES (1, 1), (2, 1), (2, 2)`)
	e.exec(`UPDATE model SET thumb_source = 'manual', thumb_file = 'manual.jpg' WHERE id = 1`)
	e.exec(`UPDATE model SET thumb_source = 'rendered', thumb_file = 'rendered.jpg' WHERE id = 2`)
	e.exec(`UPDATE model SET thumb_source = 'manual', thumb_file = 'gone.jpg' WHERE id = 3`) // file missing
	os.WriteFile(filepath.Join(e.src.Thumbnails, "manual.jpg"), []byte("manual"), 0o644)
	os.WriteFile(filepath.Join(e.src.Thumbnails, "rendered.jpg"), []byte("rendered"), 0o644)

	s := e.create(backup.ReasonManual, time.Now())

	snap, err := sql.Open("sqlite", filepath.Join(s.Path, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	for _, q := range []string{`PRAGMA user_version`, `SELECT count(*) FROM model`, `SELECT count(*) FROM model_tag`} {
		if got, want := count(t, snap, q), count(t, e.conn, q); got != want {
			t.Errorf("%s: snapshot %d, catalog %d", q, got, want)
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.Path, "thumbnails", "manual.jpg")); err != nil || string(b) != "manual" {
		t.Errorf("manual thumbnail: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(s.Path, "thumbnails", "rendered.jpg")); err == nil {
		t.Error("generated thumbnail was copied")
	}
	if b, err := os.ReadFile(filepath.Join(s.Path, "settings.json")); err != nil || string(b) != `{"language":"de"}` {
		t.Errorf("settings: %q, %v", b, err)
	}
	m := s.Manifest
	if m.App != backup.App || m.AppVersion != "test" || m.Reason != backup.ReasonManual || m.Models != 5 || m.Tags != 2 || m.Thumbnails != 1 || !m.Verified {
		t.Errorf("manifest = %+v", m)
	}
	if m.Schema != count(t, e.conn, `PRAGMA user_version`) {
		t.Errorf("manifest schema = %d", m.Schema)
	}

	if got := fmt.Sprint(names(t, s.Path)); got != "[catalog.db manifest.json settings.json thumbnails]" {
		t.Errorf("snapshot holds %s", got) // e.g. no -wal/-shm left by reading the copy
	}
	entries, _ := os.ReadDir(e.backups)
	if len(entries) != 1 || entries[0].Name() != s.Name {
		t.Errorf("backups folder holds %v, want only %s (no temporary folder)", entries, s.Name)
	}
	if got, ok := backup.Latest(e.backups); !ok || got.Name != s.Name {
		t.Errorf("Latest = %+v, %v", got, ok)
	}

	// Same second: a second snapshot gets its own folder.
	s2 := e.create(backup.ReasonManual, s.Time)
	if s2.Name == s.Name {
		t.Error("second snapshot in the same second overwrote the first")
	}
}

// A snapshot that fails halfway leaves nothing that counts as a snapshot.
func TestFailedSnapshotLeavesNothing(t *testing.T) {
	e := newEnv(t)
	e.conn.Close() // VACUUM INTO fails
	if _, err := backup.Create(context.Background(), e.backups, e.src, backup.ReasonManual, time.Now()); err == nil {
		t.Fatal("Create on a closed catalog succeeded")
	}
	entries, _ := os.ReadDir(e.backups)
	if len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestInvalidReason(t *testing.T) {
	e := newEnv(t)
	for _, r := range []string{"", "Daily", "../x", "a_b", "-x"} {
		if _, err := backup.Create(context.Background(), e.backups, e.src, r, time.Now()); err == nil {
			t.Errorf("reason %q accepted", r)
		}
	}
}

func names(t *testing.T, dir string) []string {
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

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestRotation(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local)
	day := 24 * time.Hour

	var daily, events []backup.Snapshot
	for i := 0; i < 20; i++ { // one daily per day, newest first
		daily = append(daily, e.create(backup.ReasonDaily, now.Add(-time.Duration(i)*day)))
	}
	recent := e.create(backup.ReasonRemoveRoot, now.Add(-30*day))
	old := e.create(backup.ReasonFileTypes, now.Add(-100*day))
	events = append(events, recent, old)

	// Not ours: must survive any rotation.
	foreign := []string{"my-notes", "2026-01-01_000000_daily", "2025-01-01_000000_manual-copy"}
	os.MkdirAll(filepath.Join(e.backups, foreign[0]), 0o755)
	os.MkdirAll(filepath.Join(e.backups, foreign[1]), 0o755) // scheme, but no manifest
	os.MkdirAll(filepath.Join(e.backups, foreign[2]), 0o755) // manifest of another app
	os.WriteFile(filepath.Join(e.backups, foreign[2], "manifest.json"), []byte(`{"app":"other","reason":"manual-copy"}`), 0o644)
	os.WriteFile(filepath.Join(e.backups, "readme.txt"), []byte("mine"), 0o644)
	// Leftovers: an old temporary folder of ours, and a foreign one.
	staleTmp := ".tmp-2026-01-01_000000_daily"
	os.MkdirAll(filepath.Join(e.backups, staleTmp), 0o755)
	os.Chtimes(filepath.Join(e.backups, staleTmp), now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	os.MkdirAll(filepath.Join(e.backups, ".tmp-other"), 0o755)
	os.Chtimes(filepath.Join(e.backups, ".tmp-other"), now.Add(-2*time.Hour), now.Add(-2*time.Hour))

	removed, err := backup.Rotate(e.backups, now)
	if err != nil {
		t.Fatal(err)
	}
	left := names(t, e.backups)
	for i, s := range daily {
		if keep := i < backup.KeepDaily; contains(left, s.Name) != keep {
			t.Errorf("daily %d (%s): kept = %v, want %v", i, s.Name, !keep, keep)
		}
	}
	if !contains(left, recent.Name) || contains(left, old.Name) {
		t.Errorf("event snapshots: recent kept %v, old kept %v", contains(left, recent.Name), contains(left, old.Name))
	}
	for _, f := range append(foreign, "readme.txt", ".tmp-other") {
		if !contains(left, f) {
			t.Errorf("rotation deleted %s, which it did not create", f)
		}
	}
	if contains(left, staleTmp) {
		t.Error("stale temporary folder not removed")
	}
	if want := 20 - backup.KeepDaily + 1 + 1; len(removed) != want {
		t.Errorf("removed %d: %v, want %d", len(removed), removed, want)
	}
}

// However old they are, the newest snapshots are never deleted.
func TestRotationKeepsNewest(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local)
	var all []backup.Snapshot
	for i := 0; i < 5; i++ {
		all = append(all, e.create(backup.ReasonManual, now.Add(-time.Duration(200+i)*24*time.Hour)))
	}
	if _, err := backup.Rotate(e.backups, now); err != nil {
		t.Fatal(err)
	}
	left := names(t, e.backups)
	if len(left) != backup.KeepNewest {
		t.Fatalf("left %v, want the %d newest", left, backup.KeepNewest)
	}
	for _, s := range all[:backup.KeepNewest] {
		if !contains(left, s.Name) {
			t.Errorf("newest snapshot %s deleted", s.Name)
		}
	}
}

func TestHasDaily(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.Local)
	if backup.HasDaily(e.backups, now) {
		t.Fatal("HasDaily without backups")
	}
	e.create(backup.ReasonManual, now)
	e.create(backup.ReasonDaily, now.Add(-24*time.Hour))
	if backup.HasDaily(e.backups, now) {
		t.Fatal("manual or yesterday's snapshot counted as today's daily")
	}
	e.create(backup.ReasonDaily, now.Add(-time.Hour))
	if !backup.HasDaily(e.backups, now) {
		t.Fatal("today's daily not found")
	}
}

// A damaged catalog never yields a snapshot that counts as valid: either the
// copy fails, or the copy fails its integrity check.
func TestSnapshotOfDamagedCatalogFails(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 200; i++ {
		e.exec(`INSERT INTO model (uid, name, notes, created_at, updated_at) VALUES (?, 'm', ?, 0, 0)`, i, string(make([]byte, 400)))
	}
	file := filepath.Join(e.data, "catalog.db")
	if err := db.Close(e.conn); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	for i := 3 * 4096; i < len(b)-4096; i++ { // keep the header and the first pages readable
		b[i] = byte(i * 13)
	}
	os.WriteFile(file, b, 0o644)
	raw, err := sql.Open("sqlite", file) // without db.Open's own check
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	e.src.DB = raw
	if _, err := backup.Create(context.Background(), e.backups, e.src, backup.ReasonManual, time.Now()); err == nil {
		t.Fatal("snapshot of a damaged catalog succeeded")
	}
	if list, _ := backup.List(e.backups); len(list) != 0 {
		t.Errorf("snapshots: %+v", list)
	}
	if err := backup.QuickCheck(context.Background(), raw); err == nil {
		t.Error("QuickCheck passed a damaged catalog")
	}
}
