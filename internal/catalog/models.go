package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// ModelRow is one line of the model list.
type ModelRow struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Creator    string   `json:"creator"`
	Supports   string   `json:"supports"`
	Variants   []string `json:"variants"`
	Files      int      `json:"files"`      // content files (models, slicer files, archives) present
	Roots      int      `json:"roots"`      // active roots where the model has present files
	Missing    int      `json:"missing"`    // files on active roots gone since the last scan
	Suggestion int      `json:"tagSuggest"` // open tag suggestions
	Tags       []string `json:"tags"`
	Thumb      string   `json:"thumb"` // URL of the thumbnail, "" if none
	Favorite   bool     `json:"favorite"`
	// Inactive: the model's files are only on deactivated roots (E9).
	Inactive bool `json:"inactive"`
}

// ThumbURL is the address under which the app serves a thumbnail file; the
// version parameter changes whenever the model (and so its thumbnail) changes.
func ThumbURL(file string, version int64) string {
	if file == "" {
		return ""
	}
	return fmt.Sprintf("/thumbs/%s?v=%d", file, version)
}

// ModelQuery selects models for the list (H1, H2). Empty fields do not filter.
type ModelQuery struct {
	Text      string `json:"text"`      // full-text search (H1)
	Untagged  bool   `json:"untagged"`  // only models without tags (G4)
	Favorites bool   `json:"favorites"` // only favorites

	TagID       int64  `json:"tagId"`       // has this tag
	CreatorID   int64  `json:"creatorId"`   // by this creator
	RootID      int64  `json:"rootId"`      // has files in this root folder (location)
	Ext         string `json:"ext"`         // has files of this type, e.g. "stl"
	Supports    string `json:"supports"`    // presupported | unsupported | both | none
	PrintStatus string `json:"printStatus"` // not_printed | printed | failed

	Duplicates          bool `json:"duplicates"`          // a file exists more than once (D4)
	SingleLocation      bool `json:"singleLocation"`      // all files only in one root folder, no backup (D5)
	Offline             bool `json:"offline"`             // found in a root that is offline (E3)
	BrokenArchives      bool `json:"brokenArchives"`      // contains a damaged archive (F7)
	ExtractedAndArchive bool `json:"extractedAndArchive"` // files exist extracted and inside an archive
	Missing             bool `json:"missing"`             // files gone since the last scan
	Inactive            bool `json:"inactive"`            // files only on deactivated roots (E9)
}

// Deactivated roots (E9) in these queries: the catalog still knows what lies
// there, but nothing is checked any more. So they count for what describes a
// model (text search, tag, creator, file type, file count) - a model whose
// files are only there stays visible and is marked Inactive - but not for the
// checks: a copy there is no reliable backup (D5) and no actionable duplicate
// (D4, extracted + archive), its files are not "missing", it is not "offline",
// and its archives are not re-checked (F7).

// contentCats limits checks to files that make a model.
const contentCats = `('model', 'slicer', 'archive')`

// inactiveCond is true for a model with present files, none of them on an
// active root.
const inactiveCond = `(EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.present = 1)
	AND NOT EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.present = 1
		AND l.root_id NOT IN ` + inactiveRoots + `))`

