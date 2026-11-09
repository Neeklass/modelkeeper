package scanner

import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"time"
)

// Resuming inside large files. While a large file is hashed, or a large zip
// is read, a checkpoint is saved about once per CheckpointEvery and when the
// scan is cancelled: the SHA-256 state over the bytes read so far and, for
// zips, the entries already verified. The next scan continues from there,
// provided size and modification time are unchanged; otherwise the file is
// read from the start. The checkpoint is removed when the file's result is
// stored, and leftovers when a scan of the root completes.

type checkpoint struct {
	Offset  int64           `json:"offset"`            // bytes covered by State
	State   []byte          `json:"state"`             // SHA-256 state (encoding.BinaryMarshaler)
	Next    int             `json:"next,omitempty"`    // zip: index of the next entry to read
	Members []archiveMember `json:"members,omitempty"` // zip: entries read and verified so far
}

func (s *scan) loadCheckpoint(ctx context.Context, j hashJob) *checkpoint {
	var size, mtime int64
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT size, mtime, data FROM hash_progress WHERE location_id = ?`, j.id).
		Scan(&size, &mtime, &data)
	if err != nil {
		return nil
	}
	var cp checkpoint
	if size != j.size || mtime != j.mtime || json.Unmarshal(data, &cp) != nil || cp.Offset <= 0 || cp.Offset > j.size {
		s.dropCheckpoint(j.id) // the file changed: start over
		return nil
	}
	return &cp
}

// saveCheckpoint stores a checkpoint; failures only cost the resume.
func (s *scan) saveCheckpoint(j hashJob, cp checkpoint) {
	data, err := json.Marshal(cp)
	if err != nil {
		return
	}
	err = withRetry(context.Background(), func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, `INSERT INTO hash_progress (location_id, size, mtime, offset, data, updated_at)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (location_id) DO UPDATE SET size = excluded.size,
			mtime = excluded.mtime, offset = excluded.offset, data = excluded.data, updated_at = excluded.updated_at`,
			j.id, j.size, j.mtime, cp.Offset, data, nowMs())
		return err
	})
	if err != nil {
		log.Printf("scan: save checkpoint %s: %v", j.path, err)
	}
	if s.opts.onCheckpoint != nil {
		s.opts.onCheckpoint()
	}
}

func (s *scan) dropCheckpoint(id int64) {
	s.db.Exec(`DELETE FROM hash_progress WHERE location_id = ?`, id)
}

// ticker tells when the next checkpoint is due.
type ticker struct {
	every time.Duration
	last  time.Time
}

func (t *ticker) due() bool {
	if time.Since(t.last) < t.every {
		return false
	}
	t.last = time.Now()
	return true
}

// hashResumable hashes a file on disk. Files of at least ResumeMin bytes
// continue from a checkpoint and save checkpoints while being read.
func (s *scan) hashResumable(ctx context.Context, abs string, j hashJob, buf []byte) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(abs)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	resumable := j.size >= s.opts.ResumeMin
	var off int64
	if resumable {
		if cp := s.loadCheckpoint(ctx, j); cp != nil {
			if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(cp.State); err == nil {
				if _, err := f.Seek(cp.Offset, io.SeekStart); err == nil {
					off = cp.Offset
				}
			}
			if off == 0 {
				h.Reset()
			}
		}
	}
	save := func() {
		if state, err := h.(encoding.BinaryMarshaler).MarshalBinary(); err == nil && off > 0 {
			s.saveCheckpoint(j, checkpoint{Offset: off, State: state})
		}
	}
	tick := ticker{every: s.opts.CheckpointEvery, last: time.Now()}
	for {
		if err := ctx.Err(); err != nil {
			if resumable {
				save() // Cancel loses nothing
			}
			return sum, err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			off += int64(n)
			s.t.addBytes(int64(n))
			if resumable && tick.due() {
				save()
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			if resumable {
				save()
			}
			return sum, rerr
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
