// Package backup writes local snapshots of the catalog into data/backups
// (concept J2, M) and rotates them.
//
// A snapshot is a folder named <YYYY-MM-DD_HHMMSS>_<reason> with
//
//	catalog.db     consistent copy made with VACUUM INTO (safe while the app runs, WAL included)
//	settings.json  copy of the settings
//	thumbnails/    only the manually set thumbnails; generated ones are reproducible
//	manifest.json  app and schema version, time, reason, counts
//
// It is written into a temporary folder and renamed when complete, so a
// half-written snapshot is never taken for a valid one. A folder counts as a
// snapshot only if its name matches the scheme and it holds a readable
// manifest of this app; everything else in data/backups is left alone.
package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// App is the manifest's "app" value; it marks folders this package created.
const App = "modelkeeper"

// Reasons. Event reasons are kept 90 days, daily ones by count (see Rotate).
const (
	ReasonDaily        = "daily"
	ReasonManual       = "manual"
	ReasonRemoveRoot   = "pre-remove-root"
	ReasonFileTypes    = "pre-filetypes"
	reasonPreMigration = "pre-migration"
)

// PreMigration returns the reason of the snapshot taken before migration n.
func PreMigration(n int) string { return fmt.Sprintf("%s-%04d", reasonPreMigration, n) }

const (
	timeLayout   = "2006-01-02_150405"
	tmpPrefix    = ".tmp-"
	manifestName = "manifest.json"
)

