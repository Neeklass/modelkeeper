// Package health is the maintenance check of the data folder (TODO 5.3): the
// catalog's integrity, compacting it, thumbnails no model uses any more, and
// the free space of the drive. It never touches model files, and it changes
// the catalog only when the integrity check passed.
package health

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OrphanDir is the folder below the thumbnail folder that thumbnails no
// model uses are moved into; they are deleted after OrphanKeep.
const OrphanDir = ".unused"

// OrphanKeep: unused thumbnails are kept this long before they are deleted
// (a restore of an older snapshot may still want a generated one; it would
// be generated again anyway).
const OrphanKeep = 30 * 24 * time.Hour

// Warning thresholds for the drive of the data folder.
const (
	LowSpaceBytes   = 2 << 30 // 2 GB
	LowSpacePercent = 5
)

// Report is the result of a check.
type Report struct {
	Time        int64  `json:"time"` // unix ms
	DurationMs  int64  `json:"durationMs"`
	IntegrityOK bool   `json:"integrityOk"`
	Problem     string `json:"problem,omitempty"` // integrity_check output, if not ok
	DBBytes     int64  `json:"dbBytes"`           // catalog size after the check
	Freed       int64  `json:"freed"`             // bytes freed by compacting
	Compacted   bool   `json:"compacted"`
	// Thumbnails: files in the thumbnail folder, of which Orphaned were moved
	// aside now (no model uses them) and Purged deleted (moved aside earlier).
	Thumbnails     int   `json:"thumbnails"`
	ThumbnailBytes int64 `json:"thumbnailBytes"`
	Orphaned       int   `json:"orphaned"`
	Purged         int   `json:"purged"`
	// Free space of the drive of the data folder (-1: unknown).
	FreeBytes  int64 `json:"freeBytes"`
	TotalBytes int64 `json:"totalBytes"`
	LowSpace   bool  `json:"lowSpace"`
}

// OK: nothing needs the user's attention.
func (r Report) OK() bool { return r.IntegrityOK && !r.LowSpace }

// Options of a check.
type Options struct {
	DataDir  string // the drive of this folder is checked
	ThumbDir string // data/thumbnails
	Now      time.Time
}

// Check runs the full check on the open catalog db. The caller makes sure no
// scan writes at the same time (compacting needs the catalog for itself).
func Check(ctx context.Context, db *sql.DB, catalogFile string, o Options) (Report, error) {
	start := time.Now()
	if o.Now.IsZero() {
		o.Now = start
	}
	r := Report{Time: o.Now.UnixMilli(), FreeBytes: -1, TotalBytes: -1}
	defer func() { r.DurationMs = time.Since(start).Milliseconds() }()

	problem, err := integrity(ctx, db)
	if err != nil {
		return r, err
	}
	r.IntegrityOK, r.Problem = problem == "", problem
	sizeBefore := fileSize(catalogFile)
	if r.IntegrityOK {
		// Compact only when it is worth it: a quarter of the pages are free.
		var free, total int64
		db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free)
		db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&total)
		if total > 0 && free*4 >= total {
			if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
				return r, fmt.Errorf("compact: %w", err)
			}
			r.Compacted = true
		}
		db.ExecContext(ctx, `PRAGMA optimize`)
		if o.ThumbDir != "" {
			if err := thumbnails(ctx, db, o, &r); err != nil {
				return r, fmt.Errorf("thumbnails: %w", err)
			}
		}
	}
	r.DBBytes = fileSize(catalogFile)
	if r.Compacted && sizeBefore > r.DBBytes {
		r.Freed = sizeBefore - r.DBBytes
	}
	if o.DataDir != "" {
		if free, total, err := diskSpace(o.DataDir); err == nil {
			r.FreeBytes, r.TotalBytes = free, total
			r.LowSpace = free < LowSpaceBytes || (total > 0 && free*100 < total*LowSpacePercent)
		}
	}
	return r, nil
}

// integrity runs the full integrity check; "" means ok.
func integrity(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check(20)`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		lines = append(lines, s)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(lines) == 1 && lines[0] == "ok" {
		return "", nil
	}
	return strings.Join(lines, "\n"), nil
}

// thumbnails moves thumbnail files no model uses into OrphanDir and deletes
// those moved there more than OrphanKeep ago. Only *.jpg files directly in
// the folder are considered (the app's own thumbnails).
func thumbnails(ctx context.Context, db *sql.DB, o Options, r *Report) error {
	used := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT thumb_file FROM model WHERE coalesce(thumb_file, '') <> ''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			rows.Close()
			return err
		}
		used[strings.ToLower(filepath.Base(f))] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(o.ThumbDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Nothing is used at all, but there are files: something is wrong
	// (e.g. a catalog without thumbnails was opened); leave them alone.
	safe := len(used) > 0
	aside := filepath.Join(o.ThumbDir, OrphanDir)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.EqualFold(filepath.Ext(name), ".jpg") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		// Used, or written just now (a thumbnail being saved while the check
		// runs): kept.
		if used[strings.ToLower(name)] || !safe || o.Now.Sub(fi.ModTime()) < time.Hour {
			r.Thumbnails++
			r.ThumbnailBytes += fi.Size()
			continue
		}
		if err := os.MkdirAll(aside, 0o755); err != nil {
			return err
		}
		dst := filepath.Join(aside, name)
		if err := os.Rename(filepath.Join(o.ThumbDir, name), dst); err != nil {
			return err
		}
		os.Chtimes(dst, o.Now, o.Now) // the age counts from now
		r.Orphaned++
	}
	old, err := os.ReadDir(aside)
	if err != nil {
		return nil
	}
	for _, e := range old {
		fi, err := e.Info()
		if err != nil || e.IsDir() || o.Now.Sub(fi.ModTime()) < OrphanKeep {
			continue
		}
		if os.Remove(filepath.Join(aside, e.Name())) == nil {
			r.Purged++
		}
	}
	return nil
}

func fileSize(p string) int64 {
	var n int64
	for _, f := range []string{p, p + "-wal"} {
		if fi, err := os.Stat(f); err == nil {
			n += fi.Size()
		}
	}
	return n
}
