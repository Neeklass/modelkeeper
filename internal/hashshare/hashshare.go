// Package hashshare exports the hashes of a scanned root folder to a file and
// imports such a file into another catalog of the same folder (e.g. a second
// user of the same NAS), so the files need not be read again.
//
// Only data computed from the files is shared: path, size, modification time,
// SHA-256, and for archives the check result and the members' hashes. Tags,
// notes, favorites and print status stay with each user.
//
// An imported hash is only used by the next scan when relative path, size and
// modification time match exactly (see scanner); anything else is hashed as
// usual. A full check never uses imported hashes.
//
// File format (.mkhashes): gzip-compressed JSON lines. The first line is the
// header, every further line one file.
package hashshare

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Format identifies the file; Version changes when the format does.
const (
	Format  = "modelkeeper-hashes"
	Version = 1
)

// Header is the first line of an export.
type Header struct {
	Format  string   `json:"format"`
	Version int      `json:"version"`
	Created int64    `json:"created"` // unix ms
	Root    string   `json:"root"`    // path of the exported root (information only)
	Files   int      `json:"files"`
	Exts    []string `json:"exts"` // file types recognized by the exporter (archive members of other types are missing)
}

// Entry is one file of the root (on disk, not inside an archive).
type Entry struct {
	Path    string   `json:"p"`
	Size    int64    `json:"s"`
	Mtime   int64    `json:"m"` // unix ns
	SHA256  string   `json:"h"` // hex
	Archive *Archive `json:"a,omitempty"`
}

// Archive is the check result of an archive and its members.
type Archive struct {
	Status  string   `json:"status"` // ok | broken | encrypted | unsupported
	ErrKey  string   `json:"err,omitempty"`
	Members []Member `json:"members,omitempty"`
}

// Member is a file inside an archive.
type Member struct {
	Path   string `json:"p"`
	Size   int64  `json:"s"`
	Mtime  int64  `json:"m"`
	SHA256 string `json:"h"`
}

// ExportResult summarizes an export.
type ExportResult struct {
	Files    int `json:"files"`    // exported files (incl. archives)
	Archives int `json:"archives"` // of which archives with their members
	Unhashed int `json:"unhashed"` // present files without a hash yet (not exported)
}

