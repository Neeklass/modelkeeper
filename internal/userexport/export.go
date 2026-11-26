// Package userexport writes everything the user entered in the catalog into
// one readable JSON file and applies such a file to a catalog again (J2,
// TODO 2.3). It is the plan for "everything is gone": the file needs neither
// the old catalog.db nor its snapshots, and it is independent of the SQLite
// schema.
//
// The file holds user data and configuration only: models with anything the
// user entered (tags, notes, favorite, print status, edited fields, manual
// thumbnails), corrections of the detection, root folders and the detection
// settings. Everything a scan finds again (locations, hashes of every file,
// generated thumbnails) is left out.
//
// Every model carries three ways to find it again: its stable ID (uid), its
// anchors (root folder + relative path) and the SHA-256 of its content files.
// So the data finds its files again after a move to another NAS, under
// another drive letter, or after a fresh scan on another PC.
package userexport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"modelkeeper/internal/detect"
)

// Format identifies an export file; FormatVersion is raised on incompatible
// changes. Read accepts every version up to FormatVersion.
const (
	Format        = "modelkeeper-export"
	FormatVersion = 1
)

// FileName is the name of the export inside a snapshot folder.
const FileName = "catalog-export.json"

// Export is the content of an export file.
type Export struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"formatVersion"`
	AppVersion    string `json:"appVersion,omitempty"`
	Schema        int    `json:"schema"`               // catalog schema it was written from (information only)
	ExportedAt    string `json:"exportedAt,omitempty"` // RFC 3339
	// Settings: the settings.json of the exporting app, as it was.
	Settings  json.RawMessage `json:"settings,omitempty"`
	Roots     []Root          `json:"roots"`
	Tags      []string        `json:"tags"` // all tags with at least one model
	Models    []Model         `json:"models"`
	Overrides []Override      `json:"overrides"`
	// Collections in their order (G6) and saved searches.
	Collections   []Collection  `json:"collections,omitempty"`
	SavedSearches []SavedSearch `json:"savedSearches,omitempty"`
}

// Collection is a collection with its models (uids) in order.
type Collection struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

// SavedSearch is a named search (the frontend's query as JSON).
type SavedSearch struct {
	Name  string          `json:"name"`
	Query json.RawMessage `json:"query"`
}

// Print is an entry of a model's print log (L1).
type Print struct {
	PrintedAt int64  `json:"printedAt"`
	Result    string `json:"result"`
	Printer   string `json:"printer,omitempty"`
	Settings  string `json:"settings,omitempty"`
	Notes     string `json:"notes,omitempty"`
	// Photos of the result (JPEG; base64 in the JSON).
	Photos [][]byte `json:"photos,omitempty"`
}

// Root is a root folder of the exporting catalog.
type Root struct {
	Key          string `json:"key"` // referenced by anchors and overrides
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	CreatorLevel bool   `json:"creatorLevel"`
	Removed      bool   `json:"removed,omitempty"` // deactivated (E9)
}

// Model is a model with user data.
type Model struct {
	UID            string   `json:"uid"`
	Name           string   `json:"name"`
	NameManual     bool     `json:"nameManual,omitempty"`
	Creator        string   `json:"creator,omitempty"`
	CreatorManual  bool     `json:"creatorManual,omitempty"`
	Notes          string   `json:"notes,omitempty"`
	Favorite       bool     `json:"favorite,omitempty"`
	PrintStatus    string   `json:"printStatus"`
	Supports       string   `json:"supports,omitempty"`
	SupportsManual bool     `json:"supportsManual,omitempty"`
	Origin         string   `json:"origin,omitempty"`
	OriginURL      string   `json:"originUrl,omitempty"`
	License        string   `json:"license,omitempty"`
	Tags           []string `json:"tags"`
	// DismissedSuggestions are tag suggestions the user rejected.
	DismissedSuggestions []string   `json:"dismissedSuggestions,omitempty"`
	Anchors              []Anchor   `json:"anchors"`
	Content              []string   `json:"content"` // SHA-256 (hex) of the content files
	Thumbnail            *Thumbnail `json:"thumbnail,omitempty"`
	Prints               []Print    `json:"prints,omitempty"`
	CreatedAt            int64      `json:"createdAt"` // unix ms
	UpdatedAt            int64      `json:"updatedAt"`
}

// Anchor is a place where the model was detected.
type Anchor struct {
	Root   string `json:"root"` // Root.Key
	Path   string `json:"path"` // relative, '/'-separated
	Kind   string `json:"kind"` // folder | archive | file
	Origin string `json:"origin,omitempty"`
}

// Thumbnail is a manually set thumbnail. Data holds the JPEG (base64 in the
// file); without Data, File is looked up in a "thumbnails" folder next to the
// export (inside a snapshot, where the file lies anyway).
type Thumbnail struct {
	File string `json:"file"`
	Data []byte `json:"data,omitempty"`
}

// Override is a manual correction of the detection (F4d).
type Override struct {
	Kind      string          `json:"kind"`
	Root      string          `json:"root"`
	Path      string          `json:"path"`
	ModelUID  string          `json:"modelUid,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	CreatedAt int64           `json:"createdAt"`
}