// where builds the FROM/WHERE part and the ORDER BY for a query.
func (q ModelQuery) where() (from, order string, args []any) {
	var conds []string
	from = `FROM model m`
	order = `ORDER BY m.name COLLATE NOCASE, m.id`
	if fts := ftsQuery(q.Text); fts != "" {
		from = `FROM model_fts f JOIN model m ON m.id = f.rowid`
		conds = append(conds, `model_fts MATCH ?`)
		args = append(args, fts)
		order = `ORDER BY bm25(model_fts, 10, 5, 5, 1, 1), m.name COLLATE NOCASE`
	}
	if q.Untagged {
		conds = append(conds, `NOT EXISTS (SELECT 1 FROM model_tag mt WHERE mt.model_id = m.id)`)
	}
	if q.Favorites {
		conds = append(conds, `m.favorite = 1`)
	}
	add := func(cond string, a ...any) {
		conds = append(conds, cond)
		args = append(args, a...)
	}
	if q.TagID != 0 {
		add(`EXISTS (SELECT 1 FROM model_tag mt WHERE mt.model_id = m.id AND mt.tag_id = ?)`, q.TagID)
	}
	if q.CreatorID != 0 {
		add(`m.creator_id = ?`, q.CreatorID)
	}
	if q.RootID != 0 {
		add(`EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.root_id = ? AND l.present = 1)`, q.RootID)
	}
	if q.Ext != "" {
		add(`EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.ext = ? AND l.present = 1)`, strings.ToLower(q.Ext))
	}
	switch q.Supports {
	case "presupported", "unsupported", "both":
		add(`m.supports = ?`, q.Supports)
	case "none":
		add(`m.supports IS NULL`)
	}
	if q.PrintStatus != "" {
		add(`m.print_status = ?`, q.PrintStatus)
	}
	if q.Duplicates {
		add(`EXISTS (SELECT 1 FROM location l JOIN location o ON o.file_id = l.file_id AND o.id <> l.id AND o.present = 1
			WHERE l.model_id = m.id AND l.present = 1 AND l.category IN ` + contentCats + `
			  AND l.root_id NOT IN ` + inactiveRoots + ` AND o.root_id NOT IN ` + inactiveRoots + `)`)
	}
	if q.SingleLocation {
		add(`(SELECT count(DISTINCT o.root_id) FROM location l JOIN location o ON o.file_id = l.file_id AND o.present = 1
			WHERE l.model_id = m.id AND l.present = 1 AND l.category IN ` + contentCats + `
			  AND l.root_id NOT IN ` + inactiveRoots + ` AND o.root_id NOT IN ` + inactiveRoots + `) = 1`)
	}
	if q.Offline {
		add(`EXISTS (SELECT 1 FROM model_anchor a JOIN root r ON r.id = a.root_id
			WHERE a.model_id = m.id AND r.status = 'offline' AND r.removed_at IS NULL)`)
	}
	if q.BrokenArchives {
		add(`EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.present = 1 AND l.archive_status = 'broken'
			AND l.root_id NOT IN ` + inactiveRoots + `)`)
	}
	if q.ExtractedAndArchive {
		add(`EXISTS (SELECT 1 FROM location l JOIN location o ON o.file_id = l.file_id
			WHERE l.model_id = m.id AND l.present = 1 AND l.archive_id IS NULL AND l.category IN ('model', 'slicer')
			  AND o.archive_id IS NOT NULL AND o.present = 1
			  AND l.root_id NOT IN ` + inactiveRoots + ` AND o.root_id NOT IN ` + inactiveRoots + `)`)
	}
	if q.Missing {
		add(`EXISTS (SELECT 1 FROM location l WHERE l.model_id = m.id AND l.present = 0
			AND l.root_id NOT IN ` + inactiveRoots + `)`)
	}
	if q.Inactive {
		add(inactiveCond)
	}
	if len(conds) > 0 {
		from += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	return from, order, args
}

// ModelPage is a window of the model list.
type ModelPage struct {
	Total int        `json:"total"`
	Items []ModelRow `json:"items"`
}

// ftsQuery turns user input into an FTS5 query: every word must match as a
// prefix ("orc war" -> "orc"* "war"*). Returns "" for empty input.
func ftsQuery(text string) string {
	var parts []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		parts = append(parts, `"`+w+`"*`)
	}
	return strings.Join(parts, " ")
}

