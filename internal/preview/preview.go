// Package preview reads model files for the 3D preview in the detail view (H4).
//
// Files on disk are streamed; archive members are read into memory only and
// never extracted to disk (D8). Model files are only ever read (J1).
package preview

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"modelkeeper/internal/archive"
)

// Exts are the file types the preview can display.
var Exts = map[string]bool{"stl": true, "obj": true, "3mf": true, "ply": true}

// ImageExts are the image types shown in the model's picture gallery.
var ImageExts = map[string]bool{"jpg": true, "jpeg": true, "png": true, "webp": true, "gif": true}

// MaxBytes is the largest file the preview loads.
const MaxBytes = 300 << 20

// MaxCopyMemberBytes is the largest archive member LookupAny reads (into
// memory); files on disk are streamed and have no limit.
const MaxCopyMemberBytes = 1 << 30

var (
	ErrNotFound    = errors.New("file not found")
	ErrUnsupported = errors.New("file type cannot be previewed")
	ErrOffline     = errors.New("root folder offline")
	ErrTooLarge    = archive.ErrTooLarge
)

// Source is a previewable file: on disk below a root, or inside an archive.
type Source struct {
	Name        string // base name of the file
	Ext         string
	Size        int64
	InArchive   bool
	rootPath    string
	path        string
	archivePath string
	limit       int64 // largest size Open reads; 0 = no limit for files on disk
}

// Lookup resolves a location to a file for the 3D preview or the picture
// gallery (Exts, ImageExts).
func Lookup(ctx context.Context, db *sql.DB, locationID int64) (Source, error) {
	s, err := lookup(ctx, db, locationID)
	switch {
	case err != nil:
		return s, err
	case !Exts[s.Ext] && !ImageExts[s.Ext]:
		return s, ErrUnsupported
	case s.Size > MaxBytes:
		return s, ErrTooLarge
	}
	s.limit = MaxBytes
	return s, nil
}

// LookupAny resolves a location of any type, for opening a copy of it in
// another program. Archive members up to MaxCopyMemberBytes are supported.
func LookupAny(ctx context.Context, db *sql.DB, locationID int64) (Source, error) {
	s, err := lookup(ctx, db, locationID)
	if err == nil && s.InArchive && s.Size > MaxCopyMemberBytes {
		return s, ErrTooLarge
	}
	return s, err
}

func lookup(ctx context.Context, db *sql.DB, locationID int64) (Source, error) {
	var s Source
	var status string
	var present bool
	err := db.QueryRowContext(ctx, `SELECT r.path, r.status, l.path, coalesce(a.path, ''), l.ext, l.size, l.present
		FROM location l JOIN root r ON r.id = l.root_id LEFT JOIN location a ON a.id = l.archive_id
		WHERE l.id = ?`, locationID).Scan(&s.rootPath, &status, &s.path, &s.archivePath, &s.Ext, &s.Size, &present)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return s, ErrNotFound
	case err != nil:
		return s, err
	case status == "offline":
		return s, ErrOffline
	case !present:
		return s, ErrNotFound
	}
	s.Name = path.Base(s.path)
	s.InArchive = s.archivePath != ""
	return s, nil
}

// Open returns the file's content. The caller closes it.
func (s Source) Open() (io.ReadSeekCloser, time.Time, error) {
	if s.archivePath != "" {
		arch := filepath.Join(s.rootPath, filepath.FromSlash(s.archivePath))
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(s.archivePath), "."))
		limit := s.limit
		if limit <= 0 {
			limit = MaxCopyMemberBytes
		}
		data, err := archive.ReadMember(arch, ext, s.path, limit)
		if err != nil {
			return nil, time.Time{}, err
		}
		return nopCloser{bytes.NewReader(data)}, time.Time{}, nil
	}
	f, err := os.Open(filepath.Join(s.rootPath, filepath.FromSlash(s.path)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = ErrNotFound
		}
		return nil, time.Time{}, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, time.Time{}, err
	}
	if s.limit > 0 && fi.Size() > s.limit {
		f.Close()
		return nil, time.Time{}, ErrTooLarge
	}
	return f, fi.ModTime(), nil
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }
