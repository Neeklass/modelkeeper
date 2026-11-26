package userexport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/pathutil"
)

// Import never deletes or overwrites anything the user entered in the target
// catalog: tags are added, a value is only taken over where the target has
// none (or only a detected one), and differing notes are kept both. So an
// import can be repeated and can be used to bring back what a mistake removed.

// Root choices on import.
const (
	ActionExisting = "existing" // map to a root folder of this catalog (RootID)
	ActionAdd      = "add"      // add Path as a new root folder; it is scanned after the import
	ActionSkip     = "skip"     // ignore; the models are still imported and found by content later
)

// RootChoice says what happens with an exported root folder.
type RootChoice struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	RootID int64  `json:"rootId,omitempty"`
	Path   string `json:"path,omitempty"`
}

// RootPlan describes an exported root folder for the import dialog.
type RootPlan struct {
	Key       string     `json:"key"`
	Path      string     `json:"path"`
	Kind      string     `json:"kind"`
	Removed   bool       `json:"removed"`
	Models    int        `json:"models"`    // exported models anchored there
	Overrides int        `json:"overrides"` // corrections there
	Suggested RootChoice `json:"suggested"`
	// Exists: the exported path is reachable from this PC.
	Exists bool `json:"exists"`
}

// Counts says how the exported models were (or would be) matched.
type Counts struct {
	ByID      int `json:"byId"`      // same model ID
	ByPath    int `json:"byPath"`    // same place (mapped root + path)
	ByContent int `json:"byContent"` // same files (hashes)
	// New: not in this catalog yet; kept with their data and linked up when a
	// scan finds their files (by place or by content).
	New int `json:"new"`
	// NotesKept: both catalogs had different notes; both texts are kept.
	NotesKept int `json:"notesKept"`
}

// Plan is the preview of an import.
type Plan struct {
	AppVersion  string     `json:"appVersion"`
	ExportedAt  string     `json:"exportedAt"`
	Models      int        `json:"models"`
	Tags        int        `json:"tags"`
	Overrides   int        `json:"overrides"`
	Thumbnails  int        `json:"thumbnails"`
	HasSettings bool       `json:"hasSettings"`
	Roots       []RootPlan `json:"roots"`
	Match       Counts     `json:"match"`
}

