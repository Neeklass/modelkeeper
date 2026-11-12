package watch

import (
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/db"
)

// A deactivated root (E9) is not watched: no watches, no scans on changes,
// and deactivating a watched root stops watching it.
func TestDeactivatedRootNotWatched(t *testing.T) {
	e := setup(t)
	active, ap := e.addRoot("active", "local", true)
	removed, rp := e.addRoot("removed", "local", true)
	e.db.Exec(`UPDATE root SET removed_at = 1 WHERE id = ?`, removed)

	e.sync()
	if !e.w.Live(active) || e.w.Live(removed) {
		t.Fatalf("live: active %v, deactivated %v", e.w.Live(active), e.w.Live(removed))
	}
	e.expectStarts("catch-up at startup", active)
	write(t, filepath.Join(rp, "Creator", "Orc", "orc.stl"))
	e.expectStarts("change in deactivated root")

	// Deactivating the active root stops watching it.
	e.db.Exec(`UPDATE root SET removed_at = 2 WHERE id = ?`, active)
	e.sync()
	if e.w.Live(active) {
		t.Fatal("deactivated root still watched")
	}
	write(t, filepath.Join(ap, "Creator", "Elf", "elf.stl"))
	e.expectStarts("change after deactivating")
}

// A deactivated NAS root is not rescanned on schedule.
func TestDeactivatedNASNotScheduled(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	scan := &fakeScanner{}
	w, err := New(conn, scan, Options{
		Quiet: 50 * time.Millisecond, ScheduleCheck: 40 * time.Millisecond,
		NASInterval: func() time.Duration { return time.Hour },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := conn.Exec(`INSERT INTO root (path, kind, last_scan_at, removed_at, created_at) VALUES (?, 'nas', 1, 1, 0)`,
		filepath.Join(dir, "nas")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := scan.take(); len(got) != 0 {
		t.Fatalf("deactivated NAS root scheduled: %v", got)
	}
}
