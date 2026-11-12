package backup_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/backup"
	"modelkeeper/internal/db"
)

func (e *env) target() backup.Target {
	return backup.Target{
		Catalog: filepath.Join(e.data, "catalog.db"), Settings: e.src.Settings,
		Thumbnails: e.src.Thumbnails, Backups: e.backups,
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func modelCount(t *testing.T, file string) int {
	t.Helper()
	conn, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return count(t, conn, `SELECT count(*) FROM model`)
}

// restoreEnv: a snapshot with 1 model, a manual thumbnail and German
// settings; afterwards the catalog has 3 models, English settings and
// another version of the thumbnail. The catalog is closed.
func restoreEnv(t *testing.T) (*env, backup.Snapshot) {
	e := newEnv(t)
	e.exec(`INSERT INTO model (id, uid, name, thumb_source, thumb_file, created_at, updated_at) VALUES (1, 'u1', 'Orc', 'manual', 'u1.jpg', 0, 0)`)
	os.WriteFile(filepath.Join(e.src.Thumbnails, "u1.jpg"), []byte("old thumb"), 0o644)
	s := e.create(backup.ReasonManual, time.Now().Add(-time.Hour))

	e.exec(`INSERT INTO model (uid, name, created_at, updated_at) VALUES ('u2', 'Elf', 0, 0), ('u3', 'Dwarf', 0, 0)`)
	os.WriteFile(filepath.Join(e.src.Thumbnails, "u1.jpg"), []byte("new thumb"), 0o644)
	os.WriteFile(e.src.Settings, []byte(`{"language":"en"}`), 0o644)
	if err := db.Close(e.conn); err != nil { // the app restores before opening the catalog
		t.Fatal(err)
	}
	return e, s
}

func TestRestore(t *testing.T) {
	e, s := restoreEnv(t)
	tg := e.target()
	ctx := context.Background()
	if _, ok := backup.PendingRestore(tg); ok {
		t.Fatal("pending restore without request")
	}
	if err := backup.RequestRestore(ctx, tg, s.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	if name, ok := backup.PendingRestore(tg); !ok || name != s.Name {
		t.Fatalf("PendingRestore = %q, %v", name, ok)
	}

	r, err := backup.ApplyPendingRestore(ctx, tg, "test", time.Now())
	if err != nil || r == nil {
		t.Fatalf("ApplyPendingRestore = %+v, %v", r, err)
	}
	if n := modelCount(t, tg.Catalog); n != 1 {
		t.Errorf("restored catalog has %d models, want 1", n)
	}
	if got := readFile(t, tg.Settings); got != `{"language":"de"}` {
		t.Errorf("settings = %s", got)
	}
	if got := readFile(t, filepath.Join(tg.Thumbnails, "u1.jpg")); got != "old thumb" {
		t.Errorf("thumbnail = %s", got)
	}
	for _, f := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(tg.Catalog + f); err == nil {
			t.Errorf("catalog.db%s of the replaced catalog left in place", f)
		}
	}
	if _, ok := backup.PendingRestore(tg); ok {
		t.Error("request not consumed")
	}

	// The replaced state is a snapshot of its own and can be restored back.
	prev := r.Previous
	if prev.Manifest.Reason != backup.ReasonPreRestore || prev.Manifest.Models != 3 || !prev.Manifest.Verified {
		t.Errorf("pre-restore manifest = %+v", prev.Manifest)
	}
	if got := readFile(t, filepath.Join(prev.Path, "settings.json")); got != `{"language":"en"}` {
		t.Errorf("pre-restore settings = %s", got)
	}
	if got := readFile(t, filepath.Join(prev.Path, "thumbnails", "u1.jpg")); got != "new thumb" {
		t.Errorf("pre-restore thumbnail = %s", got)
	}
	if err := backup.RequestRestore(ctx, tg, prev.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.ApplyPendingRestore(ctx, tg, "test", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := modelCount(t, tg.Catalog); n != 3 {
		t.Errorf("after undoing the restore: %d models, want 3", n)
	}
	conn, err := db.Open(tg.Catalog) // opens and passes the integrity check
	if err != nil {
		t.Fatal(err)
	}
	db.Close(conn)
}

func TestRestoreRefusesDamagedSnapshot(t *testing.T) {
	e, s := restoreEnv(t)
	tg := e.target()
	ctx := context.Background()
	if err := backup.RequestRestore(ctx, tg, s.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Damaged after the request.
	file := filepath.Join(s.Path, "catalog.db")
	b, _ := os.ReadFile(file)
	for i := 4096; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	os.WriteFile(file, b, 0o644)

	before := readFile(t, tg.Settings)
	if _, err := backup.ApplyPendingRestore(ctx, tg, "test", time.Now()); err == nil {
		t.Fatal("damaged snapshot restored")
	}
	if n := modelCount(t, tg.Catalog); n != 3 {
		t.Errorf("catalog changed: %d models", n)
	}
	if readFile(t, tg.Settings) != before {
		t.Error("settings changed")
	}
	if _, ok := backup.PendingRestore(tg); ok {
		t.Error("failed request kept: it would be retried at every start")
	}
	if err := backup.RequestRestore(ctx, tg, s.Name, time.Now()); err == nil {
		t.Error("request for a damaged snapshot accepted")
	}
	if err := backup.RequestRestore(ctx, tg, "2026-01-01_000000_manual", time.Now()); err == nil {
		t.Error("request for an unknown snapshot accepted")
	}
}

func TestCancelRestore(t *testing.T) {
	e, s := restoreEnv(t)
	tg := e.target()
	backup.RequestRestore(context.Background(), tg, s.Name, time.Now())
	if err := backup.CancelRestore(tg); err != nil {
		t.Fatal(err)
	}
	if r, err := backup.ApplyPendingRestore(context.Background(), tg, "test", time.Now()); r != nil || err != nil {
		t.Errorf("cancelled restore applied: %+v, %v", r, err)
	}
	if n := modelCount(t, tg.Catalog); n != 3 {
		t.Errorf("catalog changed: %d models", n)
	}
}

// If installing the snapshot fails halfway, the previous state is put back.
func TestRestoreRollsBack(t *testing.T) {
	e, s := restoreEnv(t)
	tg := e.target()
	ctx := context.Background()
	if err := backup.RequestRestore(ctx, tg, s.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The thumbnail folder cannot be written: installing fails after the
	// catalog and the settings were already replaced.
	os.RemoveAll(tg.Thumbnails)
	os.WriteFile(tg.Thumbnails, []byte("in the way"), 0o644)

	if _, err := backup.ApplyPendingRestore(ctx, tg, "test", time.Now()); err == nil {
		t.Fatal("restore succeeded")
	}
	if n := modelCount(t, tg.Catalog); n != 3 {
		t.Errorf("catalog not put back: %d models", n)
	}
	if got := readFile(t, tg.Settings); got != `{"language":"en"}` {
		t.Errorf("settings not put back: %s", got)
	}
	list, _ := backup.List(tg.Backups)
	for _, sn := range list {
		if sn.Manifest.Reason == backup.ReasonPreRestore {
			t.Errorf("empty pre-restore snapshot left: %s", sn.Name)
		}
	}
}
