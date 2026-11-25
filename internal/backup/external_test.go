package backup_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modelkeeper/internal/backup"
)

// Mirror copies the snapshots into the external folder once, and the copies
// pass the restore test; a damaged copy fails it.
func TestMirrorAndRestoreCheck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		e.exec(`INSERT INTO model (id, uid, name, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`, i, fmt.Sprint("u", i), fmt.Sprint("Model ", i))
	}
	e.exec(`INSERT INTO tag (id, name) VALUES (1, 'orc')`)
	now := time.Now()
	e.create(backup.ReasonManual, now.Add(-time.Hour))
	e.create(backup.ReasonDaily, now)

	ext := t.TempDir()
	target, copied, err := backup.Mirror(e.backups, ext, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 2 {
		t.Fatalf("copied %v", copied)
	}
	if _, err := os.Stat(filepath.Join(target, "README.txt")); err != nil {
		t.Error("README missing")
	}
	// Nothing new: nothing is copied again (README is rewritten).
	if _, copied, err := backup.Mirror(e.backups, ext, now); err != nil || len(copied) != 0 {
		t.Fatalf("second mirror copied %v, %v", copied, err)
	}

	list, err := backup.List(target)
	if err != nil || len(list) != 2 {
		t.Fatalf("external snapshots %d, %v", len(list), err)
	}
	work := t.TempDir()
	r := backup.CheckRestore(ctx, list[0], work, now)
	if !r.OK || r.Models != 3 || r.Tags != 1 {
		t.Fatalf("restore check: %+v", r)
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("work folder not cleaned up: %v", entries)
	}

	// Stored result.
	if err := backup.SaveRestoreCheck(e.data, r); err != nil {
		t.Fatal(err)
	}
	if err := backup.SaveRestoreCheck(e.data, r); err != nil { // overwrite
		t.Fatal(err)
	}
	if got := backup.LastRestoreCheck(e.data); got == nil || got.Snapshot != r.Snapshot || !got.OK {
		t.Errorf("last check %+v", got)
	}

	// A damaged copy (bit rot on the external drive) fails.
	db := filepath.Join(list[1].Path, "catalog.db")
	b, _ := os.ReadFile(db)
	b[len(b)/2] ^= 0xff
	os.Chmod(db, 0o644)
	os.WriteFile(db, b, 0o644)
	r = backup.CheckRestore(ctx, list[1], work, now)
	if r.OK || !strings.Contains(r.Problem, "changed") {
		t.Errorf("damaged copy: %+v", r)
	}
}