// Export writes the hashes of a root. exts are the recognized file types.
func Export(ctx context.Context, db *sql.DB, rootID int64, exts []string, w io.Writer) (ExportResult, error) {
	var res ExportResult
	var rootPath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM root WHERE id = ?`, rootID).Scan(&rootPath); err != nil {
		return res, err
	}
	db.QueryRowContext(ctx, `SELECT count(*) FROM location WHERE root_id = ? AND archive_id IS NULL AND present = 1
		AND (file_id IS NULL OR archive_status = 'pending')`, rootID).Scan(&res.Unhashed)

	const final = `('ok', 'broken', 'encrypted', 'unsupported')`
	rows, err := db.QueryContext(ctx, `SELECT l.id, l.path, l.size, l.mtime, f.sha256, coalesce(l.archive_status, ''),
			coalesce(l.archive_error, ''), l.category = 'archive'
		FROM location l JOIN file f ON f.id = l.file_id
		WHERE l.root_id = ? AND l.archive_id IS NULL AND l.present = 1
		  AND (l.category <> 'archive' OR l.archive_status IN `+final+`)
		ORDER BY l.path`, rootID)
	if err != nil {
		return res, err
	}
	type row struct {
		id      int64
		e       Entry
		archive bool
	}
	var list []row
	for rows.Next() {
		var r row
		var sum []byte
		var status, errKey string
		if err := rows.Scan(&r.id, &r.e.Path, &r.e.Size, &r.e.Mtime, &sum, &status, &errKey, &r.archive); err != nil {
			rows.Close()
			return res, err
		}
		r.e.SHA256 = hex.EncodeToString(sum)
		if r.archive {
			r.e.Archive = &Archive{Status: status, ErrKey: errKey}
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	members, err := db.PrepareContext(ctx, `SELECT m.path, m.size, m.mtime, f.sha256 FROM location m
		JOIN file f ON f.id = m.file_id WHERE m.archive_id = ? ORDER BY m.path`)
	if err != nil {
		return res, err
	}
	defer members.Close()

	zw := gzip.NewWriter(w)
	enc := json.NewEncoder(zw)
	sorted := append([]string(nil), exts...)
	sort.Strings(sorted)
	if err := enc.Encode(Header{Format: Format, Version: Version, Created: time.Now().UnixMilli(),
		Root: rootPath, Files: len(list), Exts: sorted}); err != nil {
		return res, err
	}
	for _, r := range list {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if r.archive {
			mr, err := members.QueryContext(ctx, r.id)
			if err != nil {
				return res, err
			}
			for mr.Next() {
				var m Member
				var sum []byte
				if err := mr.Scan(&m.Path, &m.Size, &m.Mtime, &sum); err != nil {
					mr.Close()
					return res, err
				}
				m.SHA256 = hex.EncodeToString(sum)
				r.e.Archive.Members = append(r.e.Archive.Members, m)
			}
			mr.Close()
			res.Archives++
		}
		if err := enc.Encode(r.e); err != nil {
			return res, err
		}
		res.Files++
	}
	return res, zw.Close()
}

// ExportFile writes an export atomically (temp file + rename).
func ExportFile(ctx context.Context, db *sql.DB, rootID int64, exts []string, file string) (ExportResult, error) {
	tmp, err := os.CreateTemp(filepath.Dir(file), ".mkhashes-*")
	if err != nil {
		return ExportResult{}, err
	}
	bw := bufio.NewWriterSize(tmp, 1<<20)
	res, err := Export(ctx, db, rootID, exts, bw)
	if err == nil {
		err = bw.Flush()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), file)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return res, err
}

// --- import ---

// Errors of an import file (the frontend shows a message for each).
var (
	ErrNotAnExport = errors.New("not a modelkeeper hash file")
	ErrNewer       = errors.New("hash file from a newer modelkeeper")
	ErrInvalid     = errors.New("hash file damaged or invalid")
	ErrNoMatch     = errors.New("hash file does not match this folder")
)

// Limits against damaged or hostile files.
const (
	maxEntries    = 5_000_000
	maxMembers    = 200_000
	maxLineBytes  = 64 << 20
	sampleEntries = 60
)

// ImportResult summarizes an import.
type ImportResult struct {
	Files          int    `json:"files"`          // entries stored for the next scan
	Archives       int    `json:"archives"`       // of which archives
	ArchivesReread int    `json:"archivesReread"` // archives left out: this catalog recognizes more file types than the exporter
	Prefix         string `json:"prefix"`         // path mapping applied ("" = same folder)
	Checked        int    `json:"checked"`        // sampled files looked up on disk
	Matching       int    `json:"matching"`       // of which with the same size and mtime
}

// Read parses an export: the header, then fn for every entry.
func Read(r io.Reader, fn func(Entry) error) (Header, error) {
	var h Header
	zr, err := gzip.NewReader(r)
	if err != nil {
		return h, ErrNotAnExport
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), maxLineBytes)
	if !sc.Scan() {
		return h, ErrNotAnExport
	}
	if json.Unmarshal(sc.Bytes(), &h) != nil || h.Format != Format {
		return h, ErrNotAnExport
	}
	if h.Version > Version {
		return h, ErrNewer
	}
	n := 0
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return h, fmt.Errorf("%w: line %d", ErrInvalid, n+2)
		}
		if err := validate(&e); err != nil {
			return h, fmt.Errorf("%w: line %d: %v", ErrInvalid, n+2, err)
		}
		if n++; n > maxEntries {
			return h, fmt.Errorf("%w: too many entries", ErrInvalid)
		}
		if err := fn(e); err != nil {
			return h, err
		}
	}
	if err := sc.Err(); err != nil {
		return h, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return h, nil
}

// cleanRel accepts only clean relative '/'-paths inside the root.
func cleanRel(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return false
	}
	return path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

func validate(e *Entry) error {
	if !cleanRel(e.Path) || e.Size < 0 || !validHash(e.SHA256) {
		return errors.New("invalid file entry")
	}
	if a := e.Archive; a != nil {
		switch a.Status {
		case "ok", "broken", "encrypted", "unsupported":
		default:
			return errors.New("invalid archive status")
		}
		if len(a.Members) > maxMembers {
			return errors.New("too many archive members")
		}
		for _, m := range a.Members {
			if !cleanRel(m.Path) || m.Size < 0 || !validHash(m.SHA256) {
				return errors.New("invalid archive member")
			}
		}
	}
	return nil
}

// Import reads an export and stores its entries for the next scan of a root.
// rootPath is that root's folder: a sample of entries is looked up there to
// find the path mapping (same folder, or one level above or below) and to
// refuse files of another folder. exts are the file types this catalog
// recognizes. Earlier imports for the root are replaced.
func Import(ctx context.Context, db *sql.DB, rootID int64, rootPath string, exts []string, r io.Reader) (ImportResult, error) {
	var res ImportResult
	// Read everything first: nothing is stored from an invalid file.
	var entries []Entry
	hdr, err := Read(r, func(e Entry) error {
		entries = append(entries, e)
		return ctx.Err()
	})
	if err != nil {
		return res, err
	}
	if len(entries) == 0 {
		return res, fmt.Errorf("%w: no files", ErrNoMatch)
	}
	mapping, checked, matching := findMapping(rootPath, entries)
	res.Checked, res.Matching = checked, matching
	if mapping == nil {
		return res, ErrNoMatch
	}
	res.Prefix = mapping.describe()

	// Archives only help if the exporter cataloged every member type this
	// catalog wants; otherwise they are read here as usual.
	exporter := map[string]bool{}
	for _, x := range hdr.Exts {
		exporter[x] = true
	}
	moreTypes := false
	for _, x := range exts {
		if !exporter[x] {
			moreTypes = true
		}
	}
	want := map[string]bool{}
	for _, x := range exts {
		want[x] = true
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM hash_import WHERE root_id = ?`, rootID); err != nil {
		return res, err
	}
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO hash_import (root_id, path, size, mtime, sha256, archive)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return res, err
	}
	defer st.Close()
	for _, e := range entries {
		p, ok := mapping.apply(e.Path)
		if !ok {
			continue
		}
		var archive any
		if e.Archive != nil {
			if moreTypes {
				res.ArchivesReread++
				continue
			}
			a := *e.Archive
			a.Members = nil
			for _, m := range e.Archive.Members {
				if want[strings.ToLower(strings.TrimPrefix(path.Ext(m.Path), "."))] {
					a.Members = append(a.Members, m)
				}
			}
			b, _ := json.Marshal(a)
			archive = string(b)
			res.Archives++
		}
		sum, _ := hex.DecodeString(e.SHA256)
		if _, err := st.ExecContext(ctx, rootID, p, e.Size, e.Mtime, sum, archive); err != nil {
			return res, err
		}
		res.Files++
	}
	return res, tx.Commit()
}