// Result describes an applied import.
type Result struct {
	Match            Counts `json:"match"`
	Tags             int    `json:"tags"`             // tag assignments added
	Overrides        int    `json:"overrides"`        // corrections added
	OverridesSkipped int    `json:"overridesSkipped"` // root skipped or a local correction decides
	Thumbnails       int    `json:"thumbnails"`
	Prints           int    `json:"prints"`      // print log entries added
	Collections      int    `json:"collections"` // collection memberships added
	TagsRemoved      int    `json:"tagsRemoved"` // sync: tags the other PC removed
	// RootsAdded are new root folders (to be scanned); RootsChanged existing
	// ones with new corrections or places (to be detected again).
	RootsAdded   []int64 `json:"rootsAdded"`
	RootsChanged []int64 `json:"rootsChanged"`
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type localRoot struct {
	id      int64
	path    string
	removed bool
}

func localRoots(ctx context.Context, q queryer) ([]localRoot, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, path, removed_at IS NOT NULL FROM root ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []localRoot
	for rows.Next() {
		var r localRoot
		if err := rows.Scan(&r.id, &r.path, &r.removed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Preview matches the export against the catalog without changing anything.
// choices may be nil: then the suggested choices are used.
func Preview(ctx context.Context, db *sql.DB, e *Export, choices []RootChoice) (Plan, error) {
	p := Plan{AppVersion: e.AppVersion, ExportedAt: e.ExportedAt, Models: len(e.Models), Tags: len(e.Tags),
		Overrides: len(e.Overrides), HasSettings: len(e.Settings) > 0, Roots: []RootPlan{}}
	for _, m := range e.Models {
		if m.Thumbnail != nil {
			p.Thumbnails++
		}
	}
	locals, err := localRoots(ctx, db)
	if err != nil {
		return p, err
	}
	for _, r := range e.Roots {
		rp := RootPlan{Key: r.Key, Path: r.Path, Kind: r.Kind, Removed: r.Removed}
		if fi, err := os.Stat(r.Path); err == nil && fi.IsDir() {
			rp.Exists = true
		}
		rp.Suggested = RootChoice{Key: r.Key, Action: ActionSkip}
		for _, l := range locals {
			if pathutil.Same(l.path, r.Path) {
				rp.Suggested = RootChoice{Key: r.Key, Action: ActionExisting, RootID: l.id}
				break
			}
		}
		if rp.Suggested.Action == ActionSkip && rp.Exists && !r.Removed {
			rp.Suggested = RootChoice{Key: r.Key, Action: ActionAdd, Path: r.Path}
		}
		for _, m := range e.Models {
			for _, a := range m.Anchors {
				if a.Root == r.Key {
					rp.Models++
					break
				}
			}
		}
		for _, o := range e.Overrides {
			if o.Root == r.Key {
				rp.Overrides++
			}
		}
		p.Roots = append(p.Roots, rp)
	}
	if choices == nil {
		for _, rp := range p.Roots {
			choices = append(choices, rp.Suggested)
		}
	}
	rootID, err := mapRoots(e, choices, locals)
	if err != nil {
		return p, err
	}
	taken := map[int64]bool{}
	for i := range e.Models {
		m := &e.Models[i]
		id, how, err := match(ctx, db, m, rootID, taken)
		if err != nil {
			return p, err
		}
		count(&p.Match, how)
		if id != 0 {
			taken[id] = true
			var notes string
			if err := db.QueryRowContext(ctx, `SELECT notes FROM model WHERE id = ?`, id).Scan(&notes); err != nil {
				return p, err
			}
			if _, kept := mergeNotes(notes, m.Notes); kept {
				p.Match.NotesKept++
			}
		}
	}
	return p, nil
}

func count(c *Counts, how string) {
	switch how {
	case "id":
		c.ByID++
	case "path":
		c.ByPath++
	case "content":
		c.ByContent++
	default:
		c.New++
	}
}

// mapRoots resolves the choices to root ids of this catalog. Roots to be
// added are not known yet (0); unknown or skipped keys map to 0 as well.
func mapRoots(e *Export, choices []RootChoice, locals []localRoot) (map[string]int64, error) {
	known := map[int64]bool{}
	for _, l := range locals {
		known[l.id] = true
	}
	keys := map[string]bool{}
	for _, r := range e.Roots {
		keys[r.Key] = true
	}
	out := map[string]int64{}
	for _, c := range choices {
		if !keys[c.Key] {
			return nil, msg.Wrap(nil, "import.unknownRoot", "key", c.Key)
		}
		switch c.Action {
		case ActionExisting:
			if !known[c.RootID] {
				return nil, msg.Wrap(nil, "error.rootNotFound")
			}
			out[c.Key] = c.RootID
		case ActionAdd, ActionSkip, "":
		default:
			return nil, msg.Wrap(nil, "error.invalidValue")
		}
	}
	return out, nil
}

// match finds the local model an exported one belongs to: by uid, then by
// place (anchor on a mapped root), then by content. taken are local models
// already matched by another exported model; they are not matched by
// content again (two exported models never fold into one by guessing).
func match(ctx context.Context, q queryer, m *Model, rootID map[string]int64, taken map[int64]bool) (int64, string, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM model WHERE uid = ?`, m.UID).Scan(&id)
	if err == nil {
		return id, "id", nil
	}
	if err != sql.ErrNoRows {
		return 0, "", err
	}
	for _, a := range m.Anchors {
		rid := rootID[a.Root]
		if rid == 0 {
			continue
		}
		err := q.QueryRowContext(ctx, `SELECT model_id FROM model_anchor WHERE root_id = ? AND path = ?`, rid, a.Path).Scan(&id)
		if err == nil {
			return id, "path", nil
		}
		if err != sql.ErrNoRows {
			return 0, "", err
		}
	}
	id, err = matchContent(ctx, q, m.Content, taken)
	if err != nil || id == 0 {
		return 0, "", err
	}
	return id, "content", nil
}

// matchContent returns the local model that holds most of the content, if
// the overlap reaches detect.MinOverlap (as for moved files, F6).
func matchContent(ctx context.Context, q queryer, content []string, taken map[int64]bool) (int64, error) {
	if len(content) == 0 {
		return 0, nil
	}
	shared := map[int64]map[int64]bool{} // local model -> file ids it shares
	const chunk = 400
	for start := 0; start < len(content); start += chunk {
		part := content[start:min(start+chunk, len(content))]
		args := make([]any, 0, len(part))
		for _, h := range part {
			b, err := hex.DecodeString(h)
			if err != nil {
				return 0, err
			}
			args = append(args, b)
		}
		rows, err := q.QueryContext(ctx, `SELECT DISTINCT l.model_id, f.id FROM file f JOIN location l ON l.file_id = f.id
			WHERE f.sha256 IN (`+strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+`)
			AND l.model_id IS NOT NULL AND l.category IN `+contentCats, args...)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var mid, fid int64
			if err := rows.Scan(&mid, &fid); err != nil {
				rows.Close()
				return 0, err
			}
			if shared[mid] == nil {
				shared[mid] = map[int64]bool{}
			}
			shared[mid][fid] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}
	type cand struct {
		id int64
		n  int
	}
	var cands []cand
	for id, f := range shared {
		if !taken[id] {
			cands = append(cands, cand{id, len(f)})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].n != cands[j].n {
			return cands[i].n > cands[j].n
		}
		return cands[i].id < cands[j].id
	})
	for _, c := range cands {
		var size int
		if err := q.QueryRowContext(ctx, `SELECT count(DISTINCT file_id) FROM location
			WHERE model_id = ? AND file_id IS NOT NULL AND category IN `+contentCats, c.id).Scan(&size); err != nil {
			return 0, err
		}
		smaller := min(size, len(content))
		if smaller > 0 && float64(c.n)/float64(smaller) >= detect.MinOverlap {
			return c.id, nil
		}
	}
	return 0, nil
}

// mergeNotes combines local and imported notes. kept reports that both
// differed and both texts are kept.
func mergeNotes(local, imported string) (string, bool) {
	li, ii := strings.TrimSpace(local), strings.TrimSpace(imported)
	switch {
	case ii == "" || li == ii || strings.Contains(li, ii):
		return local, false
	case li == "":
		return imported, false
	default:
		return local + "\n\n" + imported, true
	}
}

// ApplyOptions are the places Apply needs.
type ApplyOptions struct {
	ThumbDir  string // data/thumbnails of this app
	SourceDir string // folder of the export file (a "thumbnails" folder next to it is used)
	DataDir   string // data folder (a new root folder must not overlap it)
	// Base: for a sync (J9), the export of the same other PC that was applied
	// last time. With it the import is a three-way merge: what that PC changed
	// since (also removed tags, unset favorites, cleared fields, models taken
	// out of a collection) is applied; what it did not change does not
	// overwrite this catalog. Without it the import only adds.
	Base *Export
}

// Apply imports the export into the catalog. Root folders to be added are
// added first (and are scanned by the caller afterwards); everything else
// happens in one transaction. The caller takes a snapshot before.
func Apply(ctx context.Context, db *sql.DB, e *Export, choices []RootChoice, o ApplyOptions) (Result, error) {
	res := Result{RootsAdded: []int64{}, RootsChanged: []int64{}}
	locals, err := localRoots(ctx, db)
	if err != nil {
		return res, err
	}
	rootID, err := mapRoots(e, choices, locals)
	if err != nil {
		return res, err
	}
	for _, c := range choices {
		if c.Action != ActionAdd {
			continue
		}
		r, err := catalog.AddRoot(ctx, db, c.Path, o.DataDir)
		if err != nil {
			return res, err
		}
		rootID[c.Key] = r.ID
		res.RootsAdded = append(res.RootsAdded, r.ID)
	}
	added := map[int64]bool{}
	for _, id := range res.RootsAdded {
		added[id] = true
	}
	changed := map[int64]bool{}

	type thumbJob struct {
		modelID int64
		uid     string
		t       *Thumbnail
	}
	var thumbJobs []thumbJob
	touched := map[int64]bool{}
	localOf := map[string]int64{} // exported uid -> local model

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	taken := map[int64]bool{}
	base := map[string]*Model{}
	if o.Base != nil {
		for i := range o.Base.Models {
			base[o.Base.Models[i].UID] = &o.Base.Models[i]
		}
	}

	for i := range e.Models {
		m := &e.Models[i]
		id, how, err := match(ctx, tx, m, rootID, taken)
		if err != nil {
			return res, err
		}
		count(&res.Match, how)
		var localUID, thumbSource string
		if id == 0 {
			if id, err = createModel(ctx, tx, m, now); err != nil {
				return res, fmt.Errorf("model %s: %w", m.UID, err)
			}
			localUID = m.UID
			for _, a := range m.Anchors {
				rid := rootID[a.Root]
				if rid == 0 {
					continue
				}
				origin := a.Origin
				if origin != "manual" {
					origin = "auto"
				}
				r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_anchor (model_id, root_id, path, kind, origin)
					VALUES (?, ?, ?, ?, ?)`, id, rid, a.Path, a.Kind, origin)
				if err != nil {
					return res, err
				}
				if n, _ := r.RowsAffected(); n > 0 && !added[rid] {
					changed[rid] = true
				}
			}
			for _, h := range m.Content {
				b, _ := hex.DecodeString(h) // checked by Decode
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_fingerprint (model_id, sha256) VALUES (?, ?)`,
					id, b); err != nil {
					return res, err
				}
			}
		} else {
			merge := mergeModel
			if b := base[m.UID]; b != nil {
				merge = func(ctx context.Context, tx *sql.Tx, id int64, m *Model, now int64) (bool, error) {
					return syncModel(ctx, tx, id, m, b, now)
				}
			}
			kept, err := merge(ctx, tx, id, m, now)
			if err != nil {
				return res, fmt.Errorf("model %s: %w", m.UID, err)
			}
			if kept {
				res.Match.NotesKept++
			}
			if err := tx.QueryRowContext(ctx, `SELECT uid, coalesce(thumb_source, '') FROM model WHERE id = ?`, id).
				Scan(&localUID, &thumbSource); err != nil {
				return res, err
			}
		}
		taken[id] = true
		localOf[m.UID] = id
		touched[id] = true

		tags := m.Tags
		if b := base[m.UID]; b != nil {
			// Only tags the other PC added since; the ones it removed go here too.
			tags = minus(m.Tags, b.Tags)
			for _, t := range minus(b.Tags, m.Tags) {
				if _, err := tx.ExecContext(ctx, `DELETE FROM model_tag WHERE model_id = ?
					AND tag_id = (SELECT id FROM tag WHERE name = ?)`, id, t); err != nil {
					return res, err
				}
				res.TagsRemoved++
			}
		}
		n, err := addTags(ctx, tx, id, tags)
		if err != nil {
			return res, err
		}
		res.Tags += n
		for _, s := range m.DismissedSuggestions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tag_suggestion (model_id, name, source, state)
				VALUES (?, ?, 'folder', 'dismissed') ON CONFLICT (model_id, name) DO UPDATE SET state = 'dismissed'`,
				id, s); err != nil {
				return res, err
			}
		}
		if m.Thumbnail != nil && thumbSource != "manual" {
			thumbJobs = append(thumbJobs, thumbJob{id, localUID, m.Thumbnail})
		}
	}

	for _, ov := range e.Overrides {
		rid := rootID[ov.Root]
		if rid == 0 {
			res.OverridesSkipped++
			continue
		}
		var modelID any
		if ov.ModelUID != "" {
			id, ok := localOf[ov.ModelUID]
			if !ok {
				res.OverridesSkipped++
				continue
			}
			modelID = id
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM override WHERE root_id = ? AND path = ? AND kind IN (`+
			conflicting(ov.Kind)+`)`, rid, ov.Path).Scan(&n); err != nil {
			return res, err
		}
		if n > 0 {
			res.OverridesSkipped++ // the same or a local decision on this folder
			continue
		}
		var data any
		if len(ov.Data) > 0 {
			data = string(ov.Data)
		}
		created := ov.CreatedAt
		if created == 0 {
			created = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO override (kind, root_id, path, model_id, data, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, ov.Kind, rid, ov.Path, modelID, data, created); err != nil {
			return res, err
		}
		res.Overrides++
		if !added[rid] {
			changed[rid] = true
		}
	}

	if err := importExtras(ctx, tx, e, localOf, now, &res); err != nil {
		return res, err
	}
	if o.Base != nil {
		if err := syncRemovals(ctx, tx, e, o.Base, localOf, &res); err != nil {
			return res, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE NOT EXISTS
			(SELECT 1 FROM model_tag mt WHERE mt.tag_id = tag.id)`); err != nil {
			return res, err
		}
	}
	if err := refreshSearch(ctx, tx, touched); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}

	// Thumbnails after the commit: a failed import leaves no files behind.
	for _, j := range thumbJobs {
		ok, err := writeThumb(ctx, db, o, j.modelID, j.uid, j.t)
		if err != nil {
			return res, err
		}
		if ok {
			res.Thumbnails++
		}
	}
	for id := range changed {
		res.RootsChanged = append(res.RootsChanged, id)
	}
	sort.Slice(res.RootsChanged, func(i, j int) bool { return res.RootsChanged[i] < res.RootsChanged[j] })
	return res, nil
}

