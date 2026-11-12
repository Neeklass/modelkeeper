package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/backup"
)

func latestVersion(t *testing.T) int {
	t.Helper()
	migs, err := loadMigrations()
	if err != nil || len(migs) < 2 {
		t.Fatalf("migrations: %v", err)
	}
	return migs[len(migs)-1].version
}

// olderCatalog creates a catalog on the schema before the newest migration,
// with some content.
func olderCatalog(t *testing.T, file string) int {
	t.Helper()
	older := latestVersion(t) - 1
	conn, err := sql.Open("sqlite", file+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateTo(context.Background(), conn, older, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO model (id, uid, name, created_at, updated_at) VALUES (1, 'u1', 'Orc', 0, 0)`,
		`INSERT INTO tag (id, name) VALUES (1, 'orc')`,
		`INSERT INTO model_tag (model_id, tag_id) VALUES (1, 1)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := Close(conn); err != nil {
		t.Fatal(err)
	}
	return older
}

func userVersion(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var v int
	if err := conn.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The pre-migration snapshot is taken before any migration writes (J2), and
// holds the catalog on its old schema version.
func TestSnapshotBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "catalog.db")
	older := olderCatalog(t, file)
	backups := filepath.Join(dir, "backups")

	calls := 0
	conn, err := OpenWith(file, Options{BeforeMigrate: func(c *sql.DB, from, to, next int) error {
		calls++
		if from != older || to != older+1 || next != older+1 {
			t.Errorf("BeforeMigrate(from %d, to %d, next %d), want %d, %d, %d", from, to, next, older, older+1, older+1)
		}
		if v := userVersion(t, c); v != older {
			t.Errorf("user_version in hook = %d, want %d (not migrated yet)", v, older)
		}
		_, err := backup.Create(context.Background(), backups, backup.Source{DB: c}, backup.PreMigration(next), time.Now())
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer Close(conn)
	if calls != 1 {
		t.Fatalf("BeforeMigrate called %d times", calls)
	}
	if v := userVersion(t, conn); v != older+1 {
		t.Errorf("migrated user_version = %d", v)
	}
	s, ok := backup.Latest(backups)
	if !ok {
		t.Fatal("no snapshot")
	}
	if s.Manifest.Schema != older || s.Manifest.Models != 1 || s.Manifest.Tags != 1 {
		t.Errorf("snapshot manifest = %+v", s.Manifest)
	}
	if s.Manifest.Reason != backup.PreMigration(older+1) {
		t.Errorf("reason = %q", s.Manifest.Reason)
	}

	// Already on the latest schema: nothing to do, no hook.
	Close(conn)
	conn, err = OpenWith(file, Options{BeforeMigrate: func(*sql.DB, int, int, int) error {
		t.Error("BeforeMigrate called without pending migrations")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	Close(conn)
}

func TestNoSnapshotForNewCatalog(t *testing.T) {
	conn, err := OpenWith(filepath.Join(t.TempDir(), "catalog.db"), Options{BeforeMigrate: func(*sql.DB, int, int, int) error {
		t.Error("BeforeMigrate called for a new catalog")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	Close(conn)
}

// A failing pre-migration snapshot leaves the catalog untouched.
func TestFailedSnapshotBlocksMigration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "catalog.db")
	older := olderCatalog(t, file)
	boom := errors.New("disk full")
	if _, err := OpenWith(file, Options{BeforeMigrate: func(*sql.DB, int, int, int) error { return boom }}); !errors.Is(err, boom) {
		t.Fatalf("OpenWith = %v, want %v", err, boom)
	}
	conn, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if v := userVersion(t, conn); v != older {
		t.Errorf("user_version = %d, want %d", v, older)
	}
}

// corrupt overwrites the catalog's pages after the header.
func corrupt(t *testing.T, file string) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 3*4096 {
		t.Fatalf("catalog too small to corrupt: %d bytes", len(b))
	}
	for i := 4096; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDetectsDamage(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "catalog.db")
	conn, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := Close(conn); err != nil {
		t.Fatal(err)
	}
	corrupt(t, file)
	if _, err := Open(file); !errors.Is(err, ErrCorrupt) {
		t.Errorf("damaged pages: Open = %v, want ErrCorrupt", err)
	}

	garbage := filepath.Join(dir, "garbage.db")
	os.WriteFile(garbage, []byte("this is not an SQLite database, just some text that is long enough....................................................."), 0o644)
	if _, err := Open(garbage); !errors.Is(err, ErrCorrupt) {
		t.Errorf("not a database: Open = %v, want ErrCorrupt", err)
	}
}
