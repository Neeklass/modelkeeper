package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"modelkeeper/internal/detect"
	"modelkeeper/internal/msg"
)

// TagCount is a tag with the number of models carrying it.
type TagCount struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Models int    `json:"models"`
}

// ListTags returns all tags with usage counts, sorted by name.
func ListTags(ctx context.Context, db *sql.DB) ([]TagCount, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.id, t.name, count(mt.model_id) FROM tag t
		LEFT JOIN model_tag mt ON mt.tag_id = t.id GROUP BY t.id ORDER BY t.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.ID, &t.Name, &t.Models); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// maxTagLength bounds tag names (characters).
const maxTagLength = 80

// cleanTag trims and collapses whitespace; tags are flat, free-form text (G1).
func cleanTag(name string) (string, error) {
	n := strings.Join(strings.Fields(name), " ")
	if n == "" || utf8.RuneCountInString(n) > maxTagLength {
		return "", msg.Wrap(nil, "tags.invalid", "max", maxTagLength)
	}
	return n, nil
}

// AddTags adds tags to models (G2 bulk editing). Matching open suggestions
// count as accepted and are no longer offered. Undoable (journal).
func AddTags(ctx context.Context, db *sql.DB, modelIDs []int64, names []string) error {
	clean := make([]string, 0, len(names))
	for _, raw := range names {
		name, err := cleanTag(raw)
		if err != nil {
			return err
		}
		clean = append(clean, name)
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		params := map[string]any{"tags": strings.Join(clean, ", "), "count": len(modelIDs)}
		return journaled(ctx, tx, "history.tagsAdded", params, modelIDs, func() error {
			for _, name := range clean {
				var tagID int64
				if err := tx.QueryRowContext(ctx, `INSERT INTO tag (name) VALUES (?)
					ON CONFLICT (name) DO UPDATE SET name = name RETURNING id`, name).Scan(&tagID); err != nil {
					return err
				}
				for _, id := range modelIDs {
					if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_tag (model_id, tag_id) VALUES (?, ?)`, id, tagID); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `UPDATE tag_suggestion SET state = 'dismissed'
						WHERE model_id = ? AND name = ?`, id, name); err != nil {
						return err
					}
				}
			}
			return touch(ctx, tx, modelIDs)
		})
	})
}

// RemoveTag removes a tag from models; tags no longer used anywhere disappear.
// Undoable (journal).
func RemoveTag(ctx context.Context, db *sql.DB, modelIDs []int64, name string) error {
	return RemoveTags(ctx, db, modelIDs, []string{name})
}

// RemoveTags removes tags from models as one undoable action.
func RemoveTags(ctx context.Context, db *sql.DB, modelIDs []int64, names []string) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		params := map[string]any{"tags": strings.Join(names, ", "), "count": len(modelIDs)}
		return journaled(ctx, tx, "history.tagsRemoved", params, modelIDs, func() error {
			for _, name := range names {
				for _, id := range modelIDs {
					if _, err := tx.ExecContext(ctx, `DELETE FROM model_tag WHERE model_id = ?
						AND tag_id = (SELECT id FROM tag WHERE name = ?)`, id, name); err != nil {
						return err
					}
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE NOT EXISTS
				(SELECT 1 FROM model_tag mt WHERE mt.tag_id = tag.id)`); err != nil {
				return err
			}
			return touch(ctx, tx, modelIDs)
		})
	})
}

// DismissSuggestion hides a tag suggestion for a model; it is not offered again (F5).
// Undoable (journal).
func DismissSuggestion(ctx context.Context, db *sql.DB, modelID int64, name string) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		params := map[string]any{"tags": name, "count": 1}
		return journaled(ctx, tx, "history.suggestionDismissed", params, []int64{modelID}, func() error {
			_, err := tx.ExecContext(ctx, `UPDATE tag_suggestion SET state = 'dismissed' WHERE model_id = ? AND name = ?`,
				modelID, name)
			return err
		})
	})
}

// ModelUpdate changes descriptive fields of a model; nil fields stay unchanged.
type ModelUpdate struct {
	Name        *string `json:"name,omitempty"`
	Notes       *string `json:"notes,omitempty"`
	PrintStatus *string `json:"printStatus,omitempty"` // not_printed | printed | failed (L1)
	// Supports: presupported | unsupported | both | none, or "auto" to let detection decide again (L2).
	Supports *string `json:"supports,omitempty"`
	// Creator: set by the user ("" = no creator); detection no longer changes it.
	Creator *string `json:"creator,omitempty"`
	// Origin (e.g. "MyMiniFactory"), purchase link and license (E6, D7).
	Origin    *string `json:"origin,omitempty"`
	OriginURL *string `json:"originUrl,omitempty"`
	License   *string `json:"license,omitempty"`
}

// maxFieldLength bounds free-text fields other than notes (characters).
const maxFieldLength = 500