// conflicting lists the override kinds that decide the same question as kind
// (as in package corrections), quoted for an IN list.
func conflicting(kind string) string {
	switch kind {
	case "is_model", "not_model":
		return `'is_model', 'not_model'`
	default:
		return `'merge', 'split'`
	}
}

func creatorID(ctx context.Context, tx *sql.Tx, name string) (any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO creator (name) VALUES (?)
		ON CONFLICT (name) DO UPDATE SET name = name RETURNING id`, name).Scan(&id)
	return id, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func createModel(ctx context.Context, tx *sql.Tx, m *Model, now int64) (int64, error) {
	cid, err := creatorID(ctx, tx, m.Creator)
	if err != nil {
		return 0, err
	}
	created, updated := m.CreatedAt, m.UpdatedAt
	if created == 0 {
		created = now
	}
	if updated == 0 {
		updated = now
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO model (uid, name, creator_id, notes, origin, origin_url, license,
		print_status, supports, supports_manual, name_manual, creator_manual, favorite, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		m.UID, m.Name, cid, m.Notes, nullable(m.Origin), nullable(m.OriginURL), nullable(m.License),
		m.PrintStatus, nullable(m.Supports), m.SupportsManual, m.NameManual, m.CreatorManual, m.Favorite,
		created, updated).Scan(&id)
	return id, err
}

// mergeModel takes over the exported user data where the local model has
// none. Returns whether differing notes were both kept.
func mergeModel(ctx context.Context, tx *sql.Tx, id int64, m *Model, now int64) (bool, error) {
	var nameManual, creatorManual, supportsManual, favorite bool
	var notes, printStatus, origin, originURL, license string
	if err := tx.QueryRowContext(ctx, `SELECT name_manual, creator_manual, supports_manual, favorite, notes, print_status,
		coalesce(origin, ''), coalesce(origin_url, ''), coalesce(license, '') FROM model WHERE id = ?`, id).
		Scan(&nameManual, &creatorManual, &supportsManual, &favorite, &notes, &printStatus, &origin, &originURL, &license); err != nil {
		return false, err
	}
	exec := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	if m.NameManual && !nameManual {
		if err := exec(`UPDATE model SET name = ?, name_manual = 1 WHERE id = ?`, m.Name, id); err != nil {
			return false, err
		}
	}
	if m.CreatorManual && !creatorManual {
		cid, err := creatorID(ctx, tx, m.Creator)
		if err != nil {
			return false, err
		}
		if err := exec(`UPDATE model SET creator_id = ?, creator_manual = 1 WHERE id = ?`, cid, id); err != nil {
			return false, err
		}
	}
	merged, kept := mergeNotes(notes, m.Notes)
	if merged != notes {
		if err := exec(`UPDATE model SET notes = ? WHERE id = ?`, merged, id); err != nil {
			return false, err
		}
	}
	if m.Favorite && !favorite {
		if err := exec(`UPDATE model SET favorite = 1 WHERE id = ?`, id); err != nil {
			return false, err
		}
	}
	if m.PrintStatus != "not_printed" && printStatus == "not_printed" {
		if err := exec(`UPDATE model SET print_status = ? WHERE id = ?`, m.PrintStatus, id); err != nil {
			return false, err
		}
	}
	if m.SupportsManual && !supportsManual {
		if err := exec(`UPDATE model SET supports = ?, supports_manual = 1 WHERE id = ?`, nullable(m.Supports), id); err != nil {
			return false, err
		}
	}
	for _, f := range []struct{ col, local, imported string }{
		{"origin", origin, m.Origin}, {"origin_url", originURL, m.OriginURL}, {"license", license, m.License},
	} {
		if f.local == "" && f.imported != "" {
			if err := exec(`UPDATE model SET `+f.col+` = ? WHERE id = ?`, f.imported, id); err != nil {
				return false, err
			}
		}
	}
	return kept, exec(`UPDATE model SET updated_at = ? WHERE id = ?`, now, id)
}

// addTags adds tags to a model; returns the number of new assignments.
func addTags(ctx context.Context, tx *sql.Tx, id int64, names []string) (int, error) {
	n := 0
	for _, raw := range names {
		name := strings.Join(strings.Fields(raw), " ")
		if name == "" {
			continue
		}
		var tagID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO tag (name) VALUES (?)
			ON CONFLICT (name) DO UPDATE SET name = name RETURNING id`, name).Scan(&tagID); err != nil {
			return n, err
		}
		r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_tag (model_id, tag_id) VALUES (?, ?)`, id, tagID)
		if err != nil {
			return n, err
		}
		if k, _ := r.RowsAffected(); k > 0 {
			n++
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tag_suggestion SET state = 'dismissed' WHERE model_id = ? AND name = ?`,
			id, name); err != nil {
			return n, err
		}
	}
	return n, nil
}

func refreshSearch(ctx context.Context, tx *sql.Tx, ids map[int64]bool) error {
	list := make([]int64, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	for start := 0; start < len(list); start += 500 {
		part := list[start:min(start+500, len(list))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		q := `SELECT id FROM model WHERE id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(part)), ",") + `)`
		if err := detect.RefreshSearchFor(ctx, tx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// maxThumbBytes bounds an imported thumbnail.
const maxThumbBytes = 16 << 20

// writeThumb stores an imported manual thumbnail as the model's thumbnail
// (named after the local uid, like the thumbnails package does). Unusable
// data is skipped, not an error: the model keeps its other data.
func writeThumb(ctx context.Context, db *sql.DB, o ApplyOptions, modelID int64, uid string, t *Thumbnail) (bool, error) {
	if o.ThumbDir == "" {
		return false, nil
	}
	data := t.Data
	if len(data) == 0 && o.SourceDir != "" && t.File != "" && filepath.Base(t.File) == t.File {
		b, err := os.ReadFile(filepath.Join(o.SourceDir, "thumbnails", t.File))
		if err != nil {
			return false, nil
		}
		data = b
	}
	if len(data) == 0 || len(data) > maxThumbBytes {
		return false, nil
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
		return false, nil
	}
	if err := os.MkdirAll(o.ThumbDir, 0o755); err != nil {
		return false, err
	}
	name := uid + ".jpg"
	tmp, err := os.CreateTemp(o.ThumbDir, ".thumb-*.jpg")
	if err != nil {
		return false, err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), filepath.Join(o.ThumbDir, name))
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return false, werr
	}
	now := time.Now().UnixMilli()
	_, err = db.ExecContext(ctx, `UPDATE model SET thumb_source = 'manual', thumb_file = ?, thumb_tried_at = ?, updated_at = ?
		WHERE id = ?`, name, now, now, modelID)
	return err == nil, err
}

// importExtras adds print log entries, collections and saved searches. Like
// the rest of the import it only adds: an identical print is not added twice,
// collection members are appended in the exported order, a saved search of
// the same name here stays.
func importExtras(ctx context.Context, tx *sql.Tx, e *Export, localOf map[string]int64, now int64, res *Result) error {
	for _, m := range e.Models {
		id, ok := localOf[m.UID]
		if !ok {
			continue
		}
		for _, p := range m.Prints {
			r, err := tx.ExecContext(ctx, `INSERT INTO print_log (model_id, printed_at, result, printer, settings, notes, created_at)
				SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7 WHERE NOT EXISTS (SELECT 1 FROM print_log
					WHERE model_id = ?1 AND printed_at = ?2 AND result = ?3 AND printer = ?4 AND settings = ?5 AND notes = ?6)`,
				id, p.PrintedAt, p.Result, p.Printer, p.Settings, p.Notes, now)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n > 0 {
				res.Prints++
			}
		}
	}
	for _, c := range e.Collections {
		name := strings.Join(strings.Fields(c.Name), " ")
		if name == "" {
			continue
		}
		var cid int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO collection (name, created_at) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET name = collection.name RETURNING id`, name, now).Scan(&cid); err != nil {
			return err
		}
		for _, uid := range c.Models {
			id, ok := localOf[uid]
			if !ok {
				continue
			}
			r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO collection_model (collection_id, model_id, position)
				VALUES (?1, ?2, (SELECT coalesce(max(position), 0) + 1 FROM collection_model WHERE collection_id = ?1))`, cid, id)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n > 0 {
				res.Collections++
			}
		}
	}
	for _, ss := range e.SavedSearches {
		name := strings.Join(strings.Fields(ss.Name), " ")
		if name == "" || !json.Valid(ss.Query) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO saved_search (name, query, created_at) VALUES (?, ?, ?)`,
			name, string(ss.Query), now); err != nil {
			return err
		}
	}
	return nil
}