// SearchModels returns a page of models matching the query, sorted by
// relevance when searching text and by name otherwise.
func SearchModels(ctx context.Context, db *sql.DB, query ModelQuery, offset, limit int) (ModelPage, error) {
	page := ModelPage{Items: []ModelRow{}}
	from, order, args := query.where()
	if err := db.QueryRowContext(ctx, `SELECT count(*) `+from, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := db.QueryContext(ctx, `SELECT m.id, m.name,
			coalesce((SELECT name FROM creator WHERE id = m.creator_id), ''),
			coalesce(m.supports, ''),
			coalesce((SELECT group_concat(label, char(31)) FROM
				(SELECT label FROM variant WHERE model_id = m.id ORDER BY sort, label)), ''),
			(SELECT count(*) FROM location l WHERE l.model_id = m.id AND l.present = 1
				AND l.category IN ('model', 'slicer', 'archive')),
			(SELECT count(DISTINCT l.root_id) FROM location l WHERE l.model_id = m.id AND l.present = 1
				AND l.root_id NOT IN `+inactiveRoots+`),
			(SELECT count(*) FROM location l WHERE l.model_id = m.id AND l.present = 0
				AND l.root_id NOT IN `+inactiveRoots+`),
			(SELECT count(*) FROM tag_suggestion s WHERE s.model_id = m.id AND s.state = 'open'),
			coalesce((SELECT group_concat(name, char(31)) FROM (SELECT t.name FROM model_tag mt
				JOIN tag t ON t.id = mt.tag_id WHERE mt.model_id = m.id ORDER BY t.name COLLATE NOCASE)), ''),
			coalesce(m.thumb_file, ''), m.updated_at, m.favorite, `+inactiveCond+`
		`+from+` `+order+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var r ModelRow
		var variants, tags, thumb string
		var updated int64
		if err := rows.Scan(&r.ID, &r.Name, &r.Creator, &r.Supports, &variants, &r.Files, &r.Roots,
			&r.Missing, &r.Suggestion, &tags, &thumb, &updated, &r.Favorite, &r.Inactive); err != nil {
			return page, err
		}
		r.Thumb = ThumbURL(thumb, updated)
		r.Variants, r.Tags = splitList(variants), splitList(tags)
		page.Items = append(page.Items, r)
	}
	return page, rows.Err()
}

// ModelDetail is everything the detail view shows (H4).
type ModelDetail struct {
	ID          int64  `json:"id"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	Creator     string `json:"creator"`
	Notes       string `json:"notes"`
	PrintStatus string `json:"printStatus"`
	Supports    string `json:"supports"`
	// SupportsManual: the user set supports; detection no longer changes it.
	SupportsManual bool          `json:"supportsManual"`
	Favorite       bool          `json:"favorite"`
	Thumb          string        `json:"thumb"`       // URL, "" if none
	ThumbSource    string        `json:"thumbSource"` // manual | image | embedded | rendered | ""
	Anchors        []ModelAnchor `json:"anchors"`
	Variants       []string      `json:"variants"`
	Tags           []string      `json:"tags"`
	TagSuggested   []string      `json:"tagSuggestions"`
	Files          []ModelFile   `json:"files"`
	// HasCorrections: manual corrections exist at or below the model's folders.
	HasCorrections bool `json:"hasCorrections"`
	// Inactive: the model's files are only on deactivated roots (E9).
	Inactive bool `json:"inactive"`
}

// ModelAnchor is a place where the model was detected.
type ModelAnchor struct {
	RootID   int64  `json:"rootId"`
	RootPath string `json:"rootPath"`
	RootKind string `json:"rootKind"`
	// CreatorLevel: the root's first folder level is the creator (limits corrections).
	CreatorLevel bool   `json:"creatorLevel"`
	Online       bool   `json:"online"`
	RootRemoved  bool   `json:"rootRemoved"` // the root is deactivated (E9)
	Path         string `json:"path"`
	Kind         string `json:"kind"`
}

// ModelFile is one location of the model.
type ModelFile struct {
	LocationID  int64  `json:"locationId"`
	RootID      int64  `json:"rootId"`
	Path        string `json:"path"`        // relative to the root, or inside the archive
	ArchivePath string `json:"archivePath"` // "" if on disk
	Category    string `json:"category"`
	Ext         string `json:"ext"`
	Size        int64  `json:"size"`
	Present     bool   `json:"present"`
	Variant     string `json:"variant"`
	Hash        string `json:"hash"`   // first 12 hex chars of SHA-256, "" if not hashed
	Copies      int    `json:"copies"` // present locations on active roots with the same content (D4)
	Status      string `json:"archiveStatus"`
	RootRemoved bool   `json:"rootRemoved"` // on a deactivated root (E9)
}

// maxDetailFiles bounds the file list of one model.
const maxDetailFiles = 2000

// GetModel returns the details of a model.
func GetModel(ctx context.Context, db *sql.DB, id int64) (ModelDetail, error) {
	var d ModelDetail
	var supports sql.NullString
	var thumbFile string
	var updated int64
	err := db.QueryRowContext(ctx, `SELECT m.id, m.uid, m.name, coalesce(c.name, ''), m.notes, m.print_status, m.supports,
		m.supports_manual, coalesce(m.thumb_file, ''), coalesce(m.thumb_source, ''), m.updated_at, m.favorite,
		`+inactiveCond+`
		FROM model m LEFT JOIN creator c ON c.id = m.creator_id WHERE m.id = ?`, id).
		Scan(&d.ID, &d.UID, &d.Name, &d.Creator, &d.Notes, &d.PrintStatus, &supports, &d.SupportsManual,
			&thumbFile, &d.ThumbSource, &updated, &d.Favorite, &d.Inactive)
	if err != nil {
		return d, err
	}
	d.Thumb = ThumbURL(thumbFile, updated)
	d.Supports = supports.String

	strs := func(q string) ([]string, error) {
		rows, err := db.QueryContext(ctx, q, id)
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
	if d.Variants, err = strs(`SELECT label FROM variant WHERE model_id = ? ORDER BY sort, label`); err != nil {
		return d, err
	}
	if d.Tags, err = strs(`SELECT t.name FROM model_tag mt JOIN tag t ON t.id = mt.tag_id
		WHERE mt.model_id = ? ORDER BY t.name COLLATE NOCASE`); err != nil {
		return d, err
	}
	if d.TagSuggested, err = strs(`SELECT name FROM tag_suggestion WHERE model_id = ? AND state = 'open'
		ORDER BY name COLLATE NOCASE`); err != nil {
		return d, err
	}

	rows, err := db.QueryContext(ctx, `SELECT a.root_id, r.path, r.kind, r.creator_level, r.status <> 'offline',
			r.removed_at IS NOT NULL, a.path, a.kind
		FROM model_anchor a JOIN root r ON r.id = a.root_id WHERE a.model_id = ?
		ORDER BY r.removed_at IS NOT NULL, r.path, a.path`, id)
	if err != nil {
		return d, err
	}
	d.Anchors = []ModelAnchor{}
	for rows.Next() {
		var a ModelAnchor
		if err := rows.Scan(&a.RootID, &a.RootPath, &a.RootKind, &a.CreatorLevel, &a.Online, &a.RootRemoved, &a.Path, &a.Kind); err != nil {
			rows.Close()
			return d, err
		}
		d.Anchors = append(d.Anchors, a)
	}
	rows.Close()

	rows, err = db.QueryContext(ctx, `SELECT l.id, l.root_id, l.path, coalesce(a.path, ''), l.category, l.ext, l.size,
			l.present, coalesce(v.label, ''), f.sha256,
			coalesce((SELECT count(*) FROM location o WHERE o.file_id = l.file_id AND o.present = 1
				AND o.root_id NOT IN `+inactiveRoots+`), 0),
			coalesce(l.archive_status, ''), l.root_id IN `+inactiveRoots+`
		FROM location l
		LEFT JOIN location a ON a.id = l.archive_id
		LEFT JOIN variant v ON v.id = l.variant_id
		LEFT JOIN file f ON f.id = l.file_id
		WHERE l.model_id = ?
		ORDER BY l.present DESC, v.sort, coalesce(a.path, l.path), l.path
		LIMIT ?`, id, maxDetailFiles)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.Files = []ModelFile{}
	for rows.Next() {
		var f ModelFile
		var sha []byte
		if err := rows.Scan(&f.LocationID, &f.RootID, &f.Path, &f.ArchivePath, &f.Category, &f.Ext, &f.Size,
			&f.Present, &f.Variant, &sha, &f.Copies, &f.Status, &f.RootRemoved); err != nil {
			return d, err
		}
		if len(sha) > 0 {
			f.Hash = hex.EncodeToString(sha)[:12]
		}
		d.Files = append(d.Files, f)
	}
	return d, rows.Err()
}

// LocationPath returns the absolute path of a location on disk; for an
// archive member, the path of the archive (for "Show in Explorer", H5).
func LocationPath(ctx context.Context, db *sql.DB, locationID int64) (string, error) {
	var root, p string
	err := db.QueryRowContext(ctx, `SELECT r.path, coalesce(a.path, l.path)
		FROM location l JOIN root r ON r.id = l.root_id LEFT JOIN location a ON a.id = l.archive_id
		WHERE l.id = ?`, locationID).Scan(&root, &p)
	if err != nil {
		return "", err
	}
	return joinRoot(root, p), nil
}

// splitList splits a group_concat(…, char(31)) result.
func splitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\x1f")
}

// FilterOptions are the values offered in the filter dropdowns.
type FilterOptions struct {
	Creators []Option `json:"creators"`
	Exts     []Option `json:"exts"`
}

// Option is a filter value with the number of models it matches.
type Option struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Models int    `json:"models"`
}

// GetFilterOptions returns creators and file types that occur in the catalog.
func GetFilterOptions(ctx context.Context, db *sql.DB) (FilterOptions, error) {
	opts := FilterOptions{Creators: []Option{}, Exts: []Option{}}
	load := func(q string, into *[]Option) error {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o Option
			if err := rows.Scan(&o.ID, &o.Name, &o.Models); err != nil {
				return err
			}
			*into = append(*into, o)
		}
		return rows.Err()
	}
	if err := load(`SELECT c.id, c.name, count(m.id) FROM creator c JOIN model m ON m.creator_id = c.id
		GROUP BY c.id ORDER BY c.name COLLATE NOCASE`, &opts.Creators); err != nil {
		return opts, err
	}
	err := load(`SELECT 0, ext, count(DISTINCT model_id) FROM location
		WHERE model_id IS NOT NULL AND present = 1 GROUP BY ext ORDER BY ext`, &opts.Exts)
	return opts, err
}
