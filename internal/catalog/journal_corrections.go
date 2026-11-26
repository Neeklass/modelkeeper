package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"modelkeeper/internal/msg"
)

// Undo for corrections of the detection (F4d: mark as model / not a model,
// split, detach, merge, reset; TODO 2.1). A correction changes override rows
// and then the detection reshapes the models; a merge also moves user data
// and the detection deletes the emptied sources. So the journal keeps:
//
//   - the override rows the correction added and removed,
//   - for every model it affects: its uid, its places (anchors) and its user
//     data before; and after the detection ran (FinishCorrection).
//
// Undo removes the added rows, puts the removed ones back, lets the caller
// run the detection, and then gives each model its user data back – found by
// its uid, or at its place when the correction had removed it (the model at
// that place gets the old uid back too, so exports stay consistent). A model
// that was changed in another way since is left as it is.

// CorrectionPrefix starts the journal action of a correction.
const CorrectionPrefix = "history.correction."

type overrideRow struct {
	Kind     string `json:"kind"`
	Root     int64  `json:"root"`
	Path     string `json:"path"`
	ModelUID string `json:"modelUid,omitempty"`
	Data     string `json:"data,omitempty"`
}

func (o overrideRow) key() string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", o.Root, o.Path, o.Kind, o.ModelUID, o.Data)
}

type placeRef struct {
	Root int64  `json:"root"`
	Path string `json:"path"`
}

type correctionModel struct {
	UID     string      `json:"uid"`
	Places  []placeRef  `json:"places"`
	Before  modelState  `json:"before"`
	After   *modelState `json:"after,omitempty"` // nil: gone after the detection (merged away)
	Checked bool        `json:"checked"`         // After was taken
}

