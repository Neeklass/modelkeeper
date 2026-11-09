package scanner

import (
	"crypto/sha256"
	"encoding"
	"errors"
	"hash"
	"io"
	"os"
	"sync"
)

// prefixHasher is an io.ReaderAt over a file that hashes the file while an
// archive library reads it, so an archive is read once for both its hash and
// its contents.
//
// Invariant: h is the SHA-256 of file[0:pos). Bytes fetched contiguously from
// pos extend the hash; other reads (the zip directory at the end, out-of-order
// access) are served but not hashed. finish hashes whatever is still missing,
// so the result is always the hash of the whole file, however it was read.
//
// Reads go through a window of window bytes, which also turns the small reads
// of decompressors into large reads (important over SMB).
type prefixHasher struct {
	mu   sync.Mutex
	f    *os.File
	size int64
	h    hash.Hash
	pos  int64

	buf        []byte
	start, end int64 // file range held in buf

	count func(int64) // bytes read from the file (progress)
	ioErr error       // first read error from the file (not a format error)
}

const prefixWindow = 1 << 20

func newPrefixHasher(f *os.File, size int64, count func(int64)) *prefixHasher {
	return &prefixHasher{f: f, size: size, h: sha256.New(), buf: make([]byte, prefixWindow), count: count}
}

// resume continues from a saved hash state that covers file[0:pos).
func (p *prefixHasher) resume(state []byte, pos int64) error {
	if pos < 0 || pos > p.size {
		return errors.New("checkpoint beyond end of file")
	}
	if err := p.h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
		return err
	}
	p.pos = pos
	return nil
}

// checkpoint returns the hash state and the offset it covers.
func (p *prefixHasher) checkpoint() ([]byte, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, err := p.h.(encoding.BinaryMarshaler).MarshalBinary()
	return state, p.pos, err
}

func (p *prefixHasher) ReadAt(b []byte, off int64) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	n := 0
	for n < len(b) {
		at := off + int64(n)
		if at >= p.size {
			return n, io.EOF
		}
		if at < p.start || at >= p.end {
			if err := p.fill(at); err != nil {
				return n, err
			}
		}
		n += copy(b[n:], p.buf[at-p.start:p.end-p.start])
	}
	return n, nil
}

// fill loads the window starting at off and extends the hash if it touches pos.
func (p *prefixHasher) fill(off int64) error {
	want := min(int64(len(p.buf)), p.size-off)
	m, err := p.f.ReadAt(p.buf[:want], off)
	if p.count != nil && m > 0 {
		p.count(int64(m))
	}
	if int64(m) < want {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF // the file shrank while being read
		}
		p.start, p.end = 0, 0
		if p.ioErr == nil {
			p.ioErr = err
		}
		return err
	}
	p.start, p.end = off, off+int64(m)
	if off <= p.pos && p.pos < p.end {
		p.h.Write(p.buf[p.pos-off : m])
		p.pos = p.end
	}
	return nil
}

// readErr returns the first error reading the file itself, so I/O problems
// (e.g. the NAS going away) are not mistaken for a damaged archive.
func (p *prefixHasher) readErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ioErr
}

// finish hashes the rest of the file and returns the hash of the whole file.
func (p *prefixHasher) finish(ctx interface{ Err() error }) ([32]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var sum [32]byte
	for p.pos < p.size {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		if p.pos < p.start || p.pos >= p.end {
			if err := p.fill(p.pos); err != nil {
				return sum, err
			}
			continue // fill extended the hash
		}
		p.h.Write(p.buf[p.pos-p.start : p.end-p.start])
		p.pos = p.end
	}
	copy(sum[:], p.h.Sum(nil))
	return sum, nil
}
