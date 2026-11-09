package catalog

import (
	"context"
	"database/sql"
)

// FileTypesChange summarizes how the catalog was adapted to changed file types (F10).
type FileTypesChange struct {
	Removed         int `json:"removed"`         // locations of types no longer recognized
	Recategorized   int `json:"recategorized"`   // locations whose category changed
	ArchivesPending int `json:"archivesPending"` // archives to read again on the next scan
}

// ApplyFileTypes adapts the catalog to changed file types: locations of types
// that are no longer recognized are dropped (they are not "missing", just no
// longer tracked; files on disk are untouched), categories are updated, and if
// types were added, archives are marked to be read again on the next scan so
// their members of the new types are cataloged. Disk files of new types are
// found by the next scan anyway. Models are not re-detected here.
//
// category maps an extension to its new category ("" = not recognized).
func ApplyFileTypes(ctx context.Context, db *sql.DB, category func(ext string) string, typesAdded bool) (FileTypesChange, error) {
	var ch FileTypesChange
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ch, err
	}
	defer tx.Rollback()

	type pair struct{ ext, cat string }
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT ext, category FROM location`)
	if err != nil {
		return ch, err
	}
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.ext, &p.cat); err != nil {
			rows.Close()
			return ch, err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ch, err
	}

	exec := func(q string, args ...any) (int, error) {
		r, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, err
		}
		n, _ := r.RowsAffected()
		return int(n), nil
	}
	for _, p := range pairs {
		next := category(p.ext)
		switch {
		case next == p.cat:
		case next == "":
			// Members of a dropped archive go with it (ON DELETE CASCADE).
			n, err := exec(`DELETE FROM location WHERE ext = ? AND category = ?`, p.ext, p.cat)
			if err != nil {
				return ch, err
			}
			ch.Removed += n
		case p.cat == "archive":
			// No longer an archive: forget its members and archive state.
			if _, err := exec(`DELETE FROM location WHERE archive_id IN
				(SELECT id FROM location WHERE ext = ? AND category = 'archive')`, p.ext); err != nil {
				return ch, err
			}
			n, err := exec(`UPDATE location SET category = ?, archive_status = NULL, archive_error = NULL
				WHERE ext = ? AND category = 'archive'`, next, p.ext)
			if err != nil {
				return ch, err
			}
			ch.Recategorized += n
		case next == "archive":
			// Now an archive: read on the next scan; nested archives are not opened.
			n, err := exec(`UPDATE location SET category = 'archive',
				archive_status = CASE WHEN archive_id IS NULL THEN 'pending' ELSE 'unsupported' END, archive_error = NULL
				WHERE ext = ? AND category = ?`, p.ext, p.cat)
			if err != nil {
				return ch, err
			}
			ch.Recategorized += n
		default:
			n, err := exec(`UPDATE location SET category = ? WHERE ext = ? AND category = ?`, next, p.ext, p.cat)
			if err != nil {
				return ch, err
			}
			ch.Recategorized += n
		}
	}
	if typesAdded {
		n, err := exec(`UPDATE location SET archive_status = 'pending', archive_error = NULL
			WHERE category = 'archive' AND archive_id IS NULL AND present = 1 AND archive_status IN ('ok', 'broken')`)
		if err != nil {
			return ch, err
		}
		ch.ArchivesPending = n
	}
	return ch, tx.Commit()
}
