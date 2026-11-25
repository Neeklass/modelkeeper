package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"modelkeeper/internal/msg"
)

// Undo for catalog changes (TODO 2.1). Every change of user data through this
// package (tags, fields, favorites, suggestions) is recorded in the journal
// table, in the same transaction as the change: the user data of the changed
// models before and after. Undo puts the state from before back, for every
// model that still has the state from after. A model that was changed since
// in another way (a merge, an import) is left as it is, so undo never throws
// away newer work.

// journalKeep is the number of journal rows kept.
const journalKeep = 500

// Edits of the same model within coalesceWindow are one action, so notes
// saved while typing are undone as a whole.
const (
	coalesceAction = "history.modelEdited"
	coalesceWindow = time.Minute
)

// modelState is the user data of one model, as the journal stores it.
type modelState struct {
	UID            string   `json:"uid"`
	Name           string   `json:"name"`
	NameManual     bool     `json:"nameManual"`
	Creator        string   `json:"creator"`
	CreatorManual  bool     `json:"creatorManual"`
	Notes          string   `json:"notes"`
	Favorite       bool     `json:"favorite"`
	PrintStatus    string   `json:"printStatus"`
	Supports       string   `json:"supports"`
	SupportsManual bool     `json:"supportsManual"`
	Origin         string   `json:"origin"`
	OriginURL      string   `json:"originUrl"`
	License        string   `json:"license"`
	Tags           []string `json:"tags"`
	Dismissed      []string `json:"dismissed"`
}

type stateChange struct {
	Before modelState `json:"before"`
	After  modelState `json:"after"`
}

func txStrings(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func captureOne(ctx context.Context, tx *sql.Tx, id int64) (modelState, bool, error) {
	var s modelState
	err := tx.QueryRowContext(ctx, `SELECT m.uid, m.name, m.name_manual, coalesce(c.name, ''), m.creator_manual, m.notes,
		m.favorite, m.print_status, coalesce(m.supports, ''), m.supports_manual,
		coalesce(m.origin, ''), coalesce(m.origin_url, ''), coalesce(m.license, '')
		FROM model m LEFT JOIN creator c ON c.id = m.creator_id WHERE m.id = ?`, id).
		Scan(&s.UID, &s.Name, &s.NameManual, &s.Creator, &s.CreatorManual, &s.Notes, &s.Favorite, &s.PrintStatus,
			&s.Supports, &s.SupportsManual, &s.Origin, &s.OriginURL, &s.License)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if s.Tags, err = txStrings(ctx, tx, `SELECT t.name FROM model_tag mt JOIN tag t ON t.id = mt.tag_id
		WHERE mt.model_id = ? ORDER BY t.name`, id); err != nil {
		return s, false, err
	}
	if s.Dismissed, err = txStrings(ctx, tx, `SELECT name FROM tag_suggestion
		WHERE model_id = ? AND state = 'dismissed' ORDER BY name`, id); err != nil {
		return s, false, err
	}
	return s, true, nil
}

func capture(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]modelState, error) {
	out := make(map[int64]modelState, len(ids))
	for _, id := range ids {
		if _, ok := out[id]; ok {
			continue
		}
		s, ok, err := captureOne(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id] = s
		}
	}
	return out, nil
}

