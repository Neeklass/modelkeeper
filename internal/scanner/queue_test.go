package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"modelkeeper/internal/catalog"
)

// Queued scans start one after another once the running work is done; the
// same job is not queued twice, and a job whose root was deactivated in the
// meantime is dropped (E9).
func TestScanQueue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("Atlas/Orc/orc.stl", stlA)
	second := filepath.Join(t.TempDir(), "second")
	os.MkdirAll(filepath.Join(second, "Elf"), 0o755)
	os.WriteFile(filepath.Join(second, "Elf", "elf.stl"), stlB, 0o644)
	r2, err := catalog.AddRoot(ctx, e.db, second, "")
	if err != nil {
		t.Fatal(err)
	}
	third := filepath.Join(t.TempDir(), "third")
	os.MkdirAll(third, 0o755)
	r3, err := catalog.AddRoot(ctx, e.db, third, "")
	if err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	blocked := make(chan struct{})
	go e.mgr.Exclusive(func(context.Context) error { // keeps the manager busy
		close(blocked)
		<-release
		return nil
	})
	<-blocked
	e.mgr.EnqueueScan(e.id)
	e.mgr.EnqueueScan(e.id) // duplicate
	e.mgr.EnqueueScan(r2.ID)
	e.mgr.EnqueueScan(r3.ID)
	e.mgr.mu.Lock()
	queued := len(e.mgr.queue)
	e.mgr.mu.Unlock()
	expect(t, "queued", queued, 3)
	catalog.DeactivateRoot(ctx, e.db, r3.ID)

	close(release)
	e.mgr.WaitIdle()
	expect(t, "scans of root 1", e.int(`SELECT count(*) FROM scan_run WHERE root_id = ? AND status = 'done'`, e.id), 1)
	expect(t, "scans of root 2", e.int(`SELECT count(*) FROM scan_run WHERE root_id = ? AND status = 'done'`, r2.ID), 1)
	expect(t, "scans of the deactivated root", e.int(`SELECT count(*) FROM scan_run WHERE root_id = ?`, r3.ID), 0)
	expect(t, "models", e.int(`SELECT count(*) FROM model`), 2)

	// Idle: a queued job starts right away.
	e.mgr.EnqueueRedetect(e.id)
	e.mgr.WaitIdle()
	expect(t, "redetect ran", e.mgr.Progress().Mode, ModeDetect)

	// Close drops what is queued.
	e.mgr.Close()
	e.mgr.EnqueueScan(e.id)
	e.mgr.WaitIdle()
	expect(t, "no scan after Close", e.int(`SELECT count(*) FROM scan_run WHERE root_id = ? AND status = 'done'`, e.id), 1)
}
