// Package db opens the SQLite catalog (modernc.org/sqlite, no CGO) and
// applies schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrationFS embed.FS

// Open opens (or creates) the catalog at file and migrates it to the latest schema.
func Open(file string) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"foreign_keys(ON)",
		"busy_timeout(5000)",
		"synchronous(NORMAL)",
	} {
		q.Add("_pragma", p)
	}
	conn, err := sql.Open("sqlite", file+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := migrate(context.Background(), conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Close checkpoints the WAL into the main file and closes the database, so
// that after exit only catalog.db remains (concept M: backup by copying).
func Close(conn *sql.DB) error {
	if _, err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		conn.Close()
		return err
	}
	return conn.Close()
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// File names: 0001_description.sql
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: missing version prefix", e.Name())
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", e.Name(), err)
		}
		b, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// migrate applies all migrations newer than PRAGMA user_version, each in its
// own transaction.
func migrate(ctx context.Context, conn *sql.DB) error {
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	var current int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	if n := len(migs); n > 0 && current > migs[n-1].version {
		return fmt.Errorf("catalog schema version %d is newer than this app supports (%d)", current, migs[n-1].version)
	}
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
