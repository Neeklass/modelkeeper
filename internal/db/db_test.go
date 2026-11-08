package db

import (
	"path/filepath"
	"testing"
)

func TestOpenAndFTS5(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer Close(conn)

	var mode string
	if err := conn.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	var fk int
	if err := conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v", fk, err)
	}
	if _, err := conn.Exec(`CREATE VIRTUAL TABLE t USING fts5(name, tokenize='unicode61 remove_diacritics 2', prefix='2 3')`); err != nil {
		t.Fatalf("fts5 unavailable: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO t(name) VALUES ('Ork Krieger M_PART_BASE')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM t WHERE t MATCH 'kri* AND base'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("fts match = %d, %v", n, err)
	}
}
