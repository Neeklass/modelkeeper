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
)

// Root is a scanned root folder.
type Root struct {
	ID           int64  `json:"id"`
	Path         string `json:"path"`
	Kind         string `json:"kind"` // local | nas
	CreatorLevel bool   `json:"creatorLevel"`
	Status       string `json:"status"` // unknown | online | offline
	LastScanAt   *int64 `json:"lastScanAt,omitempty"`
	Live         bool   `json:"live"` // changes are picked up automatically (E4); set by the app
}

const rootCols = `id, path, kind, creator_level, status, last_scan_at`

func scanRoot(row interface{ Scan(...any) error }) (Root, error) {
	var r Root
	var last sql.NullInt64
	if err := row.Scan(&r.ID, &r.Path, &r.Kind, &r.CreatorLevel, &r.Status, &last); err != nil {
		return Root{}, err
	}
	if last.Valid {
		r.LastScanAt = &last.Int64
	}
	return r, nil
}

// ListRoots returns all roots ordered by path.
func ListRoots(ctx context.Context, db *sql.DB) ([]Root, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+rootCols+` FROM root ORDER BY path`)
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

// GetRoot returns one root.
func GetRoot(ctx context.Context, db *sql.DB, id int64) (Root, error) {
	r, err := scanRoot(db.QueryRowContext(ctx, `SELECT `+rootCols+` FROM root WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Root{}, msg.Wrap(nil, "error.rootNotFound")
	}
	return r, err
}

// AddRoot registers a folder as root. The kind (local/nas) is detected from
// the path. Roots must not overlap (one inside another).
func AddRoot(ctx context.Context, db *sql.DB, path string) (Root, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return Root{}, msg.Wrap(err, "error.rootInvalid", "path", path)
	}
	abs = filepath.Clean(abs)
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Root{}, msg.Wrap(err, "error.rootNotFolder", "path", abs)
	}

	existing, err := ListRoots(ctx, db)
	if err != nil {
		return Root{}, err
	}
	for _, r := range existing {
		if pathsOverlap(r.Path, abs) {
			return Root{}, msg.Wrap(nil, "error.rootOverlap", "path", abs, "other", r.Path)
		}
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

// RemoveRoot deletes a root and everything found below it from the catalog.
// Files on disk are never touched (J1). Models left without files are
// removed too, unless the user added data to them.
func RemoveRoot(ctx context.Context, db *sql.DB, id int64) error {
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

// pathsOverlap reports whether a equals b or one contains the other.
// Comparison is case-insensitive on Windows.
func pathsOverlap(a, b string) bool {
	norm := func(p string) string {
		p = filepath.Clean(p)
		if os.PathSeparator == '\\' {
			p = strings.ToLower(p)
		}
		return strings.TrimRight(p, `\/`) + string(os.PathSeparator)
	}
	na, nb := norm(a), norm(b)
	return strings.HasPrefix(na, nb) || strings.HasPrefix(nb, na)
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
