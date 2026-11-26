package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modelkeeper/internal/userexport"
)

// ExternalSubdir is the folder Mirror writes into inside the external backup
// folder the user chose (another drive, a cloud folder).
const ExternalSubdir = "modelkeeper-backups"

// Mirror copies every snapshot of dir that is missing in ext/ExternalSubdir
// there, oldest first, and then applies the retention rules there (Rotate).
// Each copy is written to a temporary folder and renamed when complete, so an
// interrupted copy (drive unplugged) is never taken for a valid snapshot.
// Returns the folder it wrote into and the names it copied.
func Mirror(dir, ext string, now time.Time) (string, []string, error) {
	target := filepath.Join(ext, ExternalSubdir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return target, nil, err
	}
	local, err := List(dir)
	if err != nil {
		return target, nil, err
	}
	remote, err := List(target)
	if err != nil {
		return target, nil, err
	}
	have := map[string]bool{}
	for _, s := range remote {
		have[s.Name] = true
	}
	var copied []string
	for i := len(local) - 1; i >= 0; i-- {
		s := local[i]
		if have[s.Name] {
			continue
		}
		tmp := filepath.Join(target, tmpPrefix+s.Name)
		os.RemoveAll(tmp)
		if err := copyTree(s.Path, tmp); err != nil {
			os.RemoveAll(tmp)
			return target, copied, fmt.Errorf("copy %s: %w", s.Name, err)
		}
		if err := os.Rename(tmp, filepath.Join(target, s.Name)); err != nil {
			os.RemoveAll(tmp)
			return target, copied, err
		}
		copied = append(copied, s.Name)
	}
	if _, err := Rotate(target, now); err != nil {
		return target, copied, err
	}
	return target, copied, replaceFile(filepath.Join(target, "README.txt"), []byte(externalReadme))
}

const externalReadme = `modelkeeper – Sicherungen / backups
====================================

Deutsch
-------
Dieser Ordner enthält Kopien der Sicherungen von modelkeeper (Katalog,
Einstellungen, manuell gesetzte Vorschaubilder). Die Modelldateien selbst
sind nicht enthalten; sie bleiben, wo sie sind.

Wiederherstellen, z. B. auf einem neuen PC:
1. Einen neuen Ordner anlegen, z. B. C:\modelkeeper, und modelkeeper.exe
   aus diesem Ordner hineinkopieren.
2. Darin den Ordner data\backups\ anlegen und den gewünschten
   Sicherungsordner (z. B. 2026-11-17_083000_daily) hineinkopieren.
3. modelkeeper.exe starten. Die App meldet, dass der Katalog fehlt, und
   bietet die Sicherung zum Wiederherstellen an.
4. Die Ordner mit den Modellen (lokal, NAS) wieder so einbinden wie vorher,
   z. B. die NAS-Freigabe über denselben Netzwerkpfad.

English
-------
This folder holds copies of modelkeeper's backups (catalog, settings,
manually set thumbnails). The model files themselves are not included.

To restore, e.g. on a new PC:
1. Create a new folder, e.g. C:\modelkeeper, and copy modelkeeper.exe from
   this folder into it.
2. Create data\backups\ in it and copy the backup folder you want
   (e.g. 2026-11-17_083000_daily) into it.
3. Start modelkeeper.exe. It reports that the catalog is missing and offers
   the backup for restoring.
4. Make the model folders (local, NAS) available the same way as before.
`

// copyTree copies the regular files below from into to (created).
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, dst)
	})
}

// RestoreCheck is the result of a restore test (CheckRestore).
type RestoreCheck struct {
	Time     int64  `json:"time"` // unix ms
	Snapshot string `json:"snapshot"`
	Path     string `json:"path"`
	External bool   `json:"external"` // the copy in the external backup folder was tested
	OK       bool   `json:"ok"`
	// Problem is the technical description of what failed ("" if OK).
	Problem string `json:"problem,omitempty"`
	Models  int    `json:"models"`
	Tags    int    `json:"tags"`
}

// CheckRestore tests that snapshot s can be restored, the way a restore
// would use it: its files are unchanged since it was written (content hash),
// a copy of its catalog in work (a temporary folder, removed afterwards)
// opens and passes the full PRAGMA integrity_check, the numbers of models
// and tags match the manifest, the settings are valid JSON and the manual
// thumbnails are complete. The snapshot itself is only read.
func CheckRestore(ctx context.Context, s Snapshot, work string, now time.Time) RestoreCheck {
	r := RestoreCheck{Time: now.UnixMilli(), Snapshot: s.Name, Path: s.Path}
	if err := checkRestore(ctx, s, work, &r); err != nil {
		r.Problem = err.Error()
		return r
	}
	r.OK = true
	return r
}

