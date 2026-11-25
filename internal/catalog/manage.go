package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"modelkeeper/internal/msg"
)

// Tag management (TODO 2.2): rename, merge and delete a tag for all models at
// once. Each is one undoable action (journal).

// tagModels returns the models carrying tag id.
func tagModels(ctx context.Context, tx *sql.Tx, tagID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT model_id FROM model_tag WHERE tag_id = ? ORDER BY model_id`, tagID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func tagByName(ctx context.Context, tx *sql.Tx, name string) (int64, string, error) {
	var id int64
	var stored string
	err := tx.QueryRowContext(ctx, `SELECT id, name FROM tag WHERE name = ?`, name).Scan(&id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", msg.Wrap(nil, "tags.notFound", "name", name)
	}
	return id, stored, err
}

// RenameTag renames a tag on all its models. If a tag with the new name
// exists already, the two are merged. Returns the number of models changed.
func RenameTag(ctx context.Context, db *sql.DB, from, to string) (int, error) {
	name, err := cleanTag(to)
	if err != nil {
		return 0, err
	}
	n := 0
	err = inTx(ctx, db, func(tx *sql.Tx) error {
		oldID, oldName, err := tagByName(ctx, tx, from)
		if err != nil {
			return err
		}
		ids, err := tagModels(ctx, tx, oldID)
		if err != nil {
			return err
		}
		n = len(ids)
		var targetID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM tag WHERE name = ?`, name).Scan(&targetID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		action := "history.tagRenamed"
		if targetID != 0 && targetID != oldID {
			action = "history.tagMerged"
		}
		params := map[string]any{"from": oldName, "to": name, "count": len(ids)}
		return journaled(ctx, tx, action, params, ids, func() error {
			switch {
			case targetID == oldID: // same tag, other spelling (case is ignored)
				_, err := tx.ExecContext(ctx, `UPDATE tag SET name = ? WHERE id = ?`, name, oldID)
				return err
			case targetID != 0:
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_tag (model_id, tag_id)
					SELECT model_id, ? FROM model_tag WHERE tag_id = ?`, targetID, oldID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE id = ?`, oldID); err != nil {
					return err
				}
			default:
				if _, err := tx.ExecContext(ctx, `UPDATE tag SET name = ? WHERE id = ?`, name, oldID); err != nil {
					return err
				}
			}
			return touch(ctx, tx, ids)
		})
	})
	return n, err
}

// DeleteTag removes a tag from all its models. Returns the number of models.
func DeleteTag(ctx context.Context, db *sql.DB, name string) (int, error) {
	n := 0
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		id, stored, err := tagByName(ctx, tx, name)
		if err != nil {
			return err
		}
		ids, err := tagModels(ctx, tx, id)
		if err != nil {
			return err
		}
		n = len(ids)
		params := map[string]any{"tags": stored, "count": len(ids)}
		return journaled(ctx, tx, "history.tagDeleted", params, ids, func() error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE id = ?`, id); err != nil {
				return err
			}
			return touch(ctx, tx, ids)
		})
	})
	return n, err
}

// ForgetWaiting removes models without any files from the catalog (their
// files are gone for good, J7), with their tags, notes and corrections. Models
// that have files are left alone. The caller takes a snapshot before; this is
// not in the journal. Returns the number of models removed.
func ForgetWaiting(ctx context.Context, db *sql.DB, ids []int64) (int, error) {
	removed := 0
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		for start := 0; start < len(ids); start += 500 {
			part := ids[start:min(start+500, len(ids))]
			args := make([]any, len(part))
			for i, id := range part {
				args[i] = id
			}
			res, err := tx.ExecContext(ctx, `DELETE FROM model WHERE id IN (`+
				strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+`) AND `+
				strings.ReplaceAll(waitingCond, "m.id", "model.id"), args...)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			removed += int(n)
		}
		for _, q := range []string{
			`DELETE FROM model_fts WHERE rowid NOT IN (SELECT id FROM model)`,
			`DELETE FROM tag WHERE NOT EXISTS (SELECT 1 FROM model_tag mt WHERE mt.tag_id = tag.id)`,
			`DELETE FROM creator WHERE NOT EXISTS (SELECT 1 FROM model m WHERE m.creator_id = creator.id)`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	return removed, err
}
