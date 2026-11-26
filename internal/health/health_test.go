package health_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modelkeeper/internal/db"
	"modelkeeper/internal/health"
)

func TestCheck(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "catalog.db")
	conn, err := db.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(conn)
	thumbs := filepath.Join(dir, "thumbnails")
	os.MkdirAll(thumbs, 0o755)
	for i := 0; i < 2000; i++ {
		conn.Exec(`INSERT INTO model (uid, name, notes, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`,
			fmt.Sprint("u", i), fmt.Sprint("m", i), strings.Repeat("x", 2000))
	}
	conn.Exec(`UPDATE model SET thumb_file = 'u1.jpg', thumb_source = 'manual' WHERE uid = 'u1'`)
	conn.Exec(`DELETE FROM model WHERE uid <> 'u1'`) // many free pages
	for _, n := range []string{"u1.jpg", "gone.jpg", "notes.txt"} {
		os.WriteFile(filepath.Join(thumbs, n), []byte("x"), 0o644)
	}
	now := time.Now()
	r, err := health.Check(ctx, conn, file, health.Options{DataDir: dir, ThumbDir: thumbs, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !r.IntegrityOK || !r.Compacted || r.Orphaned != 1 || r.Thumbnails != 1 {
		t.Errorf("report %+v", r)
	}
	if _, err := os.Stat(filepath.Join(thumbs, "u1.jpg")); err != nil {
		t.Error("used thumbnail moved")
	}
	if _, err := os.Stat(filepath.Join(thumbs, "notes.txt")); err != nil {
		t.Error("other file touched")
	}
	if _, err := os.Stat(filepath.Join(thumbs, health.OrphanDir, "gone.jpg")); err != nil {
		t.Error("unused thumbnail not moved aside")
	}
	// Kept for a while, then deleted.
	r, _ = health.Check(ctx, conn, file, health.Options{ThumbDir: thumbs, Now: now.Add(time.Hour)})
	if r.Purged != 0 {
		t.Error("purged too early")
	}
	r, _ = health.Check(ctx, conn, file, health.Options{ThumbDir: thumbs, Now: now.Add(health.OrphanKeep + time.Hour)})
	if r.Purged != 1 {
		t.Errorf("not purged: %+v", r)
	}
	var n int
	conn.QueryRow(`SELECT count(*) FROM model`).Scan(&n)
	if n != 1 {
		t.Errorf("models %d", n)
	}
}

// With no model using a thumbnail, nothing is moved (a wrong catalog opened).
func TestCheckLeavesThumbnailsWithoutCatalog(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "catalog.db")
	conn, err := db.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(conn)
	thumbs := filepath.Join(dir, "thumbnails")
	os.MkdirAll(thumbs, 0o755)
	os.WriteFile(filepath.Join(thumbs, "a.jpg"), []byte("x"), 0o644)
	r, err := health.Check(context.Background(), conn, file, health.Options{ThumbDir: thumbs})
	if err != nil || r.Orphaned != 0 {
		t.Errorf("%+v %v", r, err)
	}
}