// Options control an export.
type Options struct {
	AppVersion string
	// SettingsFile is copied into the export if it exists.
	SettingsFile string
	// ThumbDir is the thumbnail folder; EmbedThumbnails puts the manual
	// thumbnails into the file (a file to be used on its own), otherwise only
	// their names are written (inside a snapshot, next to its thumbnails).
	ThumbDir        string
	EmbedThumbnails bool
	Now             time.Time
}

// Summary describes a written export.
type Summary struct {
	Models     int `json:"models"`
	Tags       int `json:"tags"`
	Overrides  int `json:"overrides"`
	Thumbnails int `json:"thumbnails"`
}

func (e *Export) summary() Summary {
	s := Summary{Models: len(e.Models), Tags: len(e.Tags), Overrides: len(e.Overrides)}
	for _, m := range e.Models {
		if m.Thumbnail != nil {
			s.Thumbnails++
		}
	}
	return s
}

// userDataCond selects models with user data, or ones a correction points at.
const userDataCond = detect.UserDataCond

// contentCats are the file categories that make a model (as in catalog).
const contentCats = `('model', 'slicer', 'archive')`

// Build reads the export from a catalog. It only reads, so it also works on
// a read-only copy (a snapshot).
func Build(ctx context.Context, db *sql.DB, o Options) (*Export, error) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	e := &Export{Format: Format, FormatVersion: FormatVersion, AppVersion: o.AppVersion,
		ExportedAt: now.Format(time.RFC3339), Roots: []Root{}, Tags: []string{}, Models: []Model{}, Overrides: []Override{}}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&e.Schema); err != nil {
		return nil, err
	}
	if o.SettingsFile != "" {
		b, err := os.ReadFile(o.SettingsFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read settings: %w", err)
		}
		if len(b) > 0 && json.Valid(b) {
			var buf bytes.Buffer
			if json.Compact(&buf, b) == nil {
				e.Settings = json.RawMessage(buf.Bytes())
			}
		}
	}

	// Roots.
	rootKey := map[int64]string{}
	rows, err := db.QueryContext(ctx, `SELECT id, path, kind, creator_level, removed_at IS NOT NULL FROM root ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var r Root
		if err := rows.Scan(&id, &r.Path, &r.Kind, &r.CreatorLevel, &r.Removed); err != nil {
			rows.Close()
			return nil, err
		}
		r.Key = fmt.Sprintf("r%d", id)
		rootKey[id] = r.Key
		e.Roots = append(e.Roots, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Models with user data, plus the ones corrections point at.
	rows, err = db.QueryContext(ctx, `SELECT m.id, m.uid, m.name, m.name_manual, coalesce(c.name, ''), m.creator_manual,
		m.notes, m.favorite, m.print_status, coalesce(m.supports, ''), m.supports_manual,
		coalesce(m.origin, ''), coalesce(m.origin_url, ''), coalesce(m.license, ''),
		coalesce(m.thumb_source, ''), coalesce(m.thumb_file, ''), m.created_at, m.updated_at
		FROM model m LEFT JOIN creator c ON c.id = m.creator_id
		WHERE `+userDataCond+` OR EXISTS (SELECT 1 FROM override o WHERE o.model_id = m.id)
		ORDER BY m.uid`)
	if err != nil {
		return nil, err
	}
	type pending struct {
		id    int64
		thumb string
	}
	var ids []pending
	for rows.Next() {
		var m Model
		var p pending
		var thumbSource string
		if err := rows.Scan(&p.id, &m.UID, &m.Name, &m.NameManual, &m.Creator, &m.CreatorManual,
			&m.Notes, &m.Favorite, &m.PrintStatus, &m.Supports, &m.SupportsManual,
			&m.Origin, &m.OriginURL, &m.License, &thumbSource, &p.thumb, &m.CreatedAt, &m.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if thumbSource != "manual" {
			p.thumb = ""
		}
		e.Models = append(e.Models, m)
		ids = append(ids, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	uidOf := map[int64]string{}
	index := map[int64]int{} // model id -> position in e.Models
	for i, p := range ids {
		uidOf[p.id] = e.Models[i].UID
		index[p.id] = i
		e.Models[i].Tags, e.Models[i].Anchors, e.Models[i].Content = []string{}, []Anchor{}, []string{}
	}
	// The details of all exported models in a few queries (not per model:
	// the export runs with every snapshot, also at 100,000 models).
	selected := `SELECT m.id FROM model m WHERE ` + userDataCond + ` OR EXISTS (SELECT 1 FROM override o WHERE o.model_id = m.id)`
	each := func(q string, fn func(i int, rows *sql.Rows) error) error {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := fn(-1, rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	pair := func(q string, add func(m *Model, v string)) error {
		return each(q, func(_ int, rows *sql.Rows) error {
			var id int64
			var v string
			if err := rows.Scan(&id, &v); err != nil {
				return err
			}
			if i, ok := index[id]; ok {
				add(&e.Models[i], v)
			}
			return nil
		})
	}
	if err := pair(`SELECT mt.model_id, t.name FROM model_tag mt JOIN tag t ON t.id = mt.tag_id
		WHERE mt.model_id IN (`+selected+`) ORDER BY mt.model_id, t.name COLLATE NOCASE, t.name`,
		func(m *Model, v string) { m.Tags = append(m.Tags, v) }); err != nil {
		return nil, err
	}
	if err := pair(`SELECT model_id, name FROM tag_suggestion WHERE state = 'dismissed'
		AND model_id IN (`+selected+`) ORDER BY model_id, name COLLATE NOCASE, name`,
		func(m *Model, v string) { m.DismissedSuggestions = append(m.DismissedSuggestions, v) }); err != nil {
		return nil, err
	}
	if err := each(`SELECT model_id, root_id, path, kind, origin FROM model_anchor
		WHERE model_id IN (`+selected+`) ORDER BY model_id, root_id, path`, func(_ int, rows *sql.Rows) error {
		var id, rootID int64
		var a Anchor
		if err := rows.Scan(&id, &rootID, &a.Path, &a.Kind, &a.Origin); err != nil {
			return err
		}
		a.Root = rootKey[rootID]
		if a.Origin == "auto" {
			a.Origin = ""
		}
		if i, ok := index[id]; ok {
			e.Models[i].Anchors = append(e.Models[i].Anchors, a)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	// Content: hashed content files (present or missing) and the fingerprints
	// of models that lost their last location (E9).
	if err := each(`SELECT DISTINCT model_id, sha FROM (
			SELECT l.model_id, f.sha256 AS sha FROM location l JOIN file f ON f.id = l.file_id
			WHERE l.category IN `+contentCats+` AND l.model_id IN (`+selected+`)
			UNION SELECT model_id, sha256 FROM model_fingerprint WHERE model_id IN (`+selected+`))
		ORDER BY model_id, sha`, func(_ int, rows *sql.Rows) error {
		var id int64
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return err
		}
		if i, ok := index[id]; ok {
			e.Models[i].Content = append(e.Models[i].Content, hex.EncodeToString(b))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	type printRef struct{ model, print int }
	printOf := map[int64]printRef{}
	if err := each(`SELECT id, model_id, printed_at, result, printer, settings, notes FROM print_log
		WHERE model_id IN (`+selected+`) ORDER BY model_id, printed_at, id`, func(_ int, rows *sql.Rows) error {
		var pid, id int64
		var p Print
		if err := rows.Scan(&pid, &id, &p.PrintedAt, &p.Result, &p.Printer, &p.Settings, &p.Notes); err != nil {
			return err
		}
		if i, ok := index[id]; ok {
			e.Models[i].Prints = append(e.Models[i].Prints, p)
			printOf[pid] = printRef{i, len(e.Models[i].Prints) - 1}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(printOf) > 0 {
		if err := each(`SELECT print_id, data FROM print_photo WHERE print_id IN
			(SELECT id FROM print_log WHERE model_id IN (`+selected+`)) ORDER BY print_id, id`, func(_ int, rows *sql.Rows) error {
			var pid int64
			var b []byte
			if err := rows.Scan(&pid, &b); err != nil {
				return err
			}
			if r, ok := printOf[pid]; ok {
				pr := &e.Models[r.model].Prints[r.print]
				pr.Photos = append(pr.Photos, b)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	colIndex := map[int64]int{}
	if err := each(`SELECT id, name FROM collection ORDER BY name COLLATE NOCASE, id`, func(_ int, rows *sql.Rows) error {
		var id int64
		var c Collection
		if err := rows.Scan(&id, &c.Name); err != nil {
			return err
		}
		c.Models = []string{}
		colIndex[id] = len(e.Collections)
		e.Collections = append(e.Collections, c)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT cm.collection_id, m.uid FROM collection_model cm JOIN model m ON m.id = cm.model_id
		ORDER BY cm.collection_id, cm.position, m.id`, func(_ int, rows *sql.Rows) error {
		var id int64
		var uid string
		if err := rows.Scan(&id, &uid); err != nil {
			return err
		}
		if i, ok := colIndex[id]; ok {
			e.Collections[i].Models = append(e.Collections[i].Models, uid)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT name, query FROM saved_search ORDER BY name COLLATE NOCASE, id`, func(_ int, rows *sql.Rows) error {
		var ss SavedSearch
		var q string
		if err := rows.Scan(&ss.Name, &q); err != nil {
			return err
		}
		if json.Valid([]byte(q)) {
			ss.Query = json.RawMessage(q)
			e.SavedSearches = append(e.SavedSearches, ss)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for i, p := range ids {
		m := &e.Models[i]
		sort.Strings(m.Content)
		if p.thumb != "" && filepath.Base(p.thumb) == p.thumb {
			th := &Thumbnail{File: p.thumb}
			if o.EmbedThumbnails {
				if o.ThumbDir == "" {
					th = nil
				} else if b, err := os.ReadFile(filepath.Join(o.ThumbDir, p.thumb)); err == nil {
					th.Data = b
				} else {
					th = nil // the catalog references a thumbnail that is gone
				}
			}
			m.Thumbnail = th
		}
	}

	if e.Tags, err = strings1(ctx, db, `SELECT name FROM tag t WHERE EXISTS
		(SELECT 1 FROM model_tag mt WHERE mt.tag_id = t.id) ORDER BY name COLLATE NOCASE, name`); err != nil {
		return nil, err
	}

	// Corrections.
	rows, err = db.QueryContext(ctx, `SELECT kind, root_id, path, model_id, coalesce(data, ''), created_at
		FROM override ORDER BY root_id, path, kind, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ov Override
		var rootID int64
		var modelID sql.NullInt64
		var data string
		if err := rows.Scan(&ov.Kind, &rootID, &ov.Path, &modelID, &data, &ov.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		ov.Root = rootKey[rootID]
		if modelID.Valid {
			ov.ModelUID = uidOf[modelID.Int64]
			if ov.ModelUID == "" {
				continue // points at a model that no longer exists
			}
		}
		if data != "" && json.Valid([]byte(data)) {
			ov.Data = json.RawMessage(data)
		}
		e.Overrides = append(e.Overrides, ov)
	}
	rows.Close()
	return e, rows.Err()
}

func strings1(ctx context.Context, db *sql.DB, q string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, q, args...)
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

// Write encodes the export: indented, keys in a fixed order, models sorted by
// uid, so two exports of the same catalog differ only where the data does.
func (e *Export) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(e)
}

// WriteFile builds the export of db and writes it to file atomically
// (temporary file, then rename), so an interrupted write never replaces a
// good export with a broken one.
func WriteFile(ctx context.Context, db *sql.DB, file string, o Options) (Summary, error) {
	e, err := Build(ctx, db, o)
	if err != nil {
		return Summary{}, err
	}
	return e.summary(), e.writeFile(file)
}

func (e *Export) writeFile(file string) error {
	dir := filepath.Dir(file)
	tmp, err := os.CreateTemp(dir, ".export-*.json")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := e.Write(tmp); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return err
	}
	ok = true
	return nil
}

