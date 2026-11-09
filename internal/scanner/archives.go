package scanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// archives is phase 3: hash each pending archive and read all of its members (F7, F11).
// Archives are processed one at a time to bound memory use.
func (s *scan) archives(ctx context.Context) error {
	buf := make([]byte, 1<<20)
	for i, j := range s.pendingArchives {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.t.update(func(p *Progress) { p.Current = j.path })
		if err := s.processArchive(ctx, j, buf); err != nil {
			return err
		}
		s.t.update(func(p *Progress) { p.ArchivesDone = i + 1 })
	}
	return nil
}

func (s *scan) processArchive(ctx context.Context, j hashJob, buf []byte) error {
	abs := filepath.Join(s.root.Path, filepath.FromSlash(j.path))
	if !unchanged(abs, j) {
		return nil // changed since the walk; the next scan handles it
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(j.path), "."))

	// Small archives: read once, hash and open from memory. Large: two passes.
	var data []byte
	var sum [32]byte
	var err error
	if j.size <= s.opts.ArchiveMemLimit {
		data, err = readAll(ctx, abs, j.size, buf, s.t.addBytes)
		if err == nil {
			sum = sha256.Sum256(data)
		}
	} else {
		sum, _, err = hashFile(ctx, abs, buf, s.t.addBytes)
	}
	if err != nil {
		return s.archiveReadError(ctx, j, err)
	}
	if !unchanged(abs, j) {
		return nil
	}
	// Same content as last time (full check, F9): the members were read and
	// verified then and are identical now, so the checksums need no second pass.
	if s.checkUnchanged(j, sum) && j.old != nil && archiveFinal[j.state] {
		return nil
	}

	res := readArchive(ctx, abs, ext, data, nil)
	data = nil
	if err := ctx.Err(); err != nil {
		return err
	}
	if !unchanged(abs, j) {
		return nil
	}
	if res.Status == archiveBroken && !rootReachable(s.root.Path) {
		return &offlineError{errors.New(res.Detail)}
	}
	if err := s.storeArchive(context.Background(), j, sum, res); err != nil {
		return err
	}

	switch res.Status {
	case archiveBroken:
		s.issue(j.id, "archive_broken", res.ErrKey, "path", j.path, "detail", res.Detail)
		s.t.update(func(p *Progress) { p.ArchivesBroken++ })
	case archiveEncrypted:
		s.issue(j.id, "archive_encrypted", res.ErrKey, "path", j.path)
		s.t.update(func(p *Progress) { p.ArchivesEncrypted++ })
	}
	return nil
}

// archiveFinal: archive states that are the result of reading the archive.
var archiveFinal = map[string]bool{archiveOK: true, archiveBroken: true, archiveEncrypted: true}

// archiveReadError handles I/O errors on the archive file itself (not format errors).
func (s *scan) archiveReadError(ctx context.Context, j hashJob, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	if !rootReachable(s.root.Path) {
		return &offlineError{err}
	}
	s.issue(j.id, "read_error", "scan.issue.readError", "path", j.path, "detail", err.Error())
	return nil // stays pending; retried next scan
}

func (s *scan) storeArchive(ctx context.Context, j hashJob, sum [32]byte, res archiveResult) error {
	return withRetry(ctx, func(ctx context.Context) error { return s.storeArchiveOnce(ctx, j, sum, res) })
}

func (s *scan) storeArchiveOnce(ctx context.Context, j hashJob, sum [32]byte, res archiveResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	fid, err := upsertFile(ctx, tx, j.size, sum)
	if err != nil {
		return err
	}
	var errKey any
	if res.ErrKey != "" {
		errKey = res.ErrKey
	}
	r, err := tx.ExecContext(ctx, `UPDATE location SET file_id = ?, archive_status = ?, archive_error = ?
		WHERE id = ? AND size = ? AND mtime = ?`, fid, res.Status, errKey, j.id, j.size, j.mtime)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return nil // row changed meanwhile
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM location WHERE archive_id = ?`, j.id); err != nil {
		return err
	}
	now := nowMs()
	for _, m := range res.Members {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(m.Path), "."))
		cat := s.st.ExtCategory(ext)
		if cat == "" {
			continue // read for the checksum, but not cataloged
		}
		mfid, err := upsertFile(ctx, tx, m.Size, m.SHA256)
		if err != nil {
			return err
		}
		var nested any
		if cat == "archive" {
			nested = archiveUnsupported // nested archives are not opened
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO location (root_id, archive_id, path, ext, category,
			size, mtime, file_id, archive_status, present, last_seen_scan, first_seen_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			s.root.ID, j.id, m.Path, ext, cat, m.Size, m.Mtime, mfid, nested, s.runID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// readAll reads a file of known size into memory, honoring cancellation.
func readAll(ctx context.Context, abs string, size int64, buf []byte, countBytes func(int64)) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out bytes.Buffer
	out.Grow(int(size))
	if _, err := io.CopyBuffer(&out, &countingReader{r: f, ctx: ctx, add: countBytes}, buf); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
