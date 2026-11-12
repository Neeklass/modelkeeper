// Package catalog contains queries on the catalog database that are used
// by the UI (roots, statistics, issues).
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modelkeeper/internal/detect"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/pathutil"
)

// Root is a scanned root folder.
//
// Removing a root deactivates it (E9): RemovedAt is set, the root is no
// longer scanned or watched and not listed by ListRoots, but everything the
// catalog knows below it stays. Adding the same path again reactivates it.
type Root struct {
	ID           int64  `json:"id"`
	Path         string `json:"path"`
	Kind         string `json:"kind"` // local | nas
	CreatorLevel bool   `json:"creatorLevel"`
	Status       string `json:"status"` // unknown | online | offline
	LastScanAt   *int64 `json:"lastScanAt,omitempty"`
	RemovedAt    *int64 `json:"removedAt,omitempty"` // deactivated at (unix ms); nil = active
	Live         bool   `json:"live"`                // changes are picked up automatically (E4); set by the app
	// Reactivated is set by AddRoot when it reactivated a deactivated root
	// instead of adding a new one.
	Reactivated bool `json:"reactivated,omitempty"`
}

// inactiveRoots is the set of deactivated root ids, for "x.root_id NOT IN"
// conditions (evaluated once per query, the root table is tiny).
const inactiveRoots = `(SELECT id FROM root WHERE removed_at IS NOT NULL)`

const rootCols = `id, path, kind, creator_level, status, last_scan_at, removed_at`

func scanRoot(row interface{ Scan(...any) error }) (Root, error) {
	var r Root
	var last, removed sql.NullInt64
	if err := row.Scan(&r.ID, &r.Path, &r.Kind, &r.CreatorLevel, &r.Status, &last, &removed); err != nil {
		return Root{}, err
	}
	if last.Valid {
		r.LastScanAt = &last.Int64
	}
	if removed.Valid {
		r.RemovedAt = &removed.Int64
	}
	return r, nil
}

func listRoots(ctx context.Context, db *sql.DB, where string) ([]Root, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+rootCols+` FROM root `+where+` ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Root{}
	for rows.Next() {
		r, err := scanRoot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRoots returns the active roots ordered by path.
func ListRoots(ctx context.Context, db *sql.DB) ([]Root, error) {
	return listRoots(ctx, db, `WHERE removed_at IS NULL`)
}

// ListRemovedRoots returns the deactivated roots ordered by path.
func ListRemovedRoots(ctx context.Context, db *sql.DB) ([]Root, error) {
	return listRoots(ctx, db, `WHERE removed_at IS NOT NULL`)
}

// GetRoot returns one root, active or deactivated.
func GetRoot(ctx context.Context, db *sql.DB, id int64) (Root, error) {
	r, err := scanRoot(db.QueryRowContext(ctx, `SELECT `+rootCols+` FROM root WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Root{}, msg.Wrap(nil, "error.rootNotFound")
	}
	return r, err
}

// GetActiveRoot returns one root, or error.rootRemoved if it is deactivated.
func GetActiveRoot(ctx context.Context, db *sql.DB, id int64) (Root, error) {
	r, err := GetRoot(ctx, db, id)
	if err == nil && r.RemovedAt != nil {
		return r, msg.Wrap(nil, "error.rootRemoved", "path", r.Path)
	}
	return r, err
}