// SetFavorite adds models to the favorites or removes them. Undoable (journal).
func SetFavorite(ctx context.Context, db *sql.DB, modelIDs []int64, on bool) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		v := 0
		action := "history.favoritesRemoved"
		if on {
			v, action = 1, "history.favoritesAdded"
		}
		return journaled(ctx, tx, action, map[string]any{"count": len(modelIDs)}, modelIDs, func() error {
			// updated_at stays: it versions the thumbnail URL, which need not reload.
			for _, id := range modelIDs {
				if _, err := tx.ExecContext(ctx, `UPDATE model SET favorite = ? WHERE id = ? AND favorite <> ?`,
					v, id, v); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// UpdateModel applies a ModelUpdate. Edited fields are protected from detection.
// Undoable (journal).
func UpdateModel(ctx context.Context, db *sql.DB, id int64, u ModelUpdate) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM model WHERE id = ?`, id).Scan(&name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return msg.Wrap(nil, "error.modelNotFound")
			}
			return err
		}
		return journaled(ctx, tx, "history.modelEdited", map[string]any{"name": name, "count": 1}, []int64{id}, func() error {
			return updateModel(ctx, tx, id, u)
		})
	})
}

// UpdateModels applies the same change to many models (print status,
// supports, creator, origin, link, license – not name or notes) as one
// undoable action.
func UpdateModels(ctx context.Context, db *sql.DB, ids []int64, u ModelUpdate) error {
	if u.Name != nil || u.Notes != nil {
		return msg.Wrap(nil, "error.invalidValue")
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		return journaled(ctx, tx, "history.modelsEdited", map[string]any{"count": len(ids)}, ids, func() error {
			for _, id := range ids {
				if err := updateModel(ctx, tx, id, u); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func updateModel(ctx context.Context, tx *sql.Tx, id int64, u ModelUpdate) error {
	now := time.Now().UnixMilli()
	exec := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	if u.Name != nil {
		name := strings.TrimSpace(*u.Name)
		if name == "" {
			return msg.Wrap(nil, "models.nameEmpty")
		}
		if err := exec(`UPDATE model SET name = ?, name_manual = 1, updated_at = ? WHERE id = ?`, name, now, id); err != nil {
			return err
		}
	}
	if u.Notes != nil {
		if err := exec(`UPDATE model SET notes = ?, updated_at = ? WHERE id = ?`, *u.Notes, now, id); err != nil {
			return err
		}
	}
	if u.PrintStatus != nil {
		switch *u.PrintStatus {
		case "not_printed", "printed", "failed":
		default:
			return msg.Wrap(nil, "error.invalidValue")
		}
		if err := exec(`UPDATE model SET print_status = ?, updated_at = ? WHERE id = ?`, *u.PrintStatus, now, id); err != nil {
			return err
		}
	}
	if u.Supports != nil {
		var err error
		switch v := *u.Supports; v {
		case "auto":
			err = exec(`UPDATE model SET supports_manual = 0, updated_at = ? WHERE id = ?`, now, id)
		case "none":
			err = exec(`UPDATE model SET supports = NULL, supports_manual = 1, updated_at = ? WHERE id = ?`, now, id)
		case "presupported", "unsupported", "both":
			err = exec(`UPDATE model SET supports = ?, supports_manual = 1, updated_at = ? WHERE id = ?`, v, now, id)
		default:
			return msg.Wrap(nil, "error.invalidValue")
		}
		if err != nil {
			return err
		}
	}
	if u.Creator != nil {
		name := strings.Join(strings.Fields(*u.Creator), " ")
		if utf8.RuneCountInString(name) > maxFieldLength {
			return msg.Wrap(nil, "error.invalidValue")
		}
		var creator any
		if name != "" {
			var cid int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO creator (name) VALUES (?)
				ON CONFLICT (name) DO UPDATE SET name = name RETURNING id`, name).Scan(&cid); err != nil {
				return err
			}
			creator = cid
		}
		if err := exec(`UPDATE model SET creator_id = ?, creator_manual = 1, updated_at = ? WHERE id = ?`, creator, now, id); err != nil {
			return err
		}
		if err := exec(`DELETE FROM creator WHERE NOT EXISTS (SELECT 1 FROM model m WHERE m.creator_id = creator.id)`); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		col string
		v   *string
	}{{"origin", u.Origin}, {"origin_url", u.OriginURL}, {"license", u.License}} {
		if f.v == nil {
			continue
		}
		v := strings.TrimSpace(*f.v)
		if utf8.RuneCountInString(v) > maxFieldLength {
			return msg.Wrap(nil, "error.invalidValue")
		}
		var val any
		if v != "" {
			val = v
		}
		if err := exec(`UPDATE model SET `+f.col+` = ?, updated_at = ? WHERE id = ?`, val, now, id); err != nil {
			return err
		}
	}
	return touch(ctx, tx, []int64{id})
}

// touch refreshes the search index of the given models.
func touch(ctx context.Context, tx *sql.Tx, ids []int64) error {
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		q := `SELECT id FROM model WHERE id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",") + `)`
		if err := detect.RefreshSearchFor(ctx, tx, q, args...); err != nil {
			return err
		}
	}
	return nil
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
