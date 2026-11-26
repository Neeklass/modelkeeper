// Package corrections stores manual fixes of model detection as overrides
// (F4d) that survive rescans (F4e). Every operation returns the roots whose
// detection must be re-run; the caller runs detect.Apply for them.
package corrections

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
)

// Every correction is one undoable action (catalog.JournalCorrection): the
// override rows it changes and the user data of the models it affects.

func modelName(ctx context.Context, tx *sql.Tx, id int64) string {
	var name string
	tx.QueryRowContext(ctx, `SELECT name FROM model WHERE id = ?`, id).Scan(&name)
	return name
}

// atPath journals a correction at one folder.
func atPath(ctx context.Context, tx *sql.Tx, action string, rootID int64, path string, change func() error) error {
	models, err := catalog.ModelsAt(ctx, tx, rootID, path)
	if err != nil {
		return err
	}
	return catalog.JournalCorrection(ctx, tx, catalog.CorrectionPrefix+action,
		map[string]any{"path": path}, []int64{rootID}, models, change)
}

// conflicting lists override kinds that cancel each other on the same path.
var conflicting = map[string][]string{
	"is_model":  {"is_model", "not_model"},
	"not_model": {"is_model", "not_model"},
	"merge":     {"merge", "split"},
	"split":     {"merge", "split"},
}

func setOverride(ctx context.Context, tx *sql.Tx, kind string, rootID int64, path string, modelID any) error {
	for _, k := range conflicting[kind] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM override WHERE root_id = ? AND path = ? AND kind = ?`,
			rootID, path, k); err != nil {
			return err
		}
	}
	return insertOverride(ctx, tx, kind, rootID, path, modelID, nil)
}

func insertOverride(ctx context.Context, tx *sql.Tx, kind string, rootID int64, path string, modelID, data any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO override (kind, root_id, path, model_id, data, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		kind, rootID, path, modelID, data, time.Now().UnixMilli())
	return err
}

func inTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkModel: "this folder is a model" - everything below belongs to it.
func MarkModel(ctx context.Context, db *sql.DB, rootID int64, path string) ([]int64, error) {
	if strings.Trim(path, "/") == "" {
		return nil, msg.Wrap(nil, "corrections.invalidFolder")
	}
	return []int64{rootID}, inTx(ctx, db, func(tx *sql.Tx) error {
		return atPath(ctx, tx, "markModel", rootID, path, func() error {
			return setOverride(ctx, tx, "is_model", rootID, path, nil)
		})
	})
}

// MarkNotModel: the folder is not a model of its own; it merges into the folder above.
func MarkNotModel(ctx context.Context, db *sql.DB, rootID int64, path string) ([]int64, error) {
	if !strings.Contains(path, "/") {
		return nil, msg.Wrap(nil, "corrections.invalidFolder")
	}
	return []int64{rootID}, inTx(ctx, db, func(tx *sql.Tx) error {
		return atPath(ctx, tx, "markNotModel", rootID, path, func() error {
			return setOverride(ctx, tx, "not_model", rootID, path, nil)
		})
	})
}

type anchor struct {
	rootID int64
	path   string
	kind   string
}

func anchorsOf(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, modelID int64) ([]anchor, error) {
	rows, err := q.QueryContext(ctx, `SELECT root_id, path, kind FROM model_anchor WHERE model_id = ? ORDER BY root_id, path`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []anchor
	for rows.Next() {
		var a anchor
		if err := rows.Scan(&a.rootID, &a.path, &a.kind); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// subfolders returns the direct subfolders of the model's folder anchors that
// contain files of the model ("root id/path" -> number of files).
func subfolders(ctx context.Context, db *sql.DB, modelID int64) (map[anchor][]string, error) {
	anchors, err := anchorsOf(ctx, db, modelID)
	if err != nil {
		return nil, err
	}
	out := map[anchor][]string{}
	for _, a := range anchors {
		if a.kind != "folder" {
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT path FROM location WHERE model_id = ? AND root_id = ?
			AND archive_id IS NULL AND present = 1 AND substr(path, 1, ?) = ?`, modelID, a.rootID, len(a.path)+1, a.path+"/")
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return nil, err
			}
			rest := strings.TrimPrefix(p, a.path+"/")
			if i := strings.Index(rest, "/"); i > 0 {
				seen[a.path+"/"+rest[:i]] = true
			}
		}
		rows.Close()
		var subs []string
		for s := range seen {
			subs = append(subs, s)
		}
		sort.Strings(subs)
		out[a] = subs
	}
	return out, nil
}

// Split turns every subfolder of the model's folders into a model of its own.
func Split(ctx context.Context, db *sql.DB, modelID int64) ([]int64, error) {
	subs, err := subfolders(ctx, db, modelID)
	if err != nil {
		return nil, err
	}
	var roots []int64
	for a := range subs {
		roots = appendUnique(roots, a.rootID)
	}
	n := 0
	err = inTx(ctx, db, func(tx *sql.Tx) error {
		models := []int64{modelID}
		for a, list := range subs {
			for _, p := range list {
				ids, err := catalog.ModelsAt(ctx, tx, a.rootID, p)
				if err != nil {
					return err
				}
				models = append(models, ids...)
			}
		}
		params := map[string]any{"name": modelName(ctx, tx, modelID)}
		return catalog.JournalCorrection(ctx, tx, catalog.CorrectionPrefix+"split", params, roots, models, func() error {
			for a, list := range subs {
				for _, p := range list {
					if err := setOverride(ctx, tx, "is_model", a.rootID, p, nil); err != nil {
						return err
					}
					n++
				}
			}
			return nil
		})
	})
	if err == nil && n == 0 {
		return nil, msg.Wrap(nil, "corrections.nothingToSplit")
	}
	return roots, err
}

// Detach splits one location (anchor) off a model that has several.
func Detach(ctx context.Context, db *sql.DB, modelID, rootID int64, path string) ([]int64, error) {
	anchors, err := anchorsOf(ctx, db, modelID)
	if err != nil {
		return nil, err
	}
	if len(anchors) < 2 {
		return nil, msg.Wrap(nil, "corrections.nothingToDetach")
	}
	return []int64{rootID}, inTx(ctx, db, func(tx *sql.Tx) error {
		params := map[string]any{"name": modelName(ctx, tx, modelID), "path": path}
		return catalog.JournalCorrection(ctx, tx, catalog.CorrectionPrefix+"detach", params, []int64{rootID},
			[]int64{modelID}, func() error {
				return setOverride(ctx, tx, "split", rootID, path, modelID)
			})
	})
}