type correctionState struct {
	Roots    []int64           `json:"roots"`
	Added    []overrideRow     `json:"added"`
	Removed  []overrideRow     `json:"removed"`
	Models   []correctionModel `json:"models"`
	Finished bool              `json:"finished"`
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func overridesOf(ctx context.Context, q queryer, roots []int64) (map[string]overrideRow, error) {
	out := map[string]overrideRow{}
	for _, r := range roots {
		rows, err := q.QueryContext(ctx, `SELECT o.kind, o.root_id, o.path, coalesce(m.uid, ''), coalesce(o.data, '')
			FROM override o LEFT JOIN model m ON m.id = o.model_id WHERE o.root_id = ?`, r)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o overrideRow
			if err := rows.Scan(&o.Kind, &o.Root, &o.Path, &o.ModelUID, &o.Data); err != nil {
				rows.Close()
				return nil, err
			}
			out[o.key()] = o
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func placesOf(ctx context.Context, q queryer, id int64) ([]placeRef, error) {
	rows, err := q.QueryContext(ctx, `SELECT root_id, path FROM model_anchor WHERE model_id = ? ORDER BY root_id, path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []placeRef
	for rows.Next() {
		var p placeRef
		if err := rows.Scan(&p.Root, &p.Path); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ModelsAt returns the models placed at path in root, below it, or in a
// folder above it: the ones a correction at path can affect.
func ModelsAt(ctx context.Context, tx *sql.Tx, rootID int64, path string) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT model_id FROM model_anchor WHERE root_id = ?1
		AND (path = ?2 OR substr(path, 1, length(?2) + 1) = ?2 || '/' OR substr(?2, 1, length(path) + 1) = path || '/')
		ORDER BY model_id`, rootID, path)
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

// JournalCorrection runs change – which writes override rows on roots – in
// tx and records it as one undoable action. models are the models it
// affects (their user data is kept for the undo).
func JournalCorrection(ctx context.Context, tx *sql.Tx, action string, params map[string]any,
	roots, models []int64, change func() error) error {
	before, err := overridesOf(ctx, tx, roots)
	if err != nil {
		return err
	}
	var cms []correctionModel
	seen := map[int64]bool{}
	for _, id := range models {
		if seen[id] {
			continue
		}
		seen[id] = true
		s, ok, err := captureOne(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		places, err := placesOf(ctx, tx, id)
		if err != nil {
			return err
		}
		cms = append(cms, correctionModel{UID: s.UID, Places: places, Before: s})
	}
	if err := change(); err != nil {
		return err
	}
	after, err := overridesOf(ctx, tx, roots)
	if err != nil {
		return err
	}
	st := correctionState{Roots: roots, Added: []overrideRow{}, Removed: []overrideRow{}, Models: cms}
	for k, o := range after {
		if _, ok := before[k]; !ok {
			st.Added = append(st.Added, o)
		}
	}
	for k, o := range before {
		if _, ok := after[k]; !ok {
			st.Removed = append(st.Removed, o)
		}
	}
	if len(st.Added) == 0 && len(st.Removed) == 0 {
		// Nothing changed in the overrides; a merge may still have moved user
		// data, which the models' states cover.
		changed := false
		for _, m := range cms {
			if cur, ok, err := captureByUID(ctx, tx, m.UID); err != nil {
				return err
			} else if !ok || !sameState(cur, m.Before) {
				changed = true
				break
			}
		}
		if !changed {
			return nil
		}
	}
	sortRows(st.Added)
	sortRows(st.Removed)
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	var p any
	if len(params) > 0 {
		pb, err := json.Marshal(params)
		if err != nil {
			return err
		}
		p = string(pb)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO journal (at, action, params, models, state) VALUES (?, ?, ?, ?, ?)`,
		time.Now().UnixMilli(), action, p, len(cms), string(b)); err != nil {
		return err
	}
	return pruneJournal(ctx, tx)
}

func sortRows(rows []overrideRow) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].key() < rows[j].key() })
}

func captureByUID(ctx context.Context, tx *sql.Tx, uid string) (modelState, bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM model WHERE uid = ?`, uid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return modelState{}, false, nil
	}
	if err != nil {
		return modelState{}, false, err
	}
	return captureOne(ctx, tx, id)
}

// FinishCorrection records the models' states after the detection ran for
// the newest correction (called by whoever ran the detection; when the
// detection follows later, right away – the states are then those before
// the detection, which undo treats the same way).
func FinishCorrection(ctx context.Context, db *sql.DB) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		var id int64
		var action, state string
		err := tx.QueryRowContext(ctx, `SELECT id, action, state FROM journal WHERE undone_at IS NULL
			ORDER BY id DESC LIMIT 1`).Scan(&id, &action, &state)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !strings.HasPrefix(action, CorrectionPrefix)) {
			return nil
		}
		if err != nil {
			return err
		}
		var st correctionState
		if err := json.Unmarshal([]byte(state), &st); err != nil || st.Finished {
			return err
		}
		for i := range st.Models {
			m := &st.Models[i]
			cur, ok, err := captureByUID(ctx, tx, m.UID)
			if err != nil {
				return err
			}
			m.Checked = true
			if ok {
				c := cur
				m.After = &c
			}
		}
		st.Finished = true
		b, err := json.Marshal(st)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE journal SET state = ? WHERE id = ?`, string(b), id)
		return err
	})
}

// undoCorrection reverts a correction (see the top of the file). detect runs
// the detection for the roots.
func undoCorrection(ctx context.Context, db *sql.DB, res *UndoResult, state string,
	detect func(ctx context.Context, roots []int64) error) error {
	var st correctionState
	if err := json.Unmarshal([]byte(state), &st); err != nil {
		return err
	}
	if detect == nil {
		return msg.Wrap(nil, "history.needsDetection")
	}
	// 1. The override rows as before.
	if err := inTx(ctx, db, func(tx *sql.Tx) error {
		for _, o := range st.Added {
			if _, err := tx.ExecContext(ctx, `DELETE FROM override WHERE kind = ? AND root_id = ? AND path = ?
				AND coalesce((SELECT uid FROM model WHERE id = override.model_id), '') = ?`,
				o.Kind, o.Root, o.Path, o.ModelUID); err != nil {
				return err
			}
		}
		for _, o := range st.Removed {
			var modelID any
			if o.ModelUID != "" {
				var id int64
				err := tx.QueryRowContext(ctx, `SELECT id FROM model WHERE uid = ?`, o.ModelUID).Scan(&id)
				if errors.Is(err, sql.ErrNoRows) {
					continue // its model is gone; the correction cannot come back
				}
				if err != nil {
					return err
				}
				modelID = id
			}
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM override WHERE root_id = ? AND path = ? AND kind IN (`+
				conflictingKinds(o.Kind)+`)`, o.Root, o.Path).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				continue // decided otherwise since
			}
			var data any
			if o.Data != "" {
				data = o.Data
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO override (kind, root_id, path, model_id, data, created_at)
				VALUES (?, ?, ?, ?, ?, ?)`, o.Kind, o.Root, o.Path, modelID, data, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE journal SET undone_at = ? WHERE id = ?`, time.Now().UnixMilli(), res.ID)
		return err
	}); err != nil {
		return err
	}
	res.Undone = true

	// 2. The detection reshapes the models.
	var roots []int64
	for _, r := range st.Roots {
		var removed bool
		if err := db.QueryRowContext(ctx, `SELECT removed_at IS NOT NULL FROM root WHERE id = ?`, r).Scan(&removed); err == nil && !removed {
			roots = append(roots, r)
		}
	}
	if err := detect(ctx, roots); err != nil {
		return err
	}

	// 3. Each model gets its user data back.
	return inTx(ctx, db, func(tx *sql.Tx) error {
		var touched []int64
		for _, m := range st.Models {
			id, err := findModel(ctx, tx, m)
			if err != nil {
				return err
			}
			if id == 0 {
				res.Skipped++
				continue
			}
			cur, _, err := captureOne(ctx, tx, id)
			if err != nil {
				return err
			}
			// Changed since? Compared with the state after the correction;
			// a model the correction removed has nothing to compare with.
			if m.After != nil && cur.UID == m.UID && !sameState(cur, *m.After) {
				res.Skipped++
				continue
			}
			if cur.UID != m.UID {
				if _, err := tx.ExecContext(ctx, `UPDATE model SET uid = ? WHERE id = ?
					AND NOT EXISTS (SELECT 1 FROM model WHERE uid = ?)`, m.UID, id, m.UID); err != nil {
					return err
				}
			}
			if err := restoreState(ctx, tx, id, m.Before); err != nil {
				return err
			}
			touched = append(touched, id)
			res.Restored++
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE NOT EXISTS
			(SELECT 1 FROM model_tag mt WHERE mt.tag_id = tag.id)`); err != nil {
			return err
		}
		return touch(ctx, tx, touched)
	})
}

// findModel finds a recorded model: by uid, else the model at one of its places.
func findModel(ctx context.Context, tx *sql.Tx, m correctionModel) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM model WHERE uid = ?`, m.UID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	for _, p := range m.Places {
		err := tx.QueryRowContext(ctx, `SELECT model_id FROM model_anchor WHERE root_id = ? AND path = ?`, p.Root, p.Path).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	return 0, nil
}

func conflictingKinds(kind string) string {
	switch kind {
	case "is_model", "not_model":
		return `'is_model', 'not_model'`
	default:
		return `'merge', 'split'`
	}
}
