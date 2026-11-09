package catalog

import (
	"context"
	"database/sql"
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
// count as accepted and are no longer offered.
func AddTags(ctx context.Context, db *sql.DB, modelIDs []int64, names []string) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		for _, raw := range names {
			name, err := cleanTag(raw)
			if err != nil {
				return err
			}
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
}

// RemoveTag removes a tag from models; tags no longer used anywhere disappear.
func RemoveTag(ctx context.Context, db *sql.DB, modelIDs []int64, name string) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		for _, id := range modelIDs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM model_tag WHERE model_id = ?
				AND tag_id = (SELECT id FROM tag WHERE name = ?)`, id, name); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE NOT EXISTS
			(SELECT 1 FROM model_tag mt WHERE mt.tag_id = tag.id)`); err != nil {
			return err
		}
		return touch(ctx, tx, modelIDs)
	})
}

// DismissSuggestion hides a tag suggestion for a model; it is not offered again (F5).
func DismissSuggestion(ctx context.Context, db *sql.DB, modelID int64, name string) error {
	_, err := db.ExecContext(ctx, `UPDATE tag_suggestion SET state = 'dismissed' WHERE model_id = ? AND name = ?`,
		modelID, name)
	return err
}

// ModelUpdate changes descriptive fields of a model; nil fields stay unchanged.
type ModelUpdate struct {
	Name        *string `json:"name,omitempty"`
	Notes       *string `json:"notes,omitempty"`
	PrintStatus *string `json:"printStatus,omitempty"` // not_printed | printed | failed (L1)
	// Supports: presupported | unsupported | both | none, or "auto" to let detection decide again (L2).
	Supports *string `json:"supports,omitempty"`
}

// SetFavorite adds models to the favorites or removes them.
func SetFavorite(ctx context.Context, db *sql.DB, modelIDs []int64, on bool) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		v := 0
		if on {
			v = 1
		}
		// updated_at stays: it versions the thumbnail URL, which need not reload.
		for _, id := range modelIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE model SET favorite = ? WHERE id = ? AND favorite <> ?`,
				v, id, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateModel applies a ModelUpdate. Edited fields are protected from detection.
func UpdateModel(ctx context.Context, db *sql.DB, id int64, u ModelUpdate) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
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
		return touch(ctx, tx, []int64{id})
	})
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
