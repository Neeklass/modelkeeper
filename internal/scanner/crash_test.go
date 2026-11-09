package scanner

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/db"
	"modelkeeper/internal/settings"
)

// crashChild runs a scan in a child process that the parent kills hard
// (like a crash or power loss: no cancel, no clean shutdown).
func TestMain(m *testing.M) {
	if dbPath := os.Getenv("MODELKEEPER_CRASH_CHILD"); dbPath != "" {
		conn, err := db.Open(dbPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		opts := Options{HashWorkers: 1, EmitInterval: time.Hour}
		if os.Getenv("MODELKEEPER_CRASH_ONEPASS") != "" {
			opts.ArchiveMemLimit, opts.ResumeMin, opts.CheckpointEvery = 1, 1, time.Nanosecond
		}
		mgr, err := NewManager(conn, settings.Defaults, nil, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		mgr.Start(1)
		mgr.Wait()
		os.Exit(0) // only reached if the parent was too slow
	}
	os.Exit(m.Run())
}

// Every committed hash survives a hard kill; the next scan continues with the
// remaining files instead of starting over.
func TestHardKillKeepsProgress(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "models")
	const files = 400
	for i := 0; i < files; i++ {
		p := filepath.Join(root, fmt.Sprintf("C/M%03d/part.stl", i))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, bytes.Repeat([]byte{byte(i), byte(i >> 8)}, 2<<20), 0o644) // 4 MB each
	}
	dbPath := filepath.Join(dir, "catalog.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.AddRoot(context.Background(), conn, root); err != nil {
		t.Fatal(err)
	}

	hashed := func(c *sql.DB) int {
		var n int
		c.QueryRow(`SELECT count(*) FROM location WHERE file_id IS NOT NULL`).Scan(&n)
		return n
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "MODELKEEPER_CRASH_CHILD="+dbPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Kill once some, but not all, hashes are committed.
	deadline := time.Now().Add(60 * time.Second)
	for hashed(conn) < 20 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	conn.Close() // no checkpoint by the child: the WAL is left as after a crash

	conn, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("catalog does not open after the crash: %v", err)
	}
	defer conn.Close()
	var ok string
	conn.QueryRow(`PRAGMA integrity_check`).Scan(&ok)
	if ok != "ok" {
		t.Fatalf("integrity check: %s", ok)
	}
	before := hashed(conn)
	if before < 20 || before >= files {
		t.Fatalf("killed at %d of %d hashed; test needs a partial state", before, files)
	}
	var running int
	conn.QueryRow(`SELECT count(*) FROM scan_run WHERE status = 'running'`).Scan(&running)

	mgr, err := NewManager(conn, settings.Defaults, nil, Options{EmitInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(1); err != nil {
		t.Fatal(err)
	}
	mgr.Wait()
	p := mgr.Progress()
	if p.Status != StatusDone {
		t.Fatalf("resume scan: %s", p.Status)
	}
	if p.HashDone != files-before {
		t.Errorf("resume hashed %d files, want only the remaining %d", p.HashDone, files-before)
	}
	if n := hashed(conn); n != files {
		t.Errorf("hashed %d of %d", n, files)
	}
	var failed int
	conn.QueryRow(`SELECT count(*) FROM scan_run WHERE status = 'failed' AND error_key = 'scan.error.interrupted'`).Scan(&failed)
	t.Logf("killed after %d/%d hashes committed; interrupted runs marked failed: %d (running before restart: %d)",
		before, files, failed, running)
	if failed != 1 {
		t.Errorf("interrupted run not marked")
	}
}

// A hard kill in the middle of a large zip: the next scan continues inside
// the archive from the last checkpoint and ends with the same result as an
// uninterrupted scan.
func TestHardKillInsideZip(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "models")
	os.MkdirAll(root, 0o755)
	zipData := bigZip(t, 400, 250_000) // ~100 MB
	os.WriteFile(filepath.Join(root, "Pack.zip"), zipData, 0o644)
	dbPath := filepath.Join(dir, "catalog.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.AddRoot(context.Background(), conn, root); err != nil {
		t.Fatal(err)
	}
	offset := func(c *sql.DB) int64 {
		var n int64
		c.QueryRow(`SELECT coalesce(max(offset), 0) FROM hash_progress`).Scan(&n)
		return n
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "MODELKEEPER_CRASH_CHILD="+dbPath, "MODELKEEPER_CRASH_ONEPASS=1")
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for offset(conn) < int64(len(zipData))/4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	conn.Close()

	conn, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("catalog does not open after the crash: %v", err)
	}
	defer conn.Close()
	at := offset(conn)
	if at <= 0 || at >= int64(len(zipData)) {
		t.Fatalf("killed with checkpoint at %d of %d; test needs a partial state", at, len(zipData))
	}
	mgr, err := NewManager(conn, settings.Defaults, nil, Options{EmitInterval: time.Hour, ArchiveMemLimit: 1, ResumeMin: 1})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Start(1)
	mgr.Wait()
	p := mgr.Progress()
	if p.Status != StatusDone {
		t.Fatalf("resume scan: %s", p.Status)
	}
	if p.BytesDone >= int64(len(zipData)) {
		t.Errorf("resume read %d bytes; the whole archive is %d", p.BytesDone, len(zipData))
	}
	ref := newEnv(t)
	ref.write("Pack.zip", zipData)
	ref.scan()
	got := (&env{t: t, db: conn}).archiveState()["Pack.zip"]
	if want := ref.archiveState()["Pack.zip"]; got != want {
		t.Errorf("result after crash and resume differs from an uninterrupted scan")
	}
	t.Logf("killed at checkpoint %d MB of %d MB; resume read %d MB", at>>20, len(zipData)>>20, p.BytesDone>>20)
}
