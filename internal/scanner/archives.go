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
	"time"

	"modelkeeper/internal/archive"
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

	// Full check of an archive that was read before: hash it first; only if
	// its content changed are the members read again (F9).
	quickFull := s.full && j.old != nil && archiveFinal[j.state]
	// Large zips are read in one pass with checkpoints; small archives of any
	// format are read into memory once.
	inMemory := j.size <= s.opts.ArchiveMemLimit && !(ext == "zip" && j.size >= s.opts.ResumeMin)

	var sum [32]byte
	var res archiveResult
	var err error
	switch {
	case inMemory:
		var data []byte
		if data, err = readAll(ctx, abs, j.size, buf, s.t.addBytes); err != nil {
			return s.archiveReadError(ctx, j, err)
		}
		sum = sha256.Sum256(data)
		if !unchanged(abs, j) {
			return nil
		}
		if s.checkUnchanged(j, sum) && quickFull {
			return nil
		}
		res = readArchive(ctx, abs, ext, data, nil)
	case quickFull || (ext != "zip" && ext != "7z"):
		// rar (multi-volume archives are opened by the library) and quick
		// full checks: hash (resumable), then read the members if needed.
		if sum, err = s.hashResumable(ctx, abs, j, buf); err != nil {
			return s.archiveReadError(ctx, j, err)
		}
		if !unchanged(abs, j) {
			return nil
		}
		if s.checkUnchanged(j, sum) && quickFull {
			s.dropCheckpoint(j.id)
			return nil
		}
		res = readArchive(ctx, abs, ext, nil, nil)
	default:
		sum, res, err = s.readArchiveOnePass(ctx, abs, ext, j)
		if err != nil {
			return s.archiveReadError(ctx, j, err)
		}
		if !unchanged(abs, j) {
			return nil
		}
		s.checkUnchanged(j, sum)
	}
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

// readArchiveOnePass reads a zip or 7z once: the archive library reads the
// file through a prefixHasher, which hashes it on the way. Zips from
// ResumeMin on save checkpoints after entries and continue from one.
// A returned error is an I/O error on the file itself.
func (s *scan) readArchiveOnePass(ctx context.Context, abs, ext string, j hashJob) ([32]byte, archiveResult, error) {
	var sum [32]byte
	f, err := os.Open(abs)
	if err != nil {
		return sum, archiveResult{}, err
	}
	defer f.Close()
	ph := newPrefixHasher(f, j.size, s.t.addBytes)
	resumable := ext == "zip" && j.size >= s.opts.ResumeMin
	var cp *checkpoint
	if resumable {
		if cp = s.loadCheckpoint(ctx, j); cp != nil && ph.resume(cp.State, cp.Offset) != nil {
			cp, ph = nil, newPrefixHasher(f, j.size, s.t.addBytes)
		}
	}

	var res archiveResult
	it, err := archive.OpenReaderAt(ext, ph, j.size)
	if err != nil {
		res = classify(err, nil)
	} else {
		sk, _ := it.(archive.Skipper)
		var members []archiveMember
		if cp != nil && sk != nil && sk.SkipTo(cp.Next) == nil {
			members = cp.Members
		}
		var hook entryHook
		if resumable && sk != nil {
			tick := ticker{every: s.opts.CheckpointEvery, last: time.Now()}
			hook = func(next int, members []archiveMember, force bool) {
				if !force && !tick.due() {
					return
				}
				if state, pos, err := ph.checkpoint(); err == nil && pos > 0 {
					s.saveCheckpoint(j, checkpoint{Offset: pos, State: state, Next: next, Members: members})
				}
			}
		}
		res = readEntries(ctx, it, members, sk, nil, hook)
		it.Close()
	}
	if err := ph.readErr(); err != nil {
		return sum, res, err // I/O, not a damaged archive
	}
	if err := ctx.Err(); err != nil {
		return sum, res, err
	}
	sum, err = ph.finish(ctx)
	return sum, res, err
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM hash_progress WHERE location_id = ?`, j.id); err != nil {
		return err
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
