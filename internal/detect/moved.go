package detect

import (
	"context"
	"database/sql"
)

// Moved files (F6) beyond a rename within one scan. A model whose files are
// all gone from this root (an orphan that matched nothing in this run) is
// resolved in one of two ways:
//
//   - It still has present files on another root: it was moved there (e.g.
//     from the PC to the NAS). Its stale anchor and missing files on this root
//     no longer belong to it, so it is not shown with missing files forever;
//     a model left in one place shows up in the single-location filter (D5).
//
//   - Otherwise, if it carries user data and the same content now belongs to
//     a model without user data in this root, the files were copied first and
//     the original deleted later (Explorer moves across drives, or a scan in
//     between). That newer model is folded into the original, which keeps its
//     ID, tags, notes and status (D6).
//
// A model with nowhere to go keeps its missing files, so the loss is visible.

// UserDataCond is true for the model "m" if it has anything the user entered
// (the counterpart of the garbage condition in Cleanup). Also used to count
// what permanently deleting a root folder would leave without a location.
const UserDataCond = `(m.notes <> '' OR m.name_manual = 1 OR m.creator_manual = 1 OR m.supports_manual = 1
	OR m.print_status <> 'not_printed' OR coalesce(m.thumb_source, '') = 'manual' OR m.favorite = 1
	OR EXISTS (SELECT 1 FROM model_tag t WHERE t.model_id = m.id)
	OR EXISTS (SELECT 1 FROM override o WHERE o.model_id = m.id))`

func hasUserData(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM model m WHERE m.id = ? AND `+UserDataCond, id).Scan(&n)
	return n > 0, err
}

// resolveOrphans handles the orphans of this run; see above. candOf maps the
// models matched in this run to their candidate. Returns the number of models
// recognized as moved.
func resolveOrphans(ctx context.Context, tx *sql.Tx, w *writer, m *matcher, rootID int64,
	matched map[int64]bool, candOf map[int64]*Candidate) (int, error) {
	moved := 0
	adopted := map[int64]bool{} // models already folded into an orphan
	for id := range m.orphanModels {
		if matched[id] {
			continue
		}
		var elsewhere int
		// This root's present files were reassigned in this run, and the orphan
		// got none, so any present file of the model is on another root.
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM location WHERE model_id = ? AND present = 1`,
			id).Scan(&elsewhere); err != nil {
			return moved, err
		}
		if elsewhere > 0 {
			if err := detachRoot(ctx, tx, rootID, id); err != nil {
				return moved, err
			}
			moved++
			continue
		}
		keep, err := hasUserData(ctx, tx, id)
		if err != nil || !keep {
			if err != nil {
				return moved, err
			}
			continue // no user data: Cleanup removes it
		}
		target, err := copyOf(ctx, tx, m.files[id], matched, candOf, adopted)
		if err != nil {
			return moved, err
		}
		if target == 0 {
			continue
		}
		if err := adopt(ctx, tx, w, rootID, id, target, candOf[target]); err != nil {
			return moved, err
		}
		adopted[target] = true
		moved++
	}
	return moved, nil
}

// detachRoot removes a model's stale anchors and missing files on a root.
func detachRoot(ctx context.Context, tx *sql.Tx, rootID, modelID int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE location SET model_id = NULL, variant_id = NULL
		WHERE root_id = ? AND model_id = ? AND present = 0`, rootID, modelID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM model_anchor WHERE root_id = ? AND model_id = ?`, rootID, modelID)
	return err
}

// copyOf finds the model matched in this run that holds the orphan's content
// and has no user data of its own (same overlap rules as matching).
func copyOf(ctx context.Context, tx *sql.Tx, orphan map[int64]bool, matched map[int64]bool,
	candOf map[int64]*Candidate, adopted map[int64]bool) (int64, error) {
	if len(orphan) == 0 {
		return 0, nil
	}
	var bestID int64
	bestScore := 0.0
	for id := range matched {
		cd := candOf[id]
		if cd == nil || adopted[id] {
			continue
		}
		set := contentSet(cd)
		n := 0
		for fid := range set {
			if orphan[fid] {
				n++
			}
		}
		smaller, larger := len(set), len(orphan)
		if smaller > larger {
			smaller, larger = larger, smaller
		}
		if smaller == 0 || n == 0 {
			continue
		}
		score := float64(n) / float64(smaller)
		if score < MinOverlap || float64(n)/float64(larger) < 0.5 || (n < 2 && larger > 1) {
			continue
		}
		if score > bestScore || (score == bestScore && id < bestID) {
			data, err := hasUserData(ctx, tx, id)
			if err != nil {
				return 0, err
			}
			if !data {
				bestID, bestScore = id, score
			}
		}
	}
	return bestID, nil
}

// adopt folds model from into model into: files, variants, anchors and tag
// suggestions move over, from is deleted. into keeps its ID and user data.
func adopt(ctx context.Context, tx *sql.Tx, w *writer, rootID, into, from int64, cd *Candidate) error {
	// The original's old files on this root are gone (see writer.anchor).
	if err := detachRoot(ctx, tx, rootID, into); err != nil {
		return err
	}
	for _, q := range []string{
		// Variants: reuse the original's variant of the same label, move the rest.
		`UPDATE location SET variant_id = (SELECT o.id FROM variant o JOIN variant f ON f.label = o.label
			WHERE o.model_id = ?1 AND f.id = location.variant_id)
			WHERE variant_id IN (SELECT f.id FROM variant f JOIN variant o ON o.label = f.label
				WHERE f.model_id = ?2 AND o.model_id = ?1)`,
		`UPDATE variant SET model_id = ?1 WHERE model_id = ?2
			AND label NOT IN (SELECT label FROM variant WHERE model_id = ?1)`,
		`UPDATE location SET model_id = ?1 WHERE model_id = ?2`,
		`UPDATE model_anchor SET model_id = ?1 WHERE model_id = ?2`,
		`INSERT OR IGNORE INTO tag_suggestion (model_id, name, source, state)
			SELECT ?1, name, source, state FROM tag_suggestion WHERE model_id = ?2`,
		`DELETE FROM model WHERE id = ?2`,
	} {
		if _, err := tx.ExecContext(ctx, q, into, from); err != nil {
			return err
		}
	}
	if cd != nil {
		return w.updateModel(into, cd) // detected name etc. of the new place, unless edited
	}
	return nil
}
