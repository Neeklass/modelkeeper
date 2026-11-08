package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
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
	Roots      int      `json:"roots"`      // roots where the model has present files
	Missing    int      `json:"missing"`    // files gone since the last scan
	Suggestion int      `json:"tagSuggest"` // open tag suggestions
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

// SearchModels returns a page of models matching the text (H1), sorted by
// relevance when searching and by name otherwise.
func SearchModels(ctx context.Context, db *sql.DB, text string, offset, limit int) (ModelPage, error) {
	page := ModelPage{Items: []ModelRow{}}
	q := ftsQuery(text)
	from, order := `FROM model m`, `ORDER BY m.name COLLATE NOCASE, m.id`
	var args []any
	if q != "" {
		from = `FROM model_fts f JOIN model m ON m.id = f.rowid WHERE model_fts MATCH ?`
		order = `ORDER BY bm25(model_fts, 10, 5, 5, 1, 1), m.name COLLATE NOCASE`
		args = append(args, q)
	}
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
			(SELECT count(DISTINCT l.root_id) FROM location l WHERE l.model_id = m.id AND l.present = 1),
			(SELECT count(*) FROM location l WHERE l.model_id = m.id AND l.present = 0),
			(SELECT count(*) FROM tag_suggestion s WHERE s.model_id = m.id AND s.state = 'open')
		`+from+` `+order+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var r ModelRow
		var variants string
		if err := rows.Scan(&r.ID, &r.Name, &r.Creator, &r.Supports, &variants, &r.Files, &r.Roots,
			&r.Missing, &r.Suggestion); err != nil {
			return page, err
		}
		r.Variants = []string{}
		if variants != "" {
			r.Variants = strings.Split(variants, "\x1f")
		}
		page.Items = append(page.Items, r)
	}
	return page, rows.Err()
}

// ModelDetail is everything the detail view shows (H4).
type ModelDetail struct {
	ID           int64         `json:"id"`
	UID          string        `json:"uid"`
	Name         string        `json:"name"`
	Creator      string        `json:"creator"`
	Notes        string        `json:"notes"`
	PrintStatus  string        `json:"printStatus"`
	Supports     string        `json:"supports"`
	Anchors      []ModelAnchor `json:"anchors"`
	Variants     []string      `json:"variants"`
	Tags         []string      `json:"tags"`
	TagSuggested []string      `json:"tagSuggestions"`
	Files        []ModelFile   `json:"files"`
	// HasCorrections: manual corrections exist at or below the model's folders.
	HasCorrections bool `json:"hasCorrections"`
}

// ModelAnchor is a place where the model was detected.
type ModelAnchor struct {
	RootID   int64  `json:"rootId"`
	RootPath string `json:"rootPath"`
	RootKind string `json:"rootKind"`
	// CreatorLevel: the root's first folder level is the creator (limits corrections).
	CreatorLevel bool   `json:"creatorLevel"`
	Online       bool   `json:"online"`
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
	Copies      int    `json:"copies"` // present locations with the same content (D4)
	Status      string `json:"archiveStatus"`
}

// maxDetailFiles bounds the file list of one model.
const maxDetailFiles = 2000

// GetModel returns the details of a model.
func GetModel(ctx context.Context, db *sql.DB, id int64) (ModelDetail, error) {
	var d ModelDetail
	var supports sql.NullString
	err := db.QueryRowContext(ctx, `SELECT m.id, m.uid, m.name, coalesce(c.name, ''), m.notes, m.print_status, m.supports
		FROM model m LEFT JOIN creator c ON c.id = m.creator_id WHERE m.id = ?`, id).
		Scan(&d.ID, &d.UID, &d.Name, &d.Creator, &d.Notes, &d.PrintStatus, &supports)
	if err != nil {
		return d, err
	}
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

	rows, err := db.QueryContext(ctx, `SELECT a.root_id, r.path, r.kind, r.creator_level, r.status <> 'offline', a.path, a.kind
		FROM model_anchor a JOIN root r ON r.id = a.root_id WHERE a.model_id = ? ORDER BY r.path, a.path`, id)
	if err != nil {
		return d, err
	}
	d.Anchors = []ModelAnchor{}
	for rows.Next() {
		var a ModelAnchor
		if err := rows.Scan(&a.RootID, &a.RootPath, &a.RootKind, &a.CreatorLevel, &a.Online, &a.Path, &a.Kind); err != nil {
			rows.Close()
			return d, err
		}
		d.Anchors = append(d.Anchors, a)
	}
	rows.Close()

	rows, err = db.QueryContext(ctx, `SELECT l.id, l.root_id, l.path, coalesce(a.path, ''), l.category, l.ext, l.size,
			l.present, coalesce(v.label, ''), f.sha256,
			coalesce((SELECT count(*) FROM location o WHERE o.file_id = l.file_id AND o.present = 1), 0),
			coalesce(l.archive_status, '')
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
			&f.Present, &f.Variant, &sha, &f.Copies, &f.Status); err != nil {
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
