package corrections

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/msg"
)

// File corrections (F4d): single files moved to another model or split off as
// a new model, wherever they lie on disk.
//
// They need no own override kind, so a 1.0 database works unchanged: each
// moved file gets an "is_model" override at its own path whose data names its
// group ({"group": "<path>"}). The detection makes all files of a group one
// model anchored at the group path (the path of one of its files). A "merge"
// override at the group path joins the group to the target model; a "split"
// override there keeps a split-off group away from the model it came from.

type fileRef struct {
	rootID  int64
	path    string
	content bool
}

// filesOf resolves the selected locations: present files on disk (not inside
// an archive) that belong to the model.
func filesOf(ctx context.Context, tx *sql.Tx, modelID int64, locIDs []int64) ([]fileRef, error) {
	if len(locIDs) == 0 {
		return nil, msg.Wrap(nil, "corrections.noFiles")
	}
	seen := map[int64]bool{}
	var out []fileRef
	for _, id := range locIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		var f fileRef
		var cat string
		err := tx.QueryRowContext(ctx, `SELECT root_id, path, category FROM location
			WHERE id = ? AND model_id = ? AND present = 1 AND archive_id IS NULL`, id, modelID).Scan(&f.rootID, &f.path, &cat)
		if err == sql.ErrNoRows {
			return nil, msg.Wrap(nil, "corrections.invalidFile")
		}
		if err != nil {
			return nil, err
		}
		f.content = cat == detect.CatModel || cat == detect.CatSlicer || cat == detect.CatArchive
		out = append(out, f)
	}
	return out, nil
}

// byRoot groups file paths by root, roots in a stable order.
func byRoot(files []fileRef) ([]int64, map[int64][]string) {
	var roots []int64
	paths := map[int64][]string{}
	for _, f := range files {
		if _, ok := paths[f.rootID]; !ok {
			roots = append(roots, f.rootID)
		}
		paths[f.rootID] = append(paths[f.rootID], f.path)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	return roots, paths
}

// remainingContent counts the model's present content files on disk that are not selected.
func remainingContent(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, modelID int64, locIDs []int64) (int, error) {
	args := []any{modelID}
	notIn := ""
	for i, id := range locIDs {
		if i > 0 {
			notIn += ", "
		}
		notIn += "?"
		args = append(args, id)
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM location WHERE model_id = ? AND present = 1
		AND archive_id IS NULL AND category IN ('model', 'slicer', 'archive') AND id NOT IN (`+notIn+`)`, args...).Scan(&n)
	return n, err
}

func groupData(group string) string {
	b, _ := json.Marshal(map[string]string{"group": group})
	return string(b)
}

// regroup puts the files of one root into a new group and returns its path
// (the first file's path). Files left behind in their old groups keep them,
// under a new group path if the old one was a file that moved away.
func regroup(ctx context.Context, tx *sql.Tx, rootID int64, paths []string) (string, error) {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	key := paths[0]
	selected := map[string]bool{}
	for _, p := range paths {
		selected[p] = true
	}

	groups := map[string]string{} // file path -> group path
	rows, err := tx.QueryContext(ctx, `SELECT path, coalesce(data, '') FROM override WHERE root_id = ? AND kind = 'is_model'`, rootID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var p, data string
		if err := rows.Scan(&p, &data); err != nil {
			rows.Close()
			return "", err
		}
		if g := detect.FileGroupOf(data); g != "" {
			groups[p] = g
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}

	members := func(g string, skip map[string]bool) []string {
		var out []string
		for p, pg := range groups {
			if pg == g && !skip[p] {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out
	}
	// rekey moves a group to the path of its first member; its target model
	// (merge) or the model it was split from (split) moves along.
	rekey := func(old string, ms []string) error {
		nk := ms[0]
		for _, m := range ms {
			if _, err := tx.ExecContext(ctx, `UPDATE override SET data = ? WHERE root_id = ? AND kind = 'is_model' AND path = ?`,
				groupData(nk), rootID, m); err != nil {
				return err
			}
			groups[m] = nk
		}
		if nk == old {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM override WHERE root_id = ? AND path = ? AND kind IN ('merge', 'split')`,
			rootID, nk); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO override (kind, root_id, path, model_id, data, created_at)
			SELECT kind, root_id, ?, model_id, data, created_at FROM override
			WHERE root_id = ? AND path = ? AND kind IN ('merge', 'split')`, nk, rootID, old)
		return err
	}

	// 1. Files of a group at the new group path that were not selected stay together.
	if ms := members(key, selected); len(ms) > 0 {
		if err := rekey(key, ms); err != nil {
			return "", err
		}
	}
	// 2. The selected files form the new group.
	oldGroups := map[string]bool{}
	for _, p := range paths {
		if g, ok := groups[p]; ok && g != key {
			oldGroups[g] = true
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM override WHERE root_id = ? AND path = ? AND kind IN ('is_model', 'not_model')`,
			rootID, p); err != nil {
			return "", err
		}
		if err := insertOverride(ctx, tx, "is_model", rootID, p, nil, groupData(key)); err != nil {
			return "", err
		}
		groups[p] = key
	}
	// 3. Old groups whose path was one of the moved files get a new path.
	for g := range oldGroups {
		if groups[g] == g {
			continue
		}
		if ms := members(g, nil); len(ms) > 0 {
			if err := rekey(g, ms); err != nil {
				return "", err
			}
		}
		// The old path is no group any more and its file lives in another
		// group: its target is stale.
		if _, grouped := groups[g]; grouped {
			if _, err := tx.ExecContext(ctx, `DELETE FROM override WHERE root_id = ? AND path = ? AND kind IN ('merge', 'split')`,
				rootID, g); err != nil {
				return "", err
			}
		}
	}
	return key, nil
}

// MoveFiles moves single files of a model into another model. Moving all
// content files is the same as merging the whole model into the target.
func MoveFiles(ctx context.Context, db *sql.DB, sourceID, targetID int64, locIDs []int64) ([]int64, error) {
	if sourceID == targetID {
		return nil, msg.Wrap(nil, "corrections.sameModel")
	}
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM model WHERE id = ?`, targetID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, msg.Wrap(nil, "error.modelNotFound")
	}
	if len(locIDs) > 0 {
		left, err := remainingContent(ctx, db, sourceID, locIDs)
		if err != nil {
			return nil, err
		}
		if left == 0 {
			return Merge(ctx, db, targetID, []int64{sourceID})
		}
	}
	var roots []int64
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		files, err := filesOf(ctx, tx, sourceID, locIDs)
		if err != nil {
			return err
		}
		var paths map[int64][]string
		roots, paths = byRoot(files)
		params := map[string]any{"name": modelName(ctx, tx, targetID), "from": modelName(ctx, tx, sourceID), "count": len(files)}
		return catalog.JournalCorrection(ctx, tx, catalog.CorrectionPrefix+"moveFiles", params, roots,
			[]int64{sourceID, targetID}, func() error {
				for _, r := range roots {
					key, err := regroup(ctx, tx, r, paths[r])
					if err != nil {
						return err
					}
					if err := setOverride(ctx, tx, "merge", r, key, targetID); err != nil {
						return err
					}
				}
				return nil
			})
	})
	return roots, err
}

// DetachFiles splits single files off a model as a new model of their own.
func DetachFiles(ctx context.Context, db *sql.DB, modelID int64, locIDs []int64) ([]int64, error) {
	var roots []int64
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		files, err := filesOf(ctx, tx, modelID, locIDs)
		if err != nil {
			return err
		}
		content := false
		for _, f := range files {
			content = content || f.content
		}
		if !content {
			return msg.Wrap(nil, "corrections.detachNeedsContent")
		}
		var paths map[int64][]string
		roots, paths = byRoot(files)
		if len(roots) > 1 {
			return msg.Wrap(nil, "corrections.detachOneRoot")
		}
		left, err := remainingContent(ctx, tx, modelID, locIDs)
		if err != nil {
			return err
		}
		if left == 0 {
			return msg.Wrap(nil, "corrections.detachAll")
		}
		r := roots[0]
		params := map[string]any{"name": modelName(ctx, tx, modelID), "count": len(files)}
		return catalog.JournalCorrection(ctx, tx, catalog.CorrectionPrefix+"detachFiles", params, roots,
			[]int64{modelID}, func() error {
				key, err := regroup(ctx, tx, r, paths[r])
				if err != nil {
					return err
				}
				return setOverride(ctx, tx, "split", r, key, modelID)
			})
	})
	return roots, err
}

// dropFileGroups removes the file corrections of groups at or below path.
func dropFileGroups(ctx context.Context, tx *sql.Tx, rootID int64, path string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, coalesce(data, '') FROM override WHERE root_id = ? AND kind = 'is_model'`, rootID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			rows.Close()
			return err
		}
		if g := detect.FileGroupOf(data); g != "" && (g == path || strings.HasPrefix(g, path+"/")) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM override WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}
