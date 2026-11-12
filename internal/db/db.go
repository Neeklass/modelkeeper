// Package db opens the SQLite catalog (modernc.org/sqlite, no CGO) and
// applies schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
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

// ErrCorrupt is returned by Open when the catalog file is damaged (PRAGMA
// quick_check fails, or the file is not a database). Nothing was written.
var ErrCorrupt = errors.New("catalog is damaged")

// Options tune Open.
type Options struct {
	// BeforeMigrate is called before migrations are applied to an existing
	// catalog (user_version > 0), with the current and the target schema
	// version and the number of the first pending migration. If it fails,
	// nothing is migrated and Open fails. Used for the pre-migration
	// snapshot (J2). Not called for a new, empty catalog.
	BeforeMigrate func(conn *sql.DB, from, to, next int) error
}

// Open opens (or creates) the catalog at file and migrates it to the latest schema.
func Open(file string) (*sql.DB, error) { return OpenWith(file, Options{}) }

// OpenWith is Open with options. The catalog is checked with PRAGMA
// quick_check first; a damaged catalog yields an error wrapping ErrCorrupt.
func OpenWith(file string, opts Options) (*sql.DB, error) {
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
		return nil, corruptOr(err)
	}
	if err := quickCheck(conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := migrate(context.Background(), conn, opts.BeforeMigrate); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// quickCheck runs PRAGMA quick_check and returns ErrCorrupt with the first
// reported problem.
func quickCheck(conn *sql.DB) error {
	rows, err := conn.Query("PRAGMA quick_check")
	if err != nil {
		return corruptOr(err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return corruptOr(err)
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return corruptOr(err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrCorrupt, problems[0])
	}
	return nil
}

// corruptOr maps SQLite's "database disk image is malformed" (SQLITE_CORRUPT)
// and "file is not a database" (SQLITE_NOTADB) to ErrCorrupt.
func corruptOr(err error) error {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff {
		case 11, 26:
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
	}
	return err
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
// own transaction. before (optional) runs first if an existing catalog is
// about to change.
func migrate(ctx context.Context, conn *sql.DB, before func(conn *sql.DB, from, to, next int) error) error {
	return migrateTo(ctx, conn, 0, before)
}

// migrateTo is migrate up to schema version limit (0 = latest); tests use it
// to create catalogs of an older version.
func migrateTo(ctx context.Context, conn *sql.DB, limit int, before func(conn *sql.DB, from, to, next int) error) error {
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	if latest := len(migs); limit > 0 {
		for latest > 0 && migs[latest-1].version > limit {
			latest--
		}
		migs = migs[:latest]
	}
	var current int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	if n := len(migs); n > 0 && current > migs[n-1].version {
		return fmt.Errorf("catalog schema version %d is newer than this app supports (%d)", current, migs[n-1].version)
	}
	if before != nil && current > 0 {
		for _, m := range migs {
			if m.version > current {
				if err := before(conn, current, migs[len(migs)-1].version, m.version); err != nil {
					return err
				}
				break
			}
		}
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
