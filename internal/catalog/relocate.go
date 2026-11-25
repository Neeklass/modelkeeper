package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"modelkeeper/internal/msg"
	"modelkeeper/internal/pathutil"
)

// Changing the path of a root folder (E2, E9): the files moved to another
// place as a whole – a new NAS, another drive letter, \\server\share instead
// of a mapped drive. The root keeps its ID, so everything the catalog knows
// below it (models and their places, corrections, hashes) stays linked and
// nothing has to be read again. Adding the new place as a new root instead
// would leave tags and corrections on the old one.

// RelocateCheck is the result of looking for a root's files at a new path.
type RelocateCheck struct {
	Path    string `json:"path"`    // the new path, cleaned
	Sampled int    `json:"sampled"` // files of the catalog looked for
	Found   int    `json:"found"`   // of these: present at the new path with the same size
	// SameTime: of the found ones, also with the same modification time; the
	// others are read again by the next scan (their hashes then confirm them).
	SameTime int `json:"sameTime"`
	// Examples of files that were not found (relative paths), for the dialog.
	Missing []string `json:"missing"`
	// Total present files of the root in the catalog.
	Total int `json:"total"`
}

// relocateSample is the number of files CheckRelocate looks for.
const relocateSample = 200

// checkNewPath validates the new path of root id.
func checkNewPath(ctx context.Context, db *sql.DB, id int64, path, dataDir string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return "", msg.Wrap(err, "error.rootInvalid", "path", path)
	}
	abs = filepath.Clean(abs)
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", msg.Wrap(err, "error.rootNotFolder", "path", abs)
	}
	if dataDir != "" && pathutil.Overlap(abs, dataDir) {
		return "", msg.Wrap(nil, "error.rootContainsData", "path", abs, "data", dataDir)
	}
	all, err := listRoots(ctx, db, ``)
	if err != nil {
		return "", err
	}
	for _, r := range all {
		if r.ID == id || !pathutil.Overlap(r.Path, abs) {
			continue
		}
		if r.RemovedAt != nil {
			return "", msg.Wrap(nil, "error.rootOverlapRemoved", "path", abs, "other", r.Path)
		}
		return "", msg.Wrap(nil, "error.relocateOverlap", "path", abs, "other", r.Path)
	}
	return abs, nil
}

// CheckRelocate looks for a sample of the root's files (spread over the whole
// folder) at the new path. Nothing is changed.
func CheckRelocate(ctx context.Context, db *sql.DB, id int64, path, dataDir string) (RelocateCheck, error) {
	var c RelocateCheck
	if _, err := GetRoot(ctx, db, id); err != nil {
		return c, err
	}
	abs, err := checkNewPath(ctx, db, id, path, dataDir)
	if err != nil {
		return c, err
	}
	c.Path, c.Missing = abs, []string{}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM location
		WHERE root_id = ? AND archive_id IS NULL AND present = 1`, id).Scan(&c.Total); err != nil {
		return c, err
	}
	if c.Total == 0 {
		return c, nil
	}
	// Every n-th file in path order: spread over all folders, and stable.
	step := max(1, c.Total/relocateSample)
	rows, err := db.QueryContext(ctx, `SELECT path, size, mtime FROM (
			SELECT path, size, mtime, row_number() OVER (ORDER BY path) AS n FROM location
			WHERE root_id = ? AND archive_id IS NULL AND present = 1)
		WHERE (n - 1) % ? = 0 LIMIT ?`, id, step, relocateSample)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var rel string
		var size, mtime int64
		if err := rows.Scan(&rel, &size, &mtime); err != nil {
			return c, err
		}
		c.Sampled++
		fi, err := os.Stat(joinRoot(abs, rel))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != size {
			if len(c.Missing) < 5 {
				c.Missing = append(c.Missing, rel)
			}
			continue
		}
		c.Found++
		if fi.ModTime().UnixNano() == mtime {
			c.SameTime++
		}
	}
	return c, rows.Err()
}

// RelocateRoot sets the new path of a root folder; a deactivated root is
// active again afterwards. before runs right before the change (the
// snapshot, J2) and stops it if it fails. The caller scans the root then.
// Files on disk are never touched (J1).
func RelocateRoot(ctx context.Context, db *sql.DB, id int64, path, dataDir string, before func() error) (Root, error) {
	old, err := GetRoot(ctx, db, id)
	if err != nil {
		return Root{}, err
	}
	abs, err := checkNewPath(ctx, db, id, path, dataDir)
	if err != nil {
		return Root{}, err
	}
	if abs == old.Path {
		return old, nil
	}
	if before != nil {
		if err := before(); err != nil {
			return Root{}, err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Root{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE root SET path = ?, kind = ?, status = 'unknown', removed_at = NULL
		WHERE id = ?`, abs, DetectKind(abs), id); err != nil {
		return Root{}, err
	}
	if err := tx.Commit(); err != nil {
		return Root{}, err
	}
	return GetRoot(ctx, db, id)
}
