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
)

// Restoring a snapshot (J2) replaces catalog.db, settings.json and the
// manual thumbnails in the data folder. It cannot happen while the catalog
// is open, so the user requests it and it is applied at the next start,
// before settings and catalog are opened. The state it replaces is kept as
// a snapshot of its own ("pre-restore"), so a restore can be undone the
// same way. Only files below the data folder are written (J1).

// ReasonPreRestore marks the state a restore replaced.
const ReasonPreRestore = "pre-restore"

// restoreMarker holds a pending restore request, in the backups folder.
const restoreMarker = ".restore-request.json"

// Target is the data folder a snapshot is restored into.
type Target struct {
	Catalog    string // data/catalog.db
	Settings   string // data/settings.json
	Thumbnails string // data/thumbnails
	Backups    string // data/backups
}

type restoreRequest struct {
	Snapshot  string `json:"snapshot"`
	Requested string `json:"requested"`
}

// Find returns the valid snapshot with this name.
func Find(dir, name string) (Snapshot, error) {
	list, err := List(dir)
	if err != nil {
		return Snapshot{}, err
	}
	for _, s := range list {
		if s.Name == name {
			return s, nil
		}
	}
	return Snapshot{}, fmt.Errorf("snapshot %q not found", name)
}

// Verify opens a snapshot's catalog read-only and runs PRAGMA quick_check.
func Verify(ctx context.Context, s Snapshot) error {
	file := filepath.Join(s.Path, "catalog.db")
	if _, err := os.Stat(file); err != nil {
		return err
	}
	conn, err := sql.Open("sqlite", file+"?"+url.Values{"_pragma": {"query_only(1)"}}.Encode())
	if err != nil {
		return err
	}
	defer conn.Close()
	return QuickCheck(ctx, conn)
}

// RequestRestore records that snapshot name is to be restored at the next
// start, after checking that it is intact.
func RequestRestore(ctx context.Context, t Target, name string, now time.Time) error {
	s, err := Find(t.Backups, name)
	if err != nil {
		return err
	}
	if err := Verify(ctx, s); err != nil {
		return fmt.Errorf("snapshot %s is damaged: %w", name, err)
	}
	b, _ := json.Marshal(restoreRequest{Snapshot: name, Requested: now.Format(time.RFC3339)})
	tmp := filepath.Join(t.Backups, restoreMarker+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(t.Backups, restoreMarker))
}

// PendingRestore returns the snapshot requested for the next start, if any.
func PendingRestore(t Target) (string, bool) {
	b, err := os.ReadFile(filepath.Join(t.Backups, restoreMarker))
	if err != nil {
		return "", false
	}
	var r restoreRequest
	if json.Unmarshal(b, &r) != nil || r.Snapshot == "" {
		return "", false
	}
	return r.Snapshot, true
}

