// Package bundle writes the files of several models into one zip archive
// (e.g. a collection for the slicer PC or a friend, G6). The model files are
// only read; the archive is written to a temporary file next to the target
// and renamed when complete.
package bundle

import (
	"archive/zip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// File is one file to put into the archive.
type File struct {
	Name string // path inside the archive
	Disk string // file on disk
	Size int64
}

// Plan lists the files of the models: files on disk, and archives as whole
// files (their members are not unpacked). Files of offline or deactivated
// roots and missing files are counted in Skipped.
type Plan struct {
	Files   []File
	Bytes   int64
	Skipped int
}

// unsafe characters in names inside the archive.
var unsafe = strings.NewReplacer(`\`, "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_")

func clean(s string) string {
	s = strings.TrimSpace(unsafe.Replace(s))
	s = strings.Trim(s, ". ")
	if s == "" {
		return "_"
	}
	return s
}

// Build collects the files of models (in this order) for the archive.
func Build(ctx context.Context, db *sql.DB, modelIDs []int64) (Plan, error) {
	var p Plan
	names := map[string]bool{}
	for _, id := range modelIDs {
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM model WHERE id = ?`, id).Scan(&name); err != nil {
			return p, err
		}
		dir := clean(name)
		for i := 2; names[strings.ToLower(dir)]; i++ {
			dir = fmt.Sprintf("%s (%d)", clean(name), i)
		}
		names[strings.ToLower(dir)] = true
		// Files on disk of the model, and the archives its members are in.
		rows, err := db.QueryContext(ctx, `SELECT DISTINCT r.path, coalesce(an.path, ''), d.path, d.size, d.present,
				r.status <> 'offline' AND r.removed_at IS NULL
			FROM location l
			JOIN location d ON d.id = coalesce(l.archive_id, l.id)
			JOIN root r ON r.id = d.root_id
			LEFT JOIN model_anchor an ON an.model_id = l.model_id AND an.root_id = d.root_id
				AND (d.path = an.path OR d.path LIKE an.path || '/%')
			WHERE l.model_id = ?
			ORDER BY d.path`, id)
		if err != nil {
			return p, err
		}
		seen := map[string]bool{}
		for rows.Next() {
			var root, anchor, rel string
			var size int64
			var present, online bool
			if err := rows.Scan(&root, &anchor, &rel, &size, &present, &online); err != nil {
				rows.Close()
				return p, err
			}
			key := root + "\x00" + rel
			if seen[key] {
				continue
			}
			seen[key] = true
			if !present || !online {
				p.Skipped++
				continue
			}
			inner := path.Base(rel)
			if anchor != "" && strings.HasPrefix(rel, anchor+"/") {
				inner = strings.TrimPrefix(rel, anchor+"/")
			}
			parts := strings.Split(inner, "/")
			for i := range parts {
				parts[i] = clean(parts[i])
			}
			p.Files = append(p.Files, File{Name: dir + "/" + strings.Join(parts, "/"),
				Disk: filepath.Join(root, filepath.FromSlash(rel)), Size: size})
			p.Bytes += size
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return p, err
		}
	}
	return p, nil
}

// stored: these are compressed already; deflating them again only costs time.
var stored = map[string]bool{".zip": true, ".rar": true, ".7z": true, ".jpg": true, ".jpeg": true, ".png": true,
	".webp": true, ".3mf": true, ".ctb": true, ".goo": true, ".lys": true, ".mp4": true, ".gz": true}

// Progress is reported while writing.
type Progress struct {
	Files     int   `json:"files"`
	FilesDone int   `json:"filesDone"`
	Bytes     int64 `json:"bytes"`
	BytesDone int64 `json:"bytesDone"`
}

// Write writes the planned files into the zip file target. A file that
// cannot be read stops it (the incomplete archive is removed). progress
// may be nil.
func Write(ctx context.Context, p Plan, target string, progress func(Progress)) error {
	tmp := target + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	z := zip.NewWriter(f)
	pr := Progress{Files: len(p.Files), Bytes: p.Bytes}
	last := time.Now()
	for _, file := range p.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		src, err := os.Open(file.Disk)
		if err != nil {
			return err
		}
		fi, err := src.Stat()
		if err != nil {
			src.Close()
			return err
		}
		h := &zip.FileHeader{Name: file.Name, Method: zip.Deflate, Modified: fi.ModTime()}
		if stored[strings.ToLower(path.Ext(file.Name))] {
			h.Method = zip.Store
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			src.Close()
			return err
		}
		buf := make([]byte, 1<<20)
		for {
			if err := ctx.Err(); err != nil {
				src.Close()
				return err
			}
			n, rerr := src.Read(buf)
			if n > 0 {
				if _, err := w.Write(buf[:n]); err != nil {
					src.Close()
					return err
				}
				pr.BytesDone += int64(n)
				if progress != nil && time.Since(last) > 250*time.Millisecond {
					progress(pr)
					last = time.Now()
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				src.Close()
				return rerr
			}
		}
		src.Close()
		pr.FilesDone++
	}
	if err := z.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	ok = true
	if progress != nil {
		progress(pr)
	}
	return nil
}