// AddRoot registers a folder as root. The kind (local/nas) is detected from
// the path. Roots must not overlap (one inside another), and must neither be
// nor contain the app's data folder dataDir ("" = no check).
//
// The same path as a deactivated root (compared like the file system does:
// case-insensitive on Windows, trailing separators ignored) reactivates that
// root with its ID and everything below it (E9); the caller then scans it.
// Reactivation also works while the folder is unreachable (E3).
func AddRoot(ctx context.Context, db *sql.DB, path, dataDir string) (Root, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return Root{}, msg.Wrap(err, "error.rootInvalid", "path", path)
	}
	abs = filepath.Clean(abs)

	all, err := listRoots(ctx, db, ``)
	if err != nil {
		return Root{}, err
	}
	for _, r := range all {
		if !pathutil.Same(r.Path, abs) {
			continue
		}
		if r.RemovedAt == nil {
			return Root{}, msg.Wrap(nil, "error.rootOverlap", "path", abs, "other", r.Path)
		}
		if _, err := db.ExecContext(ctx, `UPDATE root SET removed_at = NULL, status = 'unknown' WHERE id = ?`, r.ID); err != nil {
			return Root{}, err
		}
		r, err := GetRoot(ctx, db, r.ID)
		r.Reactivated = true
		return r, err
	}

	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Root{}, msg.Wrap(err, "error.rootNotFolder", "path", abs)
	}
	if dataDir != "" && pathutil.Overlap(abs, dataDir) {
		return Root{}, msg.Wrap(nil, "error.rootContainsData", "path", abs, "data", dataDir)
	}
	for _, r := range all {
		if !pathutil.Overlap(r.Path, abs) {
			continue
		}
		if r.RemovedAt != nil {
			return Root{}, msg.Wrap(nil, "error.rootOverlapRemoved", "path", abs, "other", r.Path)
		}
		return Root{}, msg.Wrap(nil, "error.rootOverlap", "path", abs, "other", r.Path)
	}

	res, err := db.ExecContext(ctx,
		`INSERT INTO root (path, kind, created_at) VALUES (?, ?, ?)`,
		abs, DetectKind(abs), time.Now().UnixMilli())
	if err != nil {
		return Root{}, err
	}
	id, _ := res.LastInsertId()
	return GetRoot(ctx, db, id)
}

// DeactivateRoot removes a root from scanning, watching and the root list,
// keeping everything the catalog knows below it (E9). Files on disk are never
// touched (J1).
func DeactivateRoot(ctx context.Context, db *sql.DB, id int64) error {
	res, err := db.ExecContext(ctx, `UPDATE root SET removed_at = ? WHERE id = ? AND removed_at IS NULL`,
		time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return msg.Wrap(nil, "error.rootNotFound")
	}
	return nil
}

// PurgeImpact counts the models with user data (tags, notes, status …, see
// detect.UserDataCond) that have files or an anchor on the root and no file
// anywhere else: after PurgeRoot they no longer have any location.
func PurgeImpact(ctx context.Context, db *sql.DB, rootID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM model m
		WHERE (EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.root_id = ?1)
		    OR EXISTS (SELECT 1 FROM model_anchor a WHERE a.model_id = m.id AND a.root_id = ?1))
		  AND NOT EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.root_id <> ?1)
		  AND `+detect.UserDataCond, rootID).Scan(&n)
	return n, err
}

// PurgeRoot permanently deletes a deactivated root and everything found
// below it from the catalog (E9). confirm must equal PurgeImpact (the number
// the user confirmed); before runs right before deleting (the pre-delete
// snapshot, J2) and stops the deletion if it fails. Models left without files
// are removed too, unless the user added data to them. Files on disk are
// never touched (J1).
func PurgeRoot(ctx context.Context, db *sql.DB, id int64, confirm int, before func() error) error {
	r, err := GetRoot(ctx, db, id)
	if err != nil {
		return err
	}
	if r.RemovedAt == nil {
		return msg.Wrap(nil, "error.rootNotRemoved", "path", r.Path)
	}
	n, err := PurgeImpact(ctx, db, id)
	if err != nil {
		return err
	}
	if n != confirm {
		return msg.Wrap(nil, "error.purgeCountChanged", "count", n)
	}
	if before != nil {
		if err := before(); err != nil {
			return err
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM root WHERE id = ?`, id); err != nil {
		return err
	}
	// Content no longer referenced by any location.
	if _, err := tx.ExecContext(ctx, `DELETE FROM file WHERE NOT EXISTS
		(SELECT 1 FROM location l WHERE l.file_id = file.id)`); err != nil {
		return err
	}
	if err := detect.Cleanup(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetCreatorLevel turns the "first level = creator" rule (F4a) on or off.
func SetCreatorLevel(ctx context.Context, db *sql.DB, id int64, on bool) error {
	_, err := db.ExecContext(ctx, `UPDATE root SET creator_level = ? WHERE id = ?`, on, id)
	return err
}

// joinRoot joins a root path and a '/'-separated relative path.
func joinRoot(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}