var (
	reasonRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	nameRe   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}_\d{6})_([a-z0-9]+(?:-[a-z0-9]+)*)$`)
)

// Source is what a snapshot is made of.
type Source struct {
	DB         *sql.DB
	Settings   string // settings.json; skipped if missing
	Thumbnails string // thumbnail folder
	AppVersion string
}

// Manifest describes a snapshot.
type Manifest struct {
	App        string `json:"app"`
	AppVersion string `json:"appVersion"`
	Schema     int    `json:"schema"`  // PRAGMA user_version of the copy
	Created    string `json:"created"` // RFC 3339, local time
	Reason     string `json:"reason"`
	Models     int    `json:"models"`
	Tags       int    `json:"tags"`
	Thumbnails int    `json:"thumbnails"` // manual thumbnails copied
	// Verified: the copy passed PRAGMA quick_check (snapshots that fail it
	// are not kept). False in snapshots from before this check.
	Verified bool `json:"verified"`
	// ContentHash: SHA-256 over catalog.db, settings.json and the thumbnails
	// of the snapshot (VACUUM INTO writes identical copies of an unchanged
	// catalog), to recognize unchanged days.
	ContentHash string `json:"contentHash,omitempty"`
}

// Snapshot is a valid snapshot folder.
type Snapshot struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Time     time.Time `json:"time"`
	Manifest Manifest  `json:"manifest"`
}

// Create writes a snapshot into dir (data/backups) and returns it.
func Create(ctx context.Context, dir string, src Source, reason string, now time.Time) (Snapshot, error) {
	s, _, err := create(ctx, dir, src, reason, now, false)
	return s, err
}

// CreateIfChanged is Create, unless the content (catalog, settings, manual
// thumbnails) equals the newest snapshot's: then nothing is written and the
// newest snapshot is returned with created = false. Used for the daily
// snapshot, so unchanged days do not fill the drive with identical copies.
func CreateIfChanged(ctx context.Context, dir string, src Source, reason string, now time.Time) (Snapshot, bool, error) {
	return create(ctx, dir, src, reason, now, true)
}

func create(ctx context.Context, dir string, src Source, reason string, now time.Time, skipSame bool) (Snapshot, bool, error) {
	if !reasonRe.MatchString(reason) {
		return Snapshot{}, false, fmt.Errorf("invalid backup reason %q", reason)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Snapshot{}, false, err
	}
	now = now.Truncate(time.Second)
	name := now.Format(timeLayout) + "_" + reason
	for exists(filepath.Join(dir, name)) || exists(filepath.Join(dir, tmpPrefix+name)) {
		now = now.Add(time.Second) // two snapshots within one second
		name = now.Format(timeLayout) + "_" + reason
	}
	tmp := filepath.Join(dir, tmpPrefix+name)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return Snapshot{}, false, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()

	m, err := write(ctx, tmp, src)
	if err != nil {
		return Snapshot{}, false, err
	}
	if skipSame {
		if last, ok := Latest(dir); ok && last.Manifest.ContentHash != "" && last.Manifest.ContentHash == m.ContentHash {
			return last, false, nil // tmp is removed
		}
	}
	m.App, m.AppVersion, m.Reason, m.Created = App, src.AppVersion, reason, now.Format(time.RFC3339)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Snapshot{}, false, err
	}
	if err := writeFile(filepath.Join(tmp, manifestName), b); err != nil {
		return Snapshot{}, false, err
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(tmp, final); err != nil {
		return Snapshot{}, false, err
	}
	ok = true
	return Snapshot{Name: name, Path: final, Time: now, Manifest: m}, true, nil
}

// write fills a snapshot folder except for the manifest.
func write(ctx context.Context, tmp string, src Source) (Manifest, error) {
	var m Manifest
	dbFile := filepath.Join(tmp, "catalog.db")
	if _, err := src.DB.ExecContext(ctx, `VACUUM INTO ?`, dbFile); err != nil {
		return m, fmt.Errorf("copy catalog: %w", err)
	}
	if src.Settings != "" {
		if err := copyFile(src.Settings, filepath.Join(tmp, "settings.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return m, fmt.Errorf("copy settings: %w", err)
		}
	}

	// Counts and the thumbnail list come from the copy, so they match it.
	snap, err := sql.Open("sqlite", dbFile+"?"+url.Values{"_pragma": {"query_only(1)"}}.Encode())
	if err != nil {
		return m, err
	}
	defer snap.Close()
	if err := snap.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&m.Schema); err != nil {
		return m, fmt.Errorf("read copy: %w", err)
	}
	// A copy of a slightly damaged catalog can look fine; check it.
	if err := QuickCheck(ctx, snap); err != nil {
		return m, fmt.Errorf("copy failed the integrity check: %w", err)
	}
	m.Verified = true
	// Older schemas may lack a table; a missing count stays 0.
	snap.QueryRowContext(ctx, `SELECT count(*) FROM model`).Scan(&m.Models)
	snap.QueryRowContext(ctx, `SELECT count(*) FROM tag`).Scan(&m.Tags)

	var thumbs []string
	if rows, err := snap.QueryContext(ctx, `SELECT thumb_file FROM model
		WHERE thumb_source = 'manual' AND coalesce(thumb_file, '') <> ''`); err == nil {
		for rows.Next() {
			var f string
			if rows.Scan(&f) == nil {
				thumbs = append(thumbs, f)
			}
		}
		rows.Close()
	}
	if len(thumbs) > 0 && src.Thumbnails != "" {
		into := filepath.Join(tmp, "thumbnails")
		if err := os.Mkdir(into, 0o755); err != nil {
			return m, err
		}
		for _, f := range thumbs {
			if filepath.Base(f) != f || f == "." || f == ".." {
				continue // only plain file names live in the thumbnail folder
			}
			err := copyFile(filepath.Join(src.Thumbnails, f), filepath.Join(into, f))
			if errors.Is(err, fs.ErrNotExist) {
				continue // the catalog references a thumbnail that is gone
			}
			if err != nil {
				return m, fmt.Errorf("copy thumbnail: %w", err)
			}
			m.Thumbnails++
		}
	}
	snap.Close()
	if m.ContentHash, err = contentHash(tmp); err != nil {
		return m, err
	}
	return m, nil
}

// contentHash hashes the files of a snapshot folder (names and contents,
// in a fixed order), except the manifest.
func contentHash(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && p != filepath.Join(dir, manifestName) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s\x00", filepath.ToSlash(rel))
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "\x00%d\x00", n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Size returns the total size in bytes of the valid snapshots in dir.
func Size(dir string) (int64, error) {
	list, err := List(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, s := range list {
		total += SnapshotSize(s)
	}
	return total, nil
}

// QuickCheck runs PRAGMA quick_check on conn and returns its first problem.
func QuickCheck(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA quick_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != "ok" {
			return errors.New(line)
		}
	}
	return rows.Err()
}

// List returns the valid snapshots in dir, newest first. A missing dir is empty.
func List(dir string) ([]Snapshot, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, e := range entries {
		if s, ok := parse(dir, e); ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.After(out[j].Time)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// parse recognizes a snapshot folder created by this package.
func parse(dir string, e fs.DirEntry) (Snapshot, bool) {
	if !e.IsDir() {
		return Snapshot{}, false
	}
	sm := nameRe.FindStringSubmatch(e.Name())
	if sm == nil {
		return Snapshot{}, false
	}
	t, err := time.ParseInLocation(timeLayout, sm[1], time.Local)
	if err != nil {
		return Snapshot{}, false
	}
	p := filepath.Join(dir, e.Name())
	b, err := os.ReadFile(filepath.Join(p, manifestName))
	if err != nil {
		return Snapshot{}, false
	}
	var m Manifest
	if json.Unmarshal(b, &m) != nil || m.App != App || m.Reason != sm[2] {
		return Snapshot{}, false
	}
	return Snapshot{Name: e.Name(), Path: p, Time: t, Manifest: m}, true
}

// Latest returns the newest valid snapshot.
func Latest(dir string) (Snapshot, bool) {
	list, err := List(dir)
	if err != nil || len(list) == 0 {
		return Snapshot{}, false
	}
	return list[0], true
}

// HasDaily reports whether a daily snapshot of now's calendar day exists.
func HasDaily(dir string, now time.Time) bool {
	list, _ := List(dir)
	y, m, d := now.Date()
	for _, s := range list {
		sy, sm, sd := s.Time.Date()
		if s.Manifest.Reason == ReasonDaily && sy == y && sm == m && sd == d {
			return true
		}
	}
	return false
}

// dailyMarker records the day the daily snapshot was last considered, so an
// unchanged catalog is not copied again at every start of that day.
const dailyMarker = ".last-daily"

// Daily takes the daily snapshot unless today's was already taken or
// skipped, and skips it if nothing changed since the newest snapshot.
// created reports whether a snapshot was written.
func Daily(ctx context.Context, dir string, src Source, now time.Time) (s Snapshot, created bool, err error) {
	today := now.Format("2006-01-02")
	if b, err := os.ReadFile(filepath.Join(dir, dailyMarker)); err == nil && strings.TrimSpace(string(b)) == today {
		return Snapshot{}, false, nil
	}
	if HasDaily(dir, now) {
		return Snapshot{}, false, nil
	}
	s, created, err = CreateIfChanged(ctx, dir, src, ReasonDaily, now)
	if err != nil {
		return s, false, err
	}
	os.WriteFile(filepath.Join(dir, dailyMarker), []byte(today+"\n"), 0o644)
	return s, created, nil
}

// Retention (J2).
const (
	KeepNewest = 3                   // the newest snapshots are never deleted
	KeepDaily  = 14                  // newest daily snapshots kept
	KeepEvents = 90 * 24 * time.Hour // event snapshots (pre-*, manual) kept this long
	tmpMaxAge  = time.Hour           // leftovers of interrupted snapshots
)

// Rotate deletes snapshots beyond the retention rules and leftovers of
// interrupted snapshots. It only ever deletes folders it recognizes as its
// own (see List). Returns the names it deleted.
func Rotate(dir string, now time.Time) ([]string, error) {
	list, err := List(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	daily := 0
	for i, s := range list {
		keep := i < KeepNewest
		if s.Manifest.Reason == ReasonDaily {
			daily++
			keep = keep || daily <= KeepDaily
		} else {
			keep = keep || now.Sub(s.Time) <= KeepEvents
		}
		if keep {
			continue
		}
		if err := os.RemoveAll(s.Path); err != nil {
			return removed, err
		}
		removed = append(removed, s.Name)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return removed, err
	}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), tmpPrefix)
		if !ok || !e.IsDir() || !nameRe.MatchString(rest) {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > tmpMaxAge {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err == nil {
				removed = append(removed, e.Name())
			}
		}
	}
	return removed, nil
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeFile(p string, b []byte) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
