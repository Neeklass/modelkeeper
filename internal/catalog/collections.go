package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"modelkeeper/internal/msg"
)

// Collections (G6): named, ordered lists of models – "D&D campaign",
// "Gifts", "To print next". A model can be in several. Saved searches are
// named queries shown in the sidebar.

// Collection is a collection with the number of its models.
type Collection struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Models int    `json:"models"`
}

// CollectionRef names a collection a model is in.
type CollectionRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func cleanName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", msg.Wrap(nil, "collections.invalidName")
	}
	return name, nil
}

// ListCollections returns all collections by name.
func ListCollections(ctx context.Context, db *sql.DB) ([]Collection, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id, c.name, (SELECT count(*) FROM collection_model cm WHERE cm.collection_id = c.id)
		FROM collection c ORDER BY c.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Models); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCollection creates a collection (or returns the one of that name).
func CreateCollection(ctx context.Context, db *sql.DB, name string) (Collection, error) {
	n, err := cleanName(name)
	if err != nil {
		return Collection{}, err
	}
	var c Collection
	err = db.QueryRowContext(ctx, `INSERT INTO collection (name, created_at) VALUES (?, ?)
		ON CONFLICT (name) DO UPDATE SET name = collection.name RETURNING id, name`, n, time.Now().UnixMilli()).Scan(&c.ID, &c.Name)
	return c, err
}

// RenameCollection renames a collection.
func RenameCollection(ctx context.Context, db *sql.DB, id int64, name string) error {
	n, err := cleanName(name)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE collection SET name = ? WHERE id = ?`, n, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return msg.Wrap(nil, "collections.exists", "name", n)
		}
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return msg.Wrap(nil, "collections.notFound")
	}
	return nil
}

// DeleteCollection removes a collection; its models stay in the catalog.
func DeleteCollection(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM collection WHERE id = ?`, id)
	return err
}

// AddToCollection appends models to a collection (models already in it keep
// their place). Returns the number added.
func AddToCollection(ctx context.Context, db *sql.DB, id int64, modelIDs []int64) (int, error) {
	n := 0
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(position), 0) + 1 FROM collection_model WHERE collection_id = ?`,
			id).Scan(&next); err != nil {
			return err
		}
		for _, m := range modelIDs {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO collection_model (collection_id, model_id, position)
				SELECT ?, id, ? FROM model WHERE id = ? AND EXISTS (SELECT 1 FROM collection WHERE id = ?1)`, id, next, m)
			if err != nil {
				return err
			}
			if k, _ := res.RowsAffected(); k > 0 {
				n++
				next++
			}
		}
		return nil
	})
	return n, err
}

// RemoveFromCollection takes models out of a collection.
func RemoveFromCollection(ctx context.Context, db *sql.DB, id int64, modelIDs []int64) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		for _, m := range modelIDs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM collection_model WHERE collection_id = ? AND model_id = ?`, id, m); err != nil {
				return err
			}
		}
		return nil
	})
}

// MoveInCollection moves a model one place up (delta < 0) or down.
func MoveInCollection(ctx context.Context, db *sql.DB, id, modelID int64, delta int) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		var pos int64
		if err := tx.QueryRowContext(ctx, `SELECT position FROM collection_model WHERE collection_id = ? AND model_id = ?`,
			id, modelID).Scan(&pos); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		q := `SELECT model_id, position FROM collection_model WHERE collection_id = ? AND position > ? ORDER BY position LIMIT 1`
		if delta < 0 {
			q = `SELECT model_id, position FROM collection_model WHERE collection_id = ? AND position < ? ORDER BY position DESC LIMIT 1`
		}
		var other, otherPos int64
		if err := tx.QueryRowContext(ctx, q, id, pos).Scan(&other, &otherPos); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil // already first / last
			}
			return err
		}
		for _, s := range [][3]int64{{-1, id, modelID}, {pos, id, other}, {otherPos, id, modelID}} {
			if _, err := tx.ExecContext(ctx, `UPDATE collection_model SET position = ? WHERE collection_id = ? AND model_id = ?`,
				s[0], s[1], s[2]); err != nil {
				return err
			}
		}
		return nil
	})
}

func collectionsOf(ctx context.Context, db *sql.DB, modelID int64) ([]CollectionRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id, c.name FROM collection_model cm JOIN collection c ON c.id = cm.collection_id
		WHERE cm.model_id = ? ORDER BY c.name COLLATE NOCASE`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionRef{}
	for rows.Next() {
		var c CollectionRef
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SavedSearch is a named search (the frontend's query as JSON).
type SavedSearch struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Query string `json:"query"`
}

// ListSavedSearches returns the saved searches by name.
func ListSavedSearches(ctx context.Context, db *sql.DB) ([]SavedSearch, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, query FROM saved_search ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedSearch{}
	for rows.Next() {
		var s SavedSearch
		if err := rows.Scan(&s.ID, &s.Name, &s.Query); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SaveSearch stores a search under a name (replacing one of that name).
func SaveSearch(ctx context.Context, db *sql.DB, name, query string) (SavedSearch, error) {
	n, err := cleanName(name)
	if err != nil {
		return SavedSearch{}, err
	}
	if !json.Valid([]byte(query)) || len(query) > 10000 {
		return SavedSearch{}, msg.Wrap(nil, "error.invalidValue")
	}
	s := SavedSearch{Name: n, Query: query}
	err = db.QueryRowContext(ctx, `INSERT INTO saved_search (name, query, created_at) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET query = excluded.query RETURNING id`, n, query, time.Now().UnixMilli()).Scan(&s.ID)
	return s, err
}

// DeleteSavedSearch removes a saved search.
func DeleteSavedSearch(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM saved_search WHERE id = ?`, id)
	return err
}
