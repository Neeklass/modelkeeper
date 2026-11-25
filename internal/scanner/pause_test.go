package scanner

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/settings"
)

// Pausing stops the running scan and keeps what was hashed; a paused root is
// not scanned by queued (automatic) scans or at start, and resuming finishes
// the work (F1, F3).
func TestPauseAndResume(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		e.write(filepath.ToSlash(filepath.Join("m", string(rune('a'+i%26))+string(rune('a'+i/26))+".stl")),
			bytes.Repeat([]byte{byte(i)}, 1<<20))
	}
	if err := e.mgr.Start(e.id); err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.PauseRoot(e.id); err != nil {
		t.Fatal(err)
	}
	e.mgr.Wait()
	if !catalog.ScanPaused(ctx, e.db, e.id) {
		t.Fatal("root not paused")
	}
	r, err := catalog.GetRoot(ctx, e.db, e.id)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "Root.ScanPaused", r.ScanPaused, true)
	runs := e.int(`SELECT count(*) FROM scan_run WHERE root_id = ?`, e.id)

	// Automatic scans skip the paused root.
	e.mgr.EnqueueScan(e.id)
	e.mgr.WaitIdle()
	if err := e.mgr.ResumeUnfinished(ctx); err != nil {
		t.Fatal(err)
	}
	e.mgr.WaitIdle()
	expect(t, "no automatic scan while paused", e.int(`SELECT count(*) FROM scan_run WHERE root_id = ?`, e.id), runs)
	// Detection still runs: it reads no files.
	e.mgr.EnqueueRedetect(e.id)
	e.mgr.WaitIdle()
	expect(t, "detection ran", e.mgr.Progress().Mode, ModeDetect)

	// Resuming clears the pause and finishes the work.
	if err := e.mgr.ResumeRoot(e.id); err != nil {
		t.Fatal(err)
	}
	e.mgr.WaitIdle()
	expect(t, "resumed scan done", e.mgr.Progress().Status, StatusDone)
	expect(t, "pause cleared", catalog.ScanPaused(ctx, e.db, e.id), false)
	expect(t, "all hashed", e.int(`SELECT count(*) FROM location WHERE file_id IS NULL`), 0)
}

// A scan that stopped because the app was closed (cancelled) or crashed
// (interrupted) continues at the next start; a finished one does not.
func TestResumeUnfinished(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("Atlas/Orc/orc.stl", stlA)
	e.scan()
	done := e.int(`SELECT count(*) FROM scan_run WHERE root_id = ?`, e.id)

	if err := e.mgr.ResumeUnfinished(ctx); err != nil {
		t.Fatal(err)
	}
	e.mgr.WaitIdle()
	expect(t, "finished scan is not repeated", e.int(`SELECT count(*) FROM scan_run WHERE root_id = ?`, e.id), done)

	// Simulate a crash during a scan: NewManager marks the run interrupted.
	e.db.Exec(`INSERT INTO scan_run (root_id, mode, status, started_at) VALUES (?, 'incremental', 'running', 1)`, e.id)
	mgr, err := NewManager(e.db, settings.Defaults, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.ResumeUnfinished(ctx); err != nil {
		t.Fatal(err)
	}
	mgr.WaitIdle()
	expect(t, "interrupted scan resumed", e.int(`SELECT count(*) FROM scan_run WHERE root_id = ? AND status = 'done'`, e.id), done+1)

	// unfinished: which run states count.
	expect(t, "cancelled", unfinished(StatusCancelled, ""), true)
	expect(t, "interrupted", unfinished(StatusFailed, "scan.error.interrupted"), true)
	expect(t, "other failure", unfinished(StatusFailed, "scan.error.database"), false)
	expect(t, "offline", unfinished(StatusOffline, "scan.error.offline"), false)
	expect(t, "done", unfinished(StatusDone, ""), false)
}

// Interrupting stops a running scan for an action in between and lets the
// scan continue right after it; without a running scan it is like Exclusive.
func TestInterrupting(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 40; i++ {
		e.write(filepath.ToSlash(filepath.Join("m", string(rune('a'+i%26))+string(rune('a'+i/26))+".stl")),
			bytes.Repeat([]byte{byte(i + 1)}, 1<<20))
	}
	if err := e.mgr.Start(e.id); err != nil {
		t.Fatal(err)
	}
	ran := false
	err := e.mgr.Interrupting(func(ctx context.Context) error {
		ran = true
		// Nothing else may start meanwhile.
		if err := e.mgr.Start(e.id); err == nil {
			t.Error("a scan started inside Interrupting")
		}
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("Interrupting: ran=%v, %v", ran, err)
	}
	e.mgr.WaitIdle()
	expect(t, "scan finished after the interruption", e.mgr.Progress().Status, StatusDone)
	expect(t, "all hashed", e.int(`SELECT count(*) FROM location WHERE file_id IS NULL`), 0)

	// Idle: runs right away, nothing is started afterwards.
	runs := e.int(`SELECT count(*) FROM scan_run`)
	if err := e.mgr.Interrupting(func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	e.mgr.WaitIdle()
	expect(t, "no extra scan", e.int(`SELECT count(*) FROM scan_run`), runs)
}