func sameState(a, b modelState) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// journaled runs change inside tx and records what it changed on the models
// ids as one undoable action (action: message key, params: its parameters).
// Models the change left as they were are not recorded; nothing changed, no row.
func journaled(ctx context.Context, tx *sql.Tx, action string, params map[string]any, ids []int64, change func() error) error {
	before, err := capture(ctx, tx, ids)
	if err != nil {
		return err
	}
	if err := change(); err != nil {
		return err
	}
	after, err := capture(ctx, tx, ids)
	if err != nil {
		return err
	}
	var changes []stateChange
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		b, okB := before[id]
		a, okA := after[id]
		if okB && okA && !sameState(b, a) {
			changes = append(changes, stateChange{Before: b, After: a})
		}
	}
	if len(changes) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	// Edits of one model in a row (notes saved while typing) are one action.
	if action == coalesceAction && len(changes) == 1 {
		var lastID, lastAt int64
		var lastAction, lastState string
		err := tx.QueryRowContext(ctx, `SELECT id, at, action, state FROM journal WHERE undone_at IS NULL
			AND id = (SELECT max(id) FROM journal)`).Scan(&lastID, &lastAt, &lastAction, &lastState)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var last []stateChange
		if err == nil && lastAction == action && now-lastAt < coalesceWindow.Milliseconds() &&
			json.Unmarshal([]byte(lastState), &last) == nil && len(last) == 1 &&
			last[0].After.UID == changes[0].Before.UID && sameState(last[0].After, changes[0].Before) {
			merged, err := json.Marshal([]stateChange{{Before: last[0].Before, After: changes[0].After}})
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE journal SET at = ?, state = ? WHERE id = ?`, now, string(merged), lastID)
			return err
		}
	}
	state, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	var p any
	if len(params) > 0 {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		p = string(b)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO journal (at, action, params, models, state) VALUES (?, ?, ?, ?, ?)`,
		now, action, p, len(changes), string(state)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM journal WHERE id <= (SELECT max(id) FROM journal) - ?`, journalKeep)
	return err
}

// restoreState writes a model state back (the model is found by its uid).
func restoreState(ctx context.Context, tx *sql.Tx, id int64, s modelState) error {
	var creator any
	if name := strings.TrimSpace(s.Creator); name != "" {
		var cid int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO creator (name) VALUES (?)
			ON CONFLICT (name) DO UPDATE SET name = name RETURNING id`, name).Scan(&cid); err != nil {
			return err
		}
		creator = cid
	}
	null := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	if _, err := tx.ExecContext(ctx, `UPDATE model SET name = ?, name_manual = ?, creator_id = ?, creator_manual = ?,
		notes = ?, favorite = ?, print_status = ?, supports = ?, supports_manual = ?,
		origin = ?, origin_url = ?, license = ?, updated_at = ? WHERE id = ?`,
		s.Name, s.NameManual, creator, s.CreatorManual, s.Notes, s.Favorite, s.PrintStatus, null(s.Supports),
		s.SupportsManual, null(s.Origin), null(s.OriginURL), null(s.License), time.Now().UnixMilli(), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_tag WHERE model_id = ?`, id); err != nil {
		return err
	}
	for _, name := range s.Tags {
		var tagID int64
		// The stored spelling comes back too (undoing a rename that only changed case).
		if err := tx.QueryRowContext(ctx, `INSERT INTO tag (name) VALUES (?)
			ON CONFLICT (name) DO UPDATE SET name = excluded.name RETURNING id`, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_tag (model_id, tag_id) VALUES (?, ?)`, id, tagID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tag_suggestion SET state = 'open' WHERE model_id = ?`, id); err != nil {
		return err
	}
	for _, name := range s.Dismissed {
		if _, err := tx.ExecContext(ctx, `UPDATE tag_suggestion SET state = 'dismissed' WHERE model_id = ? AND name = ?`,
			id, name); err != nil {
			return err
		}
	}
	return nil
}

// HistoryEntry is one recorded action.
type HistoryEntry struct {
	ID     int64          `json:"id"`
	At     int64          `json:"at"` // unix ms
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
	Models int            `json:"models"`
	Undone bool           `json:"undone"`
}

// ListHistory returns the newest recorded actions, newest first.
func ListHistory(ctx context.Context, db *sql.DB, limit int) ([]HistoryEntry, error) {
	if limit <= 0 || limit > journalKeep {
		limit = journalKeep
	}
	rows, err := db.QueryContext(ctx, `SELECT id, at, action, coalesce(params, ''), models, undone_at IS NOT NULL
		FROM journal ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var params string
		if err := rows.Scan(&h.ID, &h.At, &h.Action, &params, &h.Models, &h.Undone); err != nil {
			return nil, err
		}
		if params != "" {
			json.Unmarshal([]byte(params), &h.Params)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// UndoResult describes an undone action.
type UndoResult struct {
	HistoryEntry
	Restored int `json:"restored"` // models put back
	// Skipped: models changed since in another way (left as they are) or gone.
	Skipped int `json:"skipped"`
}

// Undo reverts the newest action that is not undone yet.
func Undo(ctx context.Context, db *sql.DB) (UndoResult, error) {
	var res UndoResult
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		var state, params string
		err := tx.QueryRowContext(ctx, `SELECT id, at, action, coalesce(params, ''), models, state FROM journal
			WHERE undone_at IS NULL ORDER BY id DESC LIMIT 1`).
			Scan(&res.ID, &res.At, &res.Action, &params, &res.Models, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return msg.Wrap(nil, "history.nothingToUndo")
		}
		if err != nil {
			return err
		}
		if params != "" {
			json.Unmarshal([]byte(params), &res.Params)
		}
		var changes []stateChange
		if err := json.Unmarshal([]byte(state), &changes); err != nil {
			return err
		}
		var touched []int64
		for _, c := range changes {
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM model WHERE uid = ?`, c.Before.UID).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				res.Skipped++
				continue
			}
			if err != nil {
				return err
			}
			cur, ok, err := captureOne(ctx, tx, id)
			if err != nil {
				return err
			}
			if !ok || !sameState(cur, c.After) {
				res.Skipped++
				continue
			}
			if err := restoreState(ctx, tx, id, c.Before); err != nil {
				return err
			}
			touched = append(touched, id)
			res.Restored++
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE NOT EXISTS
			(SELECT 1 FROM model_tag mt WHERE mt.tag_id = tag.id)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE journal SET undone_at = ? WHERE id = ?`, time.Now().UnixMilli(), res.ID); err != nil {
			return err
		}
		res.Undone = true
		return touch(ctx, tx, touched)
	})
	return res, err
}
