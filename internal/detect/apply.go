package detect

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MinOverlap is the share of identical content files (relative to the
// smaller model) above which two models count as the same model.
const MinOverlap = 0.8

// Result summarizes one detection run.
type Result struct {
	Models  int `json:"models"`  // candidates found in the root
	Created int `json:"created"` // new models
	Moved   int `json:"moved"`   // matched by content after a rename/move (F6)
	Linked  int `json:"linked"`  // matched by content to a model on another root (D3)
}

// Apply detects models for a root and stores them in one transaction.
// Model IDs stay stable: an existing anchor (root+path) keeps its model; a
// moved folder is recognized by content; the same model on another root is
// linked instead of duplicated. Fields the user edited are never overwritten.
func Apply(ctx context.Context, db *sql.DB, rootID int64, rules Rules) (Result, error) {
	var res Result
	var creatorLevel bool
	if err := db.QueryRowContext(ctx, `SELECT creator_level FROM root WHERE id = ?`, rootID).Scan(&creatorLevel); err != nil {
		return res, err
	}
	rules.CreatorLevel = creatorLevel

	entries, err := loadEntries(ctx, db, rootID)
	if err != nil {
		return res, err
	}
	cands := Detect(entries, rules)
	res.Models = len(cands)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	m, err := newMatcher(ctx, tx, rootID, cands)
	if err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE location SET model_id = NULL, variant_id = NULL
		WHERE root_id = ? AND present = 1`, rootID); err != nil {
		return res, err
	}

	w, err := newWriter(ctx, tx)
	if err != nil {
		return res, err
	}
	defer w.close()

	touched := map[int64]bool{}
	for i := range cands {
		cd := &cands[i]
		modelID, how := m.match(cd)
		switch how {
		case matchNew:
			if modelID, err = w.createModel(cd); err != nil {
				return res, err
			}
			res.Created++
		case matchMoved:
			res.Moved++
		case matchLinked:
			res.Linked++
		}
		if err := w.updateModel(modelID, cd); err != nil {
			return res, err
		}
		if err := w.anchor(rootID, modelID, cd, m.movedFrom[cd.Anchor]); err != nil {
			return res, err
		}
		if err := w.assign(modelID, cd); err != nil {
			return res, err
		}
		for _, t := range cd.Tags {
			if _, err := w.tx.ExecContext(ctx, `INSERT OR IGNORE INTO tag_suggestion (model_id, name, source)
				VALUES (?, ?, 'folder')`, modelID, t); err != nil {
				return res, err
			}
		}
		touched[modelID] = true
	}
	// Models that were anchored here before but lost their files are touched too
	// (they may become garbage or need a search-index update).
	for id := range m.orphanModels {
		touched[id] = true
	}

	if err := Cleanup(ctx, tx); err != nil {
		return res, err
	}
	if err := refreshSearch(ctx, tx, touched); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

func loadEntries(ctx context.Context, db *sql.DB, rootID int64) ([]Entry, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, path, category, coalesce(file_id, 0), coalesce(archive_id, 0)
		FROM location WHERE root_id = ? AND present = 1 ORDER BY archive_id IS NOT NULL, id`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	idx := map[int64]int{}
	for rows.Next() {
		var id, fid, archiveID int64
		var p, cat string
		if err := rows.Scan(&id, &p, &cat, &fid, &archiveID); err != nil {
			return nil, err
		}
		if archiveID == 0 {
			idx[id] = len(entries)
			entries = append(entries, Entry{LocID: id, Path: p, Category: cat, FileID: fid})
			continue
		}
		if i, ok := idx[archiveID]; ok {
			entries[i].Members = append(entries[i].Members, Member{LocID: id, Path: p, Category: cat, FileID: fid})
		}
	}
	return entries, rows.Err()
}

// --- matching ---

type matchKind int

const (
	matchAnchor matchKind = iota // same root + path as before
	matchMoved                   // anchor gone, same content found elsewhere in this root
	matchLinked                  // same content on another root
	matchNew
)

type matcher struct {
	anchors      map[string]int64         // path -> model, this root
	orphanModels map[int64]bool           // models anchored in this root at paths no longer detected
	orphanPaths  map[int64][]string       // their anchor paths
	files        map[int64]map[int64]bool // model -> content file ids (orphans and other roots)
	byFile       map[int64][]int64        // file id -> orphan models
	byFileOther  map[int64][]int64        // file id -> models on other roots
	rootModels   map[int64]bool           // models that already have an anchor in this root
	used         map[int64]bool           // models claimed in this run
	movedFrom    map[string]string        // new anchor path -> old anchor path
}

func newMatcher(ctx context.Context, tx *sql.Tx, rootID int64, cands []Candidate) (*matcher, error) {
	m := &matcher{
		anchors: map[string]int64{}, orphanModels: map[int64]bool{}, orphanPaths: map[int64][]string{},
		files: map[int64]map[int64]bool{}, byFile: map[int64][]int64{}, byFileOther: map[int64][]int64{},
		rootModels: map[int64]bool{}, used: map[int64]bool{}, movedFrom: map[string]string{},
	}
	detected := map[string]bool{}
	for _, c := range cands {
		detected[c.Anchor] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT path, model_id FROM model_anchor WHERE root_id = ?`, rootID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		var id int64
		if err := rows.Scan(&p, &id); err != nil {
			rows.Close()
			return nil, err
		}
		m.anchors[p] = id
		m.rootModels[id] = true
		if !detected[p] {
			m.orphanModels[id] = true
			m.orphanPaths[id] = append(m.orphanPaths[id], p)
		}
	}
	rows.Close()
	// A model whose anchor is still detected is not an orphan, even if it had a second anchor.
	for p, id := range m.anchors {
		if detected[p] {
			delete(m.orphanModels, id)
		}
	}

	// Content of orphan models (their old locations in this root, present or not)
	// and of models on other roots.
	rows, err = tx.QueryContext(ctx, `SELECT model_id, file_id, root_id = ? FROM location
		WHERE model_id IS NOT NULL AND file_id IS NOT NULL AND category IN ('model', 'slicer', 'archive')`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid, fid int64
		var here bool
		if err := rows.Scan(&mid, &fid, &here); err != nil {
			return nil, err
		}
		switch {
		case here && m.orphanModels[mid]:
			m.byFile[fid] = appendUnique(m.byFile[fid], mid)
		case !here && !m.rootModels[mid]:
			m.byFileOther[fid] = appendUnique(m.byFileOther[fid], mid)
		default:
			continue
		}
		if m.files[mid] == nil {
			m.files[mid] = map[int64]bool{}
		}
		m.files[mid][fid] = true
	}
	return m, rows.Err()
}

func appendUnique(s []int64, v int64) []int64 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func contentSet(cd *Candidate) map[int64]bool {
	set := map[int64]bool{}
	for _, a := range cd.Assign {
		if a.Content && a.FileID != 0 {
			set[a.FileID] = true
		}
	}
	return set
}

// best returns the unused model with the highest overlap >= MinOverlap.
func (m *matcher) best(set map[int64]bool, index map[int64][]int64) int64 {
	common := map[int64]int{}
	for fid := range set {
		for _, mid := range index[fid] {
			if !m.used[mid] {
				common[mid]++
			}
		}
	}
	var bestID int64
	bestScore := 0.0
	for mid, n := range common {
		smaller, larger := len(set), len(m.files[mid])
		if smaller > larger {
			smaller, larger = larger, smaller
		}
		if smaller == 0 {
			continue
		}
		// Mostly the same content, not just a shared base or a tiny subset.
		// A single shared file only counts if both sides are that one file.
		score := float64(n) / float64(smaller)
		if score < MinOverlap || float64(n)/float64(larger) < 0.5 || (n < 2 && larger > 1) {
			continue
		}
		if score > bestScore || (score == bestScore && mid < bestID) {
			bestID, bestScore = mid, score
		}
	}
	return bestID
}

func (m *matcher) match(cd *Candidate) (int64, matchKind) {
	if id, ok := m.anchors[cd.Anchor]; ok {
		m.used[id] = true
		return id, matchAnchor
	}
	set := contentSet(cd)
	if len(set) > 0 {
		if id := m.best(set, m.byFile); id != 0 {
			m.used[id] = true
			delete(m.orphanModels, id)
			if paths := m.orphanPaths[id]; len(paths) > 0 {
				m.movedFrom[cd.Anchor] = paths[0]
				m.orphanPaths[id] = paths[1:]
			}
			return id, matchMoved
		}
		if id := m.best(set, m.byFileOther); id != 0 {
			m.used[id] = true
			return id, matchLinked
		}
	}
	return 0, matchNew
}

// --- writing ---

type writer struct {
	ctx      context.Context
	tx       *sql.Tx
	setLoc   *sql.Stmt
	creators map[string]int64
	variants map[string]int64 // "model/label" -> id
	now      int64
}

func newWriter(ctx context.Context, tx *sql.Tx) (*writer, error) {
	st, err := tx.PrepareContext(ctx, `UPDATE location SET model_id = ?, variant_id = ? WHERE id = ?`)
	if err != nil {
		return nil, err
	}
	return &writer{ctx: ctx, tx: tx, setLoc: st, creators: map[string]int64{}, variants: map[string]int64{},
		now: time.Now().UnixMilli()}, nil
}

func (w *writer) close() { w.setLoc.Close() }

func (w *writer) creatorID(name string) (any, error) {
	if name == "" {
		return nil, nil
	}
	key := strings.ToLower(name)
	if id, ok := w.creators[key]; ok {
		return id, nil
	}
	var id int64
	err := w.tx.QueryRowContext(w.ctx, `INSERT INTO creator (name) VALUES (?)
		ON CONFLICT (name) DO UPDATE SET name = name RETURNING id`, name).Scan(&id)
	if err != nil {
		return nil, err
	}
	w.creators[key] = id
	return id, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (w *writer) createModel(cd *Candidate) (int64, error) {
	uid, err := uuid.NewV7()
	if err != nil {
		return 0, err
	}
	cid, err := w.creatorID(cd.Creator)
	if err != nil {
		return 0, err
	}
	var id int64
	err = w.tx.QueryRowContext(w.ctx, `INSERT INTO model (uid, name, creator_id, supports, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`, uid.String(), cd.Name, cid, nullable(cd.Supports), w.now, w.now).Scan(&id)
	return id, err
}

// updateModel refreshes detected fields unless the user edited them.
func (w *writer) updateModel(id int64, cd *Candidate) error {
	cid, err := w.creatorID(cd.Creator)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `UPDATE model SET
		name       = CASE WHEN name_manual     = 0 THEN ? ELSE name END,
		creator_id = CASE WHEN creator_manual  = 0 THEN ? ELSE creator_id END,
		supports   = CASE WHEN supports_manual = 0 THEN ? ELSE supports END,
		updated_at = ?
		WHERE id = ?`, cd.Name, cid, nullable(cd.Supports), w.now, id)
	return err
}

func (w *writer) anchor(rootID, modelID int64, cd *Candidate, oldPath string) error {
	if oldPath != "" {
		// Moved (F6): the old, now missing locations no longer belong to the model.
		if _, err := w.tx.ExecContext(w.ctx, `UPDATE location SET model_id = NULL, variant_id = NULL
			WHERE root_id = ? AND model_id = ? AND present = 0`, rootID, modelID); err != nil {
			return err
		}
		_, err := w.tx.ExecContext(w.ctx, `UPDATE model_anchor SET path = ?, kind = ? WHERE root_id = ? AND path = ?`,
			cd.Anchor, cd.Kind, rootID, oldPath)
		return err
	}
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO model_anchor (model_id, root_id, path, kind) VALUES (?, ?, ?, ?)
		ON CONFLICT (root_id, path) DO UPDATE SET kind = excluded.kind`, modelID, rootID, cd.Anchor, cd.Kind)
	return err
}

func (w *writer) variantID(modelID int64, label string) (any, error) {
	if label == "" {
		return nil, nil
	}
	key := fmt.Sprintf("%d/%s", modelID, strings.ToLower(label))
	if id, ok := w.variants[key]; ok {
		return id, nil
	}
	var id int64
	err := w.tx.QueryRowContext(w.ctx, `INSERT INTO variant (model_id, label, sort) VALUES (?, ?, ?)
		ON CONFLICT (model_id, label) DO UPDATE SET sort = excluded.sort RETURNING id`,
		modelID, label, VariantSort(label)).Scan(&id)
	if err != nil {
		return nil, err
	}
	w.variants[key] = id
	return id, nil
}

func (w *writer) assign(modelID int64, cd *Candidate) error {
	for _, a := range cd.Assign {
		vid, err := w.variantID(modelID, a.Variant)
		if err != nil {
			return err
		}
		if _, err := w.setLoc.ExecContext(w.ctx, modelID, vid, a.LocID); err != nil {
			return err
		}
	}
	return nil
}

// Cleanup removes models without any location and without user data, plus
// unused variants, creators and search rows.
func Cleanup(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`DELETE FROM model WHERE NOT EXISTS (SELECT 1 FROM location l WHERE l.model_id = model.id)
			AND notes = '' AND name_manual = 0 AND creator_manual = 0 AND supports_manual = 0
			AND print_status = 'not_printed' AND coalesce(thumb_source, '') <> 'manual'
			AND NOT EXISTS (SELECT 1 FROM model_tag t WHERE t.model_id = model.id)
			AND NOT EXISTS (SELECT 1 FROM override o WHERE o.model_id = model.id)`,
		`DELETE FROM variant WHERE NOT EXISTS (SELECT 1 FROM location l WHERE l.variant_id = variant.id)`,
		`DELETE FROM creator WHERE NOT EXISTS (SELECT 1 FROM model m WHERE m.creator_id = creator.id)`,
		`DELETE FROM model_fts WHERE rowid NOT IN (SELECT id FROM model)`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// refreshSearch rebuilds the full-text rows of the given models (H1).
func refreshSearch(ctx context.Context, tx *sql.Tx, ids map[int64]bool) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS fts_touched (id INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fts_touched`); err != nil {
		return err
	}
	list := make([]int64, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	st, err := tx.PrepareContext(ctx, `INSERT INTO fts_touched (id) VALUES (?)`)
	if err != nil {
		return err
	}
	for _, id := range list {
		if _, err := st.ExecContext(ctx, id); err != nil {
			st.Close()
			return err
		}
	}
	st.Close()
	return RefreshSearchFor(ctx, tx, `SELECT id FROM fts_touched`)
}

// RefreshSearchFor rebuilds the full-text rows of the models selected by idQuery.
func RefreshSearchFor(ctx context.Context, tx *sql.Tx, idQuery string, args ...any) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_fts WHERE rowid IN (`+idQuery+`)`, args...); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO model_fts (rowid, name, creator, tags, notes, paths)
		SELECT m.id, m.name, coalesce(c.name, ''),
			coalesce((SELECT group_concat(t.name, ' ') FROM model_tag mt JOIN tag t ON t.id = mt.tag_id
				WHERE mt.model_id = m.id), ''),
			m.notes,
			coalesce((SELECT group_concat(a.path, ' ') FROM model_anchor a WHERE a.model_id = m.id), '') || ' ' ||
			coalesce((SELECT group_concat(l.path, ' ') FROM location l WHERE l.model_id = m.id
				AND l.category IN ('model', 'slicer', 'archive')), '')
		FROM model m LEFT JOIN creator c ON c.id = m.creator_id
		WHERE m.id IN (`+idQuery+`)`, args...)
	return err
}