func checkRestore(ctx context.Context, s Snapshot, work string, r *RestoreCheck) error {
	m := s.Manifest
	if m.ContentHash != "" {
		h, err := contentHash(s.Path)
		if err != nil {
			return fmt.Errorf("read snapshot: %w", err)
		}
		if h != m.ContentHash {
			return errors.New("the files of the snapshot changed since it was written (damaged copy)")
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.Path, "settings.json")); err == nil && !json.Valid(b) {
		return errors.New("settings.json is not valid")
	}
	thumbs := 0
	if entries, err := os.ReadDir(filepath.Join(s.Path, "thumbnails")); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() {
				thumbs++
			}
		}
	}
	if thumbs < m.Thumbnails {
		return fmt.Errorf("%d of %d manual thumbnails are missing", m.Thumbnails-thumbs, m.Thumbnails)
	}
	// The readable export is the fallback when the catalog cannot be used.
	if m.Export {
		if _, err := userexport.Read(filepath.Join(s.Path, userexport.FileName)); err != nil {
			return fmt.Errorf("%s cannot be read: %w", userexport.FileName, err)
		}
	}

	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(work, "restore-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "catalog.db")
	if err := copyFile(filepath.Join(s.Path, "catalog.db"), file); err != nil {
		return fmt.Errorf("copy catalog: %w", err)
	}
	conn, err := sql.Open("sqlite", file+"?"+url.Values{"_pragma": {"query_only(1)"}}.Encode())
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := integrityCheck(ctx, conn); err != nil {
		return fmt.Errorf("catalog failed the integrity check: %w", err)
	}
	var schema int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&schema); err != nil {
		return err
	}
	if schema != m.Schema {
		return fmt.Errorf("schema %d, manifest says %d", schema, m.Schema)
	}
	conn.QueryRowContext(ctx, `SELECT count(*) FROM model`).Scan(&r.Models)
	conn.QueryRowContext(ctx, `SELECT count(*) FROM tag`).Scan(&r.Tags)
	if r.Models != m.Models || r.Tags != m.Tags {
		return fmt.Errorf("%d models and %d tags, manifest says %d and %d", r.Models, r.Tags, m.Models, m.Tags)
	}
	return nil
}

// integrityCheck runs the full PRAGMA integrity_check (slower and more
// thorough than quick_check) and returns its first problem.
func integrityCheck(ctx context.Context, conn *sql.DB) error {
	var line string
	if err := conn.QueryRowContext(ctx, `PRAGMA integrity_check(1)`).Scan(&line); err != nil {
		return err
	}
	if line != "ok" {
		return errors.New(line)
	}
	return nil
}

const checkFile = "restore-test.json"

// SaveRestoreCheck stores the result of the newest restore test in the data
// folder, for the settings.
func SaveRestoreCheck(dataDir string, r RestoreCheck) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return replaceFile(filepath.Join(dataDir, checkFile), b)
}

// LastRestoreCheck returns the stored result of the newest restore test, nil if none.
func LastRestoreCheck(dataDir string) *RestoreCheck {
	b, err := os.ReadFile(filepath.Join(dataDir, checkFile))
	if err != nil {
		return nil
	}
	var r RestoreCheck
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// replaceFile writes a file that may exist already: into a temporary file
// first, then renamed over the old one.
func replaceFile(p string, b []byte) error {
	tmp := p + ".tmp"
	os.Remove(tmp)
	if err := writeFile(tmp, b); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

// ExternalDir returns the folder Mirror writes into below ext.
func ExternalDir(ext string) string { return filepath.Join(ext, ExternalSubdir) }

// Fetch copies snapshot name from another backup folder (the external one,
// or one picked on a new PC) into dir, so that it can be restored from
// there. The copy is written to a temporary folder and renamed when
// complete; its content hash is checked, so a damaged copy is refused. A
// snapshot already in dir is kept as it is. Returns the local snapshot.
func Fetch(ctx context.Context, from, dir, name string) (Snapshot, error) {
	if s, err := Find(dir, name); err == nil {
		return s, nil
	}
	src, err := Find(from, name)
	if err != nil {
		return Snapshot{}, err
	}
	if err := Verify(ctx, src); err != nil {
		return Snapshot{}, fmt.Errorf("snapshot %s is damaged: %w", name, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Snapshot{}, err
	}
	tmp := filepath.Join(dir, tmpPrefix+name)
	os.RemoveAll(tmp)
	if err := copyTree(src.Path, tmp); err != nil {
		os.RemoveAll(tmp)
		return Snapshot{}, err
	}
	if h, err := contentHash(tmp); err != nil || (src.Manifest.ContentHash != "" && h != src.Manifest.ContentHash) {
		os.RemoveAll(tmp)
		if err == nil {
			err = errors.New("the copy differs from the original")
		}
		return Snapshot{}, fmt.Errorf("copy of %s: %w", name, err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.RemoveAll(tmp)
		return Snapshot{}, err
	}
	return Find(dir, name)
}
