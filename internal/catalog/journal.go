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

// journalKeep is the number of journal rows kept; journalMaxBytes bounds
// their total size (a bulk action on 20,000 models stores some megabytes,
// and the journal is part of every snapshot). The newest row always stays.
const journalKeep = 500

var journalMaxBytes = 50 << 20 // a variable for tests

// pruneJournal drops the oldest rows beyond journalKeep or journalMaxBytes.
func pruneJournal(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM journal WHERE id <= (SELECT max(id) FROM journal) - ?`, journalKeep); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM journal WHERE id IN (
			SELECT id FROM (SELECT id, sum(length(state)) OVER (ORDER BY id DESC) AS total FROM journal)
			WHERE total > ?)
		AND id < (SELECT max(id) FROM journal)`, journalMaxBytes)
	return err
}

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

// capture reads the states of many models with a few queries per 500
// models (not per model: a bulk action may touch tens of thousands). The
// result equals captureOne for each model.
func capture(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]modelState, error) {
	out := make(map[int64]modelState, len(ids))
	seen := make(map[int64]bool, len(ids))
	uniq := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	for start := 0; start < len(uniq); start += captureChunk {
		part := uniq[start:min(start+captureChunk, len(uniq))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		in := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		rows, err := tx.QueryContext(ctx, `SELECT m.id, m.uid, m.name, m.name_manual, coalesce(c.name, ''), m.creator_manual,
			m.notes, m.favorite, m.print_status, coalesce(m.supports, ''), m.supports_manual,
			coalesce(m.origin, ''), coalesce(m.origin_url, ''), coalesce(m.license, '')
			FROM model m LEFT JOIN creator c ON c.id = m.creator_id WHERE m.id IN (`+in+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var st modelState
			if err := rows.Scan(&id, &st.UID, &st.Name, &st.NameManual, &st.Creator, &st.CreatorManual, &st.Notes,
				&st.Favorite, &st.PrintStatus, &st.Supports, &st.SupportsManual, &st.Origin, &st.OriginURL, &st.License); err != nil {
				rows.Close()
				return nil, err
			}
			st.Tags, st.Dismissed = []string{}, []string{}
			out[id] = st
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		add := func(q string, set func(st *modelState, v string)) error {
			rows, err := tx.QueryContext(ctx, q, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id int64
				var v string
				if err := rows.Scan(&id, &v); err != nil {
					return err
				}
				if st, ok := out[id]; ok {
					set(&st, v)
					out[id] = st
				}
			}
			return rows.Err()
		}
		if err := add(`SELECT mt.model_id, t.name FROM model_tag mt JOIN tag t ON t.id = mt.tag_id
			WHERE mt.model_id IN (`+in+`) ORDER BY mt.model_id, t.name`,
			func(st *modelState, v string) { st.Tags = append(st.Tags, v) }); err != nil {
			return nil, err
		}
		if err := add(`SELECT model_id, name FROM tag_suggestion WHERE state = 'dismissed'
			AND model_id IN (`+in+`) ORDER BY model_id, name`,
			func(st *modelState, v string) { st.Dismissed = append(st.Dismissed, v) }); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// captureChunk is the number of models per capture query.
const captureChunk = 500

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
	return writeJournal(ctx, tx, action, params, changes)
}

// writeJournal stores one action with its model changes (nothing if empty).
func writeJournal(ctx context.Context, tx *sql.Tx, action string, params map[string]any, changes []stateChange) error {
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
	return pruneJournal(ctx, tx)
}

// Recorder journals a change whose models are only known while it runs (an
// import or a sync, J9): Before is called for each existing model before it is
// changed, Write after the change records one undoable action. Models created
// by the change are not recorded (undo keeps them; they come from files).
type Recorder struct {
	before map[int64]modelState
	order  []int64
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder { return &Recorder{before: map[int64]modelState{}} }

// Before captures the user data of model id (once; later calls are ignored).
func (r *Recorder) Before(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, ok := r.before[id]; ok {
		return nil
	}
	s, ok, err := captureOne(ctx, tx, id)
	if err != nil || !ok {
		return err
	}
	r.before[id] = s
	r.order = append(r.order, id)
	return nil
}

// Write records the changed models as one action and returns their number.
func (r *Recorder) Write(ctx context.Context, tx *sql.Tx, action string, params map[string]any) (int, error) {
	after, err := capture(ctx, tx, r.order)
	if err != nil {
		return 0, err
	}
	var changes []stateChange
	for _, id := range r.order {
		a, ok := after[id]
		if b := r.before[id]; ok && !sameState(b, a) {
			changes = append(changes, stateChange{Before: b, After: a})
		}
	}
	if params == nil {
		params = map[string]any{}
	}
	params["count"] = len(changes)
	return len(changes), writeJournal(ctx, tx, action, params, changes)
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

// NextUndo returns the action Undo would revert (msg "history.nothingToUndo"
// if there is none). Corrections (Action starting with CorrectionPrefix) need
// the detection: the caller runs Undo for them without a scan running.
func NextUndo(ctx context.Context, db *sql.DB) (HistoryEntry, error) {
	var h HistoryEntry
	var params string
	err := db.QueryRowContext(ctx, `SELECT id, at, action, coalesce(params, ''), models FROM journal
		WHERE undone_at IS NULL ORDER BY id DESC LIMIT 1`).Scan(&h.ID, &h.At, &h.Action, &params, &h.Models)
	if errors.Is(err, sql.ErrNoRows) {
		return h, msg.Wrap(nil, "history.nothingToUndo")
	}
	if params != "" {
		json.Unmarshal([]byte(params), &h.Params)
	}
	return h, err
}

// Undo reverts the newest action that is not undone yet. detect runs the
// detection for roots; it is needed to undo a correction (nil otherwise).
func Undo(ctx context.Context, db *sql.DB, detect func(ctx context.Context, roots []int64) error) (UndoResult, error) {
	var res UndoResult
	h, err := NextUndo(ctx, db)
	if err != nil {
		return res, err
	}
	res.HistoryEntry = h
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM journal WHERE id = ?`, h.ID).Scan(&state); err != nil {
		return res, err
	}
	if strings.HasPrefix(h.Action, CorrectionPrefix) {
		err := undoCorrection(ctx, db, &res, state, detect)
		return res, err
	}
	err = inTx(ctx, db, func(tx *sql.Tx) error {
		// Still the newest one (nothing happened in between)?
		var undone sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT undone_at FROM journal WHERE id = ?`, h.ID).Scan(&undone); err != nil {
			return err
		}
		if undone.Valid {
			return msg.Wrap(nil, "history.nothingToUndo")
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