// ErrNotAnExport: the file is not a modelkeeper export.
var ErrNotAnExport = errors.New("not a modelkeeper export")

// ErrTooNew: the export was written by a newer app version.
var ErrTooNew = errors.New("export from a newer modelkeeper")

// maxExportBytes bounds what Read loads (a catalog of 200,000 models with
// user data and some embedded thumbnails stays far below).
const maxExportBytes = 2 << 30

// Read parses and checks an export file. Nothing is applied.
func Read(file string) (*Export, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Decode(io.LimitReader(f, maxExportBytes))
}

// Decode parses and checks an export.
func Decode(r io.Reader) (*Export, error) {
	var e Export
	dec := json.NewDecoder(r)
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAnExport, err)
	}
	if e.Format != Format {
		return nil, ErrNotAnExport
	}
	if e.FormatVersion < 1 {
		return nil, ErrNotAnExport
	}
	if e.FormatVersion > FormatVersion {
		return nil, ErrTooNew
	}
	if err := e.check(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAnExport, err)
	}
	return &e, nil
}

// check validates references and values, so Apply can rely on them.
func (e *Export) check() error {
	keys := map[string]bool{}
	for _, r := range e.Roots {
		if r.Key == "" || keys[r.Key] {
			return fmt.Errorf("root key %q missing or used twice", r.Key)
		}
		keys[r.Key] = true
	}
	uids := map[string]bool{}
	for i := range e.Models {
		m := &e.Models[i]
		if strings.TrimSpace(m.UID) == "" || uids[m.UID] {
			return fmt.Errorf("model uid %q missing or used twice", m.UID)
		}
		uids[m.UID] = true
		if strings.TrimSpace(m.Name) == "" {
			m.Name = "?"
		}
		switch m.PrintStatus {
		case "not_printed", "printed", "failed":
		case "":
			m.PrintStatus = "not_printed"
		default:
			return fmt.Errorf("model %s: print status %q", m.UID, m.PrintStatus)
		}
		switch m.Supports {
		case "", "presupported", "unsupported", "both":
		default:
			return fmt.Errorf("model %s: supports %q", m.UID, m.Supports)
		}
		for _, a := range m.Anchors {
			if !keys[a.Root] {
				return fmt.Errorf("model %s: unknown root %q", m.UID, a.Root)
			}
			switch a.Kind {
			case "folder", "archive", "file":
			default:
				return fmt.Errorf("model %s: anchor kind %q", m.UID, a.Kind)
			}
		}
		for _, c := range m.Content {
			if b, err := hex.DecodeString(c); err != nil || len(b) != 32 {
				return fmt.Errorf("model %s: content hash %q", m.UID, c)
			}
		}
		for _, p := range m.Prints {
			if p.Result != "ok" && p.Result != "failed" {
				return fmt.Errorf("model %s: print result %q", m.UID, p.Result)
			}
		}
	}
	for _, o := range e.Overrides {
		if !keys[o.Root] {
			return fmt.Errorf("override: unknown root %q", o.Root)
		}
		switch o.Kind {
		case "is_model", "not_model", "merge", "split":
		default:
			return fmt.Errorf("override kind %q", o.Kind)
		}
		if o.ModelUID != "" && !uids[o.ModelUID] {
			return fmt.Errorf("override: unknown model %q", o.ModelUID)
		}
	}
	return nil
}
