package scanner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type hashJob struct {
	id    int64
	path  string // rel
	size  int64
	mtime int64
	old   []byte // known SHA-256, nil if not hashed yet
	state string // archive_status of archives ("" if none)
}

type hashResult struct {
	job   hashJob
	sum   [32]byte
	err   error
	stale bool // file changed while hashing; next scan picks it up
}

// pendingWork loads the files and archives that phase 2 and 3 must process
// and initializes the progress totals.
func (s *scan) pendingWork(ctx context.Context) (files, archives []hashJob, err error) {
	load := func(where string) ([]hashJob, error) {
		rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.path, l.size, l.mtime, f.sha256, coalesce(l.archive_status, '')
			FROM location l LEFT JOIN file f ON f.id = l.file_id
			WHERE l.root_id = ? AND l.archive_id IS NULL AND l.present = 1 AND `+where+`
			ORDER BY l.path`, s.root.ID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []hashJob
		for rows.Next() {
			var j hashJob
			if err := rows.Scan(&j.id, &j.path, &j.size, &j.mtime, &j.old, &j.state); err != nil {
				return nil, err
			}
			out = append(out, j)
		}
		return out, rows.Err()
	}
	// A full check (F9) takes every file; otherwise only unhashed ones (F3).
	fileCond, archiveCond := `l.category <> 'archive' AND l.file_id IS NULL`,
		`l.category = 'archive' AND (l.file_id IS NULL OR l.archive_status = 'pending')`
	if s.full {
		fileCond, archiveCond = `l.category <> 'archive'`, `l.category = 'archive'`
	}
	files, err = load(fileCond)
	if err != nil {
		return nil, nil, err
	}
	archives, err = load(archiveCond)
	if err != nil {
		return nil, nil, err
	}
	var bytes int64
	for _, j := range files {
		bytes += j.size
	}
	for _, j := range archives {
		bytes += j.size
	}
	// Already read before an interruption (checkpoints) does not count again.
	var resumed int64
	s.db.QueryRowContext(ctx, `SELECT coalesce(sum(p.offset), 0) FROM hash_progress p JOIN location l ON l.id = p.location_id
		WHERE l.root_id = ? AND p.size = l.size AND p.mtime = l.mtime`, s.root.ID).Scan(&resumed)
	bytes = max(0, bytes-resumed)
	s.t.update(func(p *Progress) {
		p.HashTotal, p.BytesTotal, p.ArchivesTotal = len(files), bytes, len(archives)
	})
	return files, archives, nil
}

// hashFiles is phase 2: hash every present, not yet hashed, non-archive file (F3).
func (s *scan) hashFiles(ctx context.Context) error {
	files, archives, err := s.pendingWork(ctx)
	if err != nil {
		return err
	}
	s.pendingArchives = archives
	if len(files) == 0 {
		return nil
	}

	jobs := make(chan hashJob)
	results := make(chan hashResult)
	var wg sync.WaitGroup
	for i := 0; i < s.opts.HashWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 1<<20)
			for j := range jobs {
				results <- s.hashOne(ctx, j, buf)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, j := range files {
			select {
			case jobs <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	var pending []hashResult
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		// Use a fresh context: results already computed are worth keeping on cancel.
		err := s.storeHashes(context.Background(), pending)
		pending = pending[:0]
		return err
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	done, offline := 0, 0
	var storeErr error
	for {
		select {
		case r, ok := <-results:
			if !ok {
				if err := flush(); err != nil {
					return err
				}
				if storeErr != nil {
					return storeErr
				}
				if offline > 0 && !rootReachable(s.root.Path) {
					return &offlineError{errors.New("root vanished while hashing")}
				}
				return ctx.Err()
			}
			done++
			s.t.update(func(p *Progress) { p.HashDone, p.Current = done, r.job.path })
			switch {
			case r.err != nil && errors.Is(r.err, context.Canceled):
			case r.err != nil:
				if !rootReachable(s.root.Path) {
					offline++
					continue
				}
				s.issue(r.job.id, "read_error", "scan.issue.readError", "path", r.job.path, "detail", r.err.Error())
			case r.stale:
			default:
				s.checkUnchanged(r.job, r.sum)
				pending = append(pending, r)
				// Saved right away: an interruption costs at most the files in progress.
				if err := flush(); err != nil && storeErr == nil {
					storeErr = err
				}
			}
		case <-tick.C:
			if err := flush(); err != nil && storeErr == nil {
				storeErr = err
			}
		}
	}
}

func (s *scan) hashOne(ctx context.Context, j hashJob, buf []byte) hashResult {
	abs := filepath.Join(s.root.Path, filepath.FromSlash(j.path))
	sum, err := s.hashResumable(ctx, abs, j, buf)
	if err != nil {
		return hashResult{job: j, err: err}
	}
	return hashResult{job: j, sum: sum, stale: !unchanged(abs, j)}
}

// checkUnchanged reports a file whose content differs from the known hash
// although size and modification time are the same (F8): the file was
// modified without updating its timestamp, or the data is damaged.
// Returns false in that case.
func (s *scan) checkUnchanged(j hashJob, sum [32]byte) bool {
	if j.old == nil || bytes.Equal(j.old, sum[:]) {
		return true
	}
	s.issue(j.id, "hash_changed", "scan.issue.hashChanged", "path", j.path,
		"old", hex.EncodeToString(j.old)[:12], "new", hex.EncodeToString(sum[:])[:12])
	s.t.update(func(p *Progress) { p.HashChanged++ })
	return false
}

// unchanged reports whether the file still has the size/mtime from the catalog.
func unchanged(abs string, j hashJob) bool {
	fi, err := os.Stat(abs)
	return err == nil && fi.Size() == j.size && fi.ModTime().UnixNano() == j.mtime
}

func (s *scan) storeHashes(ctx context.Context, rs []hashResult) error {
	return withRetry(ctx, func(ctx context.Context) error { return s.storeHashesOnce(ctx, rs) })
}

func (s *scan) storeHashesOnce(ctx context.Context, rs []hashResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rs {
		fid, err := upsertFile(ctx, tx, r.job.size, r.sum)
		if err != nil {
			return err
		}
		// Guard on size/mtime: if the walk of a later scan changed the row, keep it unhashed.
		if _, err := tx.ExecContext(ctx, `UPDATE location SET file_id = ? WHERE id = ? AND size = ? AND mtime = ?`,
			fid, r.job.id, r.job.size, r.job.mtime); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM hash_progress WHERE location_id = ?`, r.job.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// upsertFile returns the id of the file with this hash, creating it if needed.
func upsertFile(ctx context.Context, tx *sql.Tx, size int64, sum [32]byte) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO file (sha256, size) VALUES (?, ?)
		ON CONFLICT (sha256) DO UPDATE SET size = excluded.size RETURNING id`, sum[:], size).Scan(&id)
	return id, err
}