// minus returns the names in a that are not in b (case is ignored, as for tags).
func minus(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[strings.ToLower(x)] = true
	}
	var out []string
	for _, x := range a {
		if !in[strings.ToLower(x)] {
			out = append(out, x)
		}
	}
	return out
}

// syncModel applies what the other PC changed since base (three-way merge):
// a field it changed is taken over, one it did not change stays as it is
// here. Notes changed on both sides keep both texts. Returns whether that
// happened.
func syncModel(ctx context.Context, tx *sql.Tx, id int64, m, b *Model, now int64) (bool, error) {
	var notes string
	if err := tx.QueryRowContext(ctx, `SELECT notes FROM model WHERE id = ?`, id).Scan(&notes); err != nil {
		return false, err
	}
	exec := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	changed := 0
	if m.Name != b.Name || m.NameManual != b.NameManual {
		if m.NameManual {
			if err := exec(`UPDATE model SET name = ?, name_manual = 1 WHERE id = ?`, m.Name, id); err != nil {
				return false, err
			}
			changed++
		}
	}
	if m.CreatorManual && (m.Creator != b.Creator || !b.CreatorManual) {
		cid, err := creatorID(ctx, tx, m.Creator)
		if err != nil {
			return false, err
		}
		if err := exec(`UPDATE model SET creator_id = ?, creator_manual = 1 WHERE id = ?`, cid, id); err != nil {
			return false, err
		}
		changed++
	}
	if m.Favorite != b.Favorite {
		if err := exec(`UPDATE model SET favorite = ? WHERE id = ?`, m.Favorite, id); err != nil {
			return false, err
		}
		changed++
	}
	if m.PrintStatus != b.PrintStatus {
		if err := exec(`UPDATE model SET print_status = ? WHERE id = ?`, m.PrintStatus, id); err != nil {
			return false, err
		}
		changed++
	}
	if m.SupportsManual && (m.Supports != b.Supports || !b.SupportsManual) {
		if err := exec(`UPDATE model SET supports = ?, supports_manual = 1 WHERE id = ?`, nullable(m.Supports), id); err != nil {
			return false, err
		}
		changed++
	}
	for _, f := range []struct{ col, cur, old string }{
		{"origin", m.Origin, b.Origin}, {"origin_url", m.OriginURL, b.OriginURL}, {"license", m.License, b.License},
	} {
		if f.cur != f.old {
			if err := exec(`UPDATE model SET `+f.col+` = ? WHERE id = ?`, nullable(f.cur), id); err != nil {
				return false, err
			}
			changed++
		}
	}
	kept := false
	if m.Notes != b.Notes {
		next := m.Notes
		if strings.TrimSpace(notes) != strings.TrimSpace(b.Notes) {
			next, kept = mergeNotes(notes, m.Notes) // changed here too: keep both
		}
		if next != notes {
			if err := exec(`UPDATE model SET notes = ? WHERE id = ?`, next, id); err != nil {
				return false, err
			}
			changed++
		}
	}
	if changed > 0 {
		if err := exec(`UPDATE model SET updated_at = ? WHERE id = ?`, now, id); err != nil {
			return false, err
		}
	}
	return kept, nil
}

// syncRemovals takes models out of collections the other PC took them out
// of since base.
func syncRemovals(ctx context.Context, tx *sql.Tx, e, base *Export, localOf map[string]int64, res *Result) error {
	cur := map[string]map[string]bool{}
	for _, c := range e.Collections {
		set := map[string]bool{}
		for _, u := range c.Models {
			set[u] = true
		}
		cur[strings.ToLower(c.Name)] = set
	}
	for _, c := range base.Collections {
		now, still := cur[strings.ToLower(c.Name)]
		for _, u := range c.Models {
			if still && now[u] {
				continue
			}
			id, ok := localOf[u]
			if !ok {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM collection_model WHERE model_id = ?
				AND collection_id = (SELECT id FROM collection WHERE name = ?)`, id, c.Name); err != nil {
				return err
			}
		}
	}
	return nil
}