// CancelRestore drops a pending restore request.
func CancelRestore(t Target) error {
	err := os.Remove(filepath.Join(t.Backups, restoreMarker))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Restored describes an applied restore.
type Restored struct {
	From     Snapshot // the snapshot that was restored
	Previous Snapshot // the replaced state, kept as a pre-restore snapshot
}

// ApplyPendingRestore restores a requested snapshot. Call it at start,
// before settings and catalog are opened. Returns nil without a request.
// The request is consumed either way; on an error the data folder is as it
// was before.
func ApplyPendingRestore(ctx context.Context, t Target, appVersion string, now time.Time) (*Restored, error) {
	name, ok := PendingRestore(t)
	if !ok {
		return nil, nil
	}
	defer CancelRestore(t) // never retried at every start
	from, err := Find(t.Backups, name)
	if err != nil {
		return nil, err
	}
	if err := Verify(ctx, from); err != nil {
		return nil, fmt.Errorf("snapshot %s is damaged: %w", name, err)
	}

	prev, undo, err := keepCurrent(ctx, t, from, appVersion, now)
	if err != nil {
		return nil, fmt.Errorf("keep current state: %w", err)
	}
	if err := install(t, from); err != nil {
		if uerr := undo(); uerr != nil {
			return nil, fmt.Errorf("restore %s: %v; putting the previous state back also failed: %v (it is in %s)", name, err, uerr, prev.Path)
		}
		return nil, fmt.Errorf("restore %s: %w", name, err)
	}
	return &Restored{From: from, Previous: prev}, nil
}

// keepCurrent moves the current catalog (with its WAL files) and copies the
// settings and the thumbnails the snapshot will overwrite into a new
// pre-restore snapshot. undo puts the catalog back.
func keepCurrent(ctx context.Context, t Target, from Snapshot, appVersion string, now time.Time) (Snapshot, func() error, error) {
	now = now.Truncate(time.Second)
	name := now.Format(timeLayout) + "_" + ReasonPreRestore
	for exists(filepath.Join(t.Backups, name)) {
		now = now.Add(time.Second)
		name = now.Format(timeLayout) + "_" + ReasonPreRestore
	}
	tmp := filepath.Join(t.Backups, tmpPrefix+name)
	final := filepath.Join(t.Backups, name)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return Snapshot{}, nil, err
	}

	var moved [][2]string // from, to
	moveBack := func(dir string) error {
		var first error
		for i := len(moved) - 1; i >= 0; i-- {
			src := filepath.Join(dir, filepath.Base(moved[i][1]))
			if err := os.Rename(src, moved[i][0]); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	fail := func(err error) (Snapshot, func() error, error) {
		if berr := moveBack(tmp); berr != nil {
			return Snapshot{}, nil, fmt.Errorf("%v; moving the catalog back failed: %v (it is in %s)", err, berr, tmp)
		}
		os.RemoveAll(tmp)
		return Snapshot{}, nil, err
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := t.Catalog + suffix
		dst := filepath.Join(tmp, "catalog.db"+suffix)
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fail(err)
		}
		moved = append(moved, [2]string{src, dst})
	}
	if err := copyFile(t.Settings, filepath.Join(tmp, "settings.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail(err)
	}
	if entries, err := os.ReadDir(filepath.Join(from.Path, "thumbnails")); err == nil {
		for _, e := range entries {
			cur := filepath.Join(t.Thumbnails, e.Name())
			if _, err := os.Stat(cur); err != nil {
				continue
			}
			os.MkdirAll(filepath.Join(tmp, "thumbnails"), 0o755)
			if err := copyFile(cur, filepath.Join(tmp, "thumbnails", e.Name())); err != nil {
				return fail(err)
			}
		}
	}

	m := Manifest{App: App, AppVersion: appVersion, Reason: ReasonPreRestore, Created: now.Format(time.RFC3339)}
	if len(moved) > 0 {
		describeCatalog(ctx, filepath.Join(tmp, "catalog.db"), &m) // best effort: it may be the damaged one
	}
	m.ContentHash, _ = contentHash(tmp)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := writeFile(filepath.Join(tmp, manifestName), b); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fail(err)
	}
	s := Snapshot{Name: name, Path: final, Time: now, Manifest: m}
	undo := func() error {
		os.Remove(t.Catalog) // the partly installed copy
		if err := moveBack(final); err != nil {
			return err
		}
		if err := copyOver(filepath.Join(final, "settings.json"), t.Settings); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// Thumbnails: put back the ones that were overwritten, remove the added ones.
		if entries, err := os.ReadDir(filepath.Join(from.Path, "thumbnails")); err == nil {
			for _, e := range entries {
				saved := filepath.Join(final, "thumbnails", e.Name())
				cur := filepath.Join(t.Thumbnails, e.Name())
				if _, err := os.Stat(saved); err == nil {
					if err := copyOver(saved, cur); err != nil {
						return err
					}
				} else {
					os.Remove(cur)
				}
			}
		}
		return os.RemoveAll(final) // everything is back in place
	}
	return s, undo, nil
}

// copyOver copies src to dst, replacing dst.
func copyOver(src, dst string) error {
	tmp := dst + ".restore-tmp"
	os.Remove(tmp)
	if err := copyFile(src, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// describeCatalog fills schema, counts and Verified from a catalog file,
// leaving them empty if it cannot be read.
func describeCatalog(ctx context.Context, file string, m *Manifest) {
	conn, err := sql.Open("sqlite", file+"?"+url.Values{"_pragma": {"query_only(1)"}}.Encode())
	if err != nil {
		return
	}
	defer conn.Close()
	if conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&m.Schema) != nil {
		return
	}
	conn.QueryRowContext(ctx, `SELECT count(*) FROM model`).Scan(&m.Models)
	conn.QueryRowContext(ctx, `SELECT count(*) FROM tag`).Scan(&m.Tags)
	m.Verified = QuickCheck(ctx, conn) == nil
}

// install copies a snapshot's files into the data folder (each file via a
// temporary file and a rename).
func install(t Target, from Snapshot) error {
	put := copyOver
	if err := put(filepath.Join(from.Path, "catalog.db"), t.Catalog); err != nil {
		return err
	}
	if err := put(filepath.Join(from.Path, "settings.json"), t.Settings); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(from.Path, "thumbnails"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.Thumbnails, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if err := put(filepath.Join(from.Path, "thumbnails", e.Name()), filepath.Join(t.Thumbnails, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// SnapshotSize returns the size of one snapshot folder in bytes.
func SnapshotSize(s Snapshot) int64 {
	var total int64
	filepath.WalkDir(s.Path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}