// ImportFile imports an export file.
func ImportFile(ctx context.Context, db *sql.DB, rootID int64, rootPath string, exts []string, file string) (ImportResult, error) {
	f, err := os.Open(file)
	if err != nil {
		return ImportResult{}, err
	}
	defer f.Close()
	return Import(ctx, db, rootID, rootPath, exts, bufio.NewReaderSize(f, 1<<20))
}

// mapping turns an exported path into a path below this root.
type mapping struct {
	strip string // exporter's root was above: drop this leading folder
	add   string // exporter's root was below: prepend this folder
}

func (m *mapping) apply(p string) (string, bool) {
	if m.strip != "" {
		rest, ok := strings.CutPrefix(p, m.strip+"/")
		return rest, ok
	}
	if m.add != "" {
		return m.add + "/" + p, true
	}
	return p, true
}

func (m *mapping) describe() string {
	switch {
	case m.strip != "":
		return "-" + m.strip
	case m.add != "":
		return "+" + m.add
	}
	return ""
}

// findMapping looks up a sample of entries below rootPath and returns the
// mapping under which at least half of them exist with the same size and
// mtime: the same folder (common case), or the exporter's root one level
// above or below this one. nil if none fits.
func findMapping(rootPath string, entries []Entry) (*mapping, int, int) {
	sample := make([]Entry, 0, sampleEntries)
	step := max(1, len(entries)/sampleEntries)
	for i := 0; i < len(entries) && len(sample) < sampleEntries; i += step {
		sample = append(sample, entries[i])
	}
	// score checks the sample (or its first `limit` entries) under m.
	score := func(m *mapping, limit int) (hits, n int) {
		for _, e := range sample {
			if limit > 0 && n >= limit {
				break
			}
			p, ok := m.apply(e.Path)
			if !ok {
				continue
			}
			n++
			fi, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(p)))
			if err == nil && fi.Mode().IsRegular() && fi.Size() == e.Size && fi.ModTime().UnixNano() == e.Mtime {
				hits++
			}
		}
		return hits, n
	}
	same := &mapping{}
	hits, n := score(same, 0)
	if n > 0 && hits*2 >= n {
		return same, n, hits
	}
	bestHits, bestN := hits, n

	var cands []*mapping
	firsts := map[string]bool{} // exporter's root one level above: drop the first folder
	for _, e := range sample {
		if i := strings.IndexByte(e.Path, '/'); i > 0 && !firsts[e.Path[:i]] {
			firsts[e.Path[:i]] = true
			cands = append(cands, &mapping{strip: e.Path[:i]})
		}
	}
	if des, err := os.ReadDir(rootPath); err == nil { // exporter's root one level below
		for _, d := range des {
			if d.IsDir() {
				cands = append(cands, &mapping{add: d.Name()})
			}
		}
	}
	var best *mapping
	for _, c := range cands {
		if h, _ := score(c, 3); h == 0 {
			continue // cheap pre-check: over the NAS every lookup costs time
		}
		h, k := score(c, 0)
		if k > 0 && h*2 >= k && h > bestHits {
			best, bestHits, bestN = c, h, k
		}
	}
	if best == nil {
		return nil, max(bestN, len(sample)), bestHits
	}
	return best, bestN, bestHits
}
