package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// offlineError signals that the root vanished while scanning (NAS dropped).
type offlineError struct{ err error }

func (e *offlineError) Error() string { return "root offline: " + e.err.Error() }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func rootReachable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// skipDirs are system folders that never contain models.
var skipDirs = map[string]bool{
	"$recycle.bin":              true,
	"system volume information": true,
	"#recycle":                  true,
	"@eadir":                    true,
}

// knownLoc is a disk location already in the catalog.
type knownLoc struct {
	id      int64
	size    int64
	mtime   int64
	present bool
	seen    bool
}

// walkBatchSize is the number of row changes per transaction.
const walkBatchSize = 2000

// walk lists the root and reconciles it with the catalog (F2).
func (s *scan) walk(ctx context.Context) error {
	known, err := s.loadKnown(ctx)
	if err != nil {
		return err
	}

	b := newBatch(s.db)
	var failedDirs []string // rel paths of unreadable folders; entries below are not marked missing
	now := nowMs()
	seen, fresh, changed := 0, 0, 0

	walkErr := filepath.WalkDir(s.root.Path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel := relPath(s.root.Path, p)
		if err != nil {
			if rel == "" {
				return &offlineError{err}
			}
			if d == nil || d.IsDir() {
				failedDirs = append(failedDirs, rel)
			}
			s.issue(0, "read_error", "scan.issue.readError", "path", rel, "detail", err.Error())
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if rel != "" && skipDirs[strings.ToLower(d.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlinks, devices
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(d.Name()), "."))
		cat := s.st.ExtCategory(ext)
		if cat == "" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			s.issue(0, "read_error", "scan.issue.readError", "path", rel, "detail", err.Error())
			return nil
		}
		size, mtime := info.Size(), info.ModTime().UnixNano()
		seen++

		k, ok := known[rel]
		switch {
		case !ok:
			fresh++
			var archStatus any
			if cat == "archive" {
				archStatus = "pending"
			}
			b.add(`INSERT INTO location (root_id, path, ext, category, size, mtime, archive_status,
				present, last_seen_scan, first_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
				s.root.ID, rel, ext, cat, size, mtime, archStatus, s.runID, now)
		case k.size != size || k.mtime != mtime:
			k.seen = true
			changed++
			// Content changed: forget hash and archive members; phase 2/3 redo them.
			b.add(`DELETE FROM location WHERE archive_id = ?`, k.id)
			b.add(`UPDATE location SET size = ?, mtime = ?, file_id = NULL, present = 1, missing_since = NULL,
				archive_status = CASE WHEN category = 'archive' THEN 'pending' END, archive_error = NULL
				WHERE id = ?`, size, mtime, k.id)
		default:
			k.seen = true
			if !k.present {
				b.add(`UPDATE location SET present = 1, missing_since = NULL WHERE id = ? OR archive_id = ?`, k.id, k.id)
			}
		}
		if b.len() >= walkBatchSize {
			if err := b.flush(ctx); err != nil {
				return err
			}
		}
		s.t.update(func(pr *Progress) {
			pr.FilesSeen, pr.FilesNew, pr.FilesChanged, pr.Current = seen, fresh, changed, rel
		})
		return nil
	})
	if walkErr != nil {
		// Keep what we have; a cancelled or broken walk never marks anything missing.
		if err := b.flush(context.Background()); err != nil {
			return err
		}
		return walkErr
	}

	// Missing: in the catalog, not seen now, not below an unreadable folder.
	missing := 0
	for rel, k := range known {
		if k.seen || !k.present || underAny(rel, failedDirs) {
			continue
		}
		missing++
		b.add(`UPDATE location SET present = 0, missing_since = ? WHERE id = ? OR archive_id = ?`, now, k.id, k.id)
		if b.len() >= walkBatchSize {
			if err := b.flush(ctx); err != nil {
				return err
			}
		}
	}
	b.add(`UPDATE location SET last_seen_scan = ? WHERE root_id = ? AND present = 1`, s.runID, s.root.ID)
	if err := b.flush(ctx); err != nil {
		return err
	}
	s.t.update(func(pr *Progress) { pr.FilesMissing = missing })
	return nil
}

func (s *scan) loadKnown(ctx context.Context) (map[string]*knownLoc, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, path, size, mtime, present FROM location
		WHERE root_id = ? AND archive_id IS NULL`, s.root.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]*knownLoc{}
	for rows.Next() {
		var k knownLoc
		var p string
		if err := rows.Scan(&k.id, &p, &k.size, &k.mtime, &k.present); err != nil {
			return nil, err
		}
		known[p] = &k
	}
	return known, rows.Err()
}

// relPath returns p relative to root with '/' separators ("" for the root itself).
func relPath(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func underAny(rel string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// batch collects statements and runs them in one transaction.
type batch struct {
	db    *sql.DB
	stmts []stmt
}

type stmt struct {
	q    string
	args []any
}

func newBatch(db *sql.DB) *batch { return &batch{db: db} }

func (b *batch) add(q string, args ...any) { b.stmts = append(b.stmts, stmt{q, args}) }
func (b *batch) len() int                  { return len(b.stmts) }

func (b *batch) flush(ctx context.Context) error {
	if len(b.stmts) == 0 {
		return nil
	}
	if err := withRetry(ctx, b.flushOnce); err != nil {
		return err
	}
	b.stmts = b.stmts[:0]
	return nil
}

func (b *batch) flushOnce(ctx context.Context) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, st := range b.stmts {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// retryDelays are the pauses between attempts of a failed write transaction.
// Another program (indexer, antivirus, backup) can briefly lock the catalog;
// every attempt is a fresh transaction, so a retry never writes twice.
var retryDelays = []time.Duration{200 * time.Millisecond, time.Second, 3 * time.Second}

func withRetry(ctx context.Context, fn func(context.Context) error) error {
	err := fn(ctx)
	for _, d := range retryDelays {
		if err == nil || ctx.Err() != nil {
			return err
		}
		log.Printf("catalog write failed, retrying in %v: %v", d, err)
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
		err = fn(ctx)
	}
	return err
}