// Merge joins the source models into the target (F4d). The sources' tags,
// tag suggestions and notes move to the target; their folders stay merged on rescans.
func Merge(ctx context.Context, db *sql.DB, targetID int64, sourceIDs []int64) ([]int64, error) {
	var roots []int64
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM model WHERE id = ?`, targetID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return msg.Wrap(nil, "error.modelNotFound")
		}
		targetAnchors, err := anchorsOf(ctx, tx, targetID)
		if err != nil {
			return err
		}
		for _, a := range targetAnchors {
			roots = appendUnique(roots, a.rootID)
		}
		sourceAnchors := map[int64][]anchor{}
		models := []int64{targetID}
		for _, src := range sourceIDs {
			if src == targetID {
				continue
			}
			anchors, err := anchorsOf(ctx, tx, src)
			if err != nil {
				return err
			}
			sourceAnchors[src] = anchors
			models = append(models, src)
			for _, a := range anchors {
				roots = appendUnique(roots, a.rootID)
			}
		}
		params := map[string]any{"name": modelName(ctx, tx, targetID), "count": len(sourceAnchors)}
		return catalog.JournalCorrection(ctx, tx, catalog.CorrectionPrefix+"merge", params, roots, models, func() error {
			for _, src := range models[1:] {
				for _, a := range sourceAnchors[src] {
					if err := setOverride(ctx, tx, "merge", a.rootID, a.path, targetID); err != nil {
						return err
					}
				}
				if err := moveUserData(ctx, tx, src, targetID); err != nil {
					return err
				}
			}
			return nil
		})
	})
	return roots, err
}

func moveUserData(ctx context.Context, tx *sql.Tx, src, dst int64) error {
	for _, q := range []string{
		`INSERT OR IGNORE INTO model_tag (model_id, tag_id) SELECT ?2, tag_id FROM model_tag WHERE model_id = ?1`,
		`INSERT OR IGNORE INTO tag_suggestion (model_id, name, source, state)
			SELECT ?2, name, source, state FROM tag_suggestion WHERE model_id = ?1`,
		`UPDATE model SET notes = CASE
				WHEN (SELECT notes FROM model WHERE id = ?1) = '' THEN notes
				WHEN notes = '' THEN (SELECT notes FROM model WHERE id = ?1)
				ELSE notes || char(10) || char(10) || (SELECT notes FROM model WHERE id = ?1) END,
			print_status = CASE WHEN print_status = 'not_printed'
				THEN (SELECT print_status FROM model WHERE id = ?1) ELSE print_status END,
			favorite = max(favorite, (SELECT favorite FROM model WHERE id = ?1)),
			origin = coalesce(nullif(origin, ''), (SELECT origin FROM model WHERE id = ?1)),
			origin_url = coalesce(nullif(origin_url, ''), (SELECT origin_url FROM model WHERE id = ?1)),
			license = coalesce(nullif(license, ''), (SELECT license FROM model WHERE id = ?1))
			WHERE id = ?2`,
		// Corrections that pointed at the source now point at the target.
		`UPDATE override SET model_id = ?2 WHERE model_id = ?1 AND kind = 'merge'`,
		`DELETE FROM override WHERE model_id = ?1`,
		// Prints and collection memberships go along.
		`UPDATE print_log SET model_id = ?2 WHERE model_id = ?1`,
		`INSERT OR IGNORE INTO collection_model (collection_id, model_id, position)
			SELECT collection_id, ?2, position FROM collection_model WHERE model_id = ?1`,
		`DELETE FROM collection_model WHERE model_id = ?1`,
		// The emptied source is removed by detection's cleanup.
		`DELETE FROM model_tag WHERE model_id = ?1`,
		`UPDATE model SET notes = '', print_status = 'not_printed', name_manual = 0, creator_manual = 0,
			supports_manual = 0, thumb_source = NULL, favorite = 0, origin = NULL, origin_url = NULL, license = NULL
			WHERE id = ?1`,
	} {
		if _, err := tx.ExecContext(ctx, q, src, dst); err != nil {
			return err
		}
	}
	return nil
}

// Reset removes all corrections at the model's folders.
func Reset(ctx context.Context, db *sql.DB, modelID int64) ([]int64, error) {
	anchors, err := anchorsOf(ctx, db, modelID)
	if err != nil {
		return nil, err
	}
	var roots []int64
	for _, a := range anchors {
		roots = appendUnique(roots, a.rootID)
	}
	err = inTx(ctx, db, func(tx *sql.Tx) error {
		// Roots of corrections that point at the model elsewhere, too.
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT root_id FROM override WHERE model_id = ?`, modelID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r int64
			if err := rows.Scan(&r); err != nil {
				rows.Close()
				return err
			}
			roots = appendUnique(roots, r)
		}
		rows.Close()
		models := []int64{modelID}
		for _, a := range anchors {
			ids, err := catalog.ModelsAt(ctx, tx, a.rootID, a.path)
			if err != nil {
				return err
			}
			models = append(models, ids...)
		}
		params := map[string]any{"name": modelName(ctx, tx, modelID)}
		return catalog.JournalCorrection(ctx, tx, catalog.CorrectionPrefix+"reset", params, roots, models, func() error {
			for _, a := range anchors {
				// The anchor itself and everything below it.
				if _, err := tx.ExecContext(ctx, `DELETE FROM override WHERE root_id = ?
					AND (path = ? OR substr(path, 1, ?) = ?)`, a.rootID, a.path, len(a.path)+1, a.path+"/"); err != nil {
					return err
				}
				// Files moved into the model from elsewhere (file corrections).
				if err := dropFileGroups(ctx, tx, a.rootID, a.path); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM override WHERE model_id = ?`, modelID)
			return err
		})
	})
	return roots, err
}

// HasCorrections reports whether corrections exist at or below the model's folders.
func HasCorrections(ctx context.Context, db *sql.DB, modelID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM override o WHERE o.model_id = ?1 OR EXISTS (
		SELECT 1 FROM model_anchor a WHERE a.model_id = ?1 AND a.root_id = o.root_id
		AND (o.path = a.path OR substr(o.path, 1, length(a.path) + 1) = a.path || '/'))`, modelID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n > 0, err
}

func appendUnique(s []int64, v int64) []int64 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
