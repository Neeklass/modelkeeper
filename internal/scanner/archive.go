package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// Archive outcome (location.archive_status).
const (
	archiveOK          = "ok"
	archiveBroken      = "broken"
	archiveEncrypted   = "encrypted"
	archiveUnsupported = "unsupported"
)

// archiveMember is one file read from an archive.
type archiveMember struct {
	Path   string // '/'-separated, relative to the archive
	Size   int64
	Mtime  int64 // unix ns, 0 if unknown
	SHA256 [32]byte
}

// archiveResult is the outcome of reading an archive completely.
type archiveResult struct {
	Status  string
	ErrKey  string // message key when Status != ok
	Detail  string
	Members []archiveMember // successfully read members (also for broken archives)
}

// memberOpener abstracts the formats: next returns the next file entry and a
// reader for its content, or io.EOF when done.
type memberIter interface {
	next() (hdr memberHeader, r io.Reader, err error)
	close() error
}

type memberHeader struct {
	name      string
	size      int64
	modified  time.Time
	isDir     bool
	encrypted bool
	crc32     uint32 // expected CRC when the library does not verify it (7z), 0 = none
	checkCRC  bool
}

// errEncrypted marks archives or members that need a password.
var errEncrypted = errors.New("encrypted")

// errUnsupported marks formats or compression methods we cannot read.
var errUnsupported = errors.New("unsupported")

// laterRarVolume matches the second and following volumes of a multi-volume
// RAR (name.part2.rar ...). They are read through the first volume.
var laterRarVolume = regexp.MustCompile(`(?i)\.part0*([2-9]|[1-9]\d+)\.rar$`)

// openArchive opens an archive by extension. data, if not nil, is the full
// archive content already in memory (avoids reading it a second time).
func openArchive(file, ext string, data []byte) (memberIter, error) {
	switch ext {
	case "zip":
		var zr *zip.Reader
		var closer io.Closer
		if data != nil {
			r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return nil, err
			}
			zr = r
		} else {
			rc, err := zip.OpenReader(file)
			if err != nil {
				return nil, err
			}
			zr, closer = &rc.Reader, rc
		}
		return &zipIter{files: zr.File, closer: closer}, nil
	case "7z":
		if data != nil {
			r, err := sevenzip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return nil, wrap7z(err)
			}
			return &sevenIter{files: r.File}, nil
		}
		rc, err := sevenzip.OpenReader(file)
		if err != nil {
			return nil, wrap7z(err)
		}
		return &sevenIter{files: rc.File, closer: rc}, nil
	case "rar":
		if laterRarVolume.MatchString(file) {
			return nil, fmt.Errorf("%w: later volume of a multi-volume archive", errUnsupported)
		}
		if data != nil {
			r, err := rardecode.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			return &rarIter{r: r}, nil
		}
		rc, err := rardecode.OpenReader(file)
		if err != nil {
			return nil, err
		}
		return &rarIter{r: &rc.Reader, closer: rc}, nil
	}
	return nil, fmt.Errorf("%w: .%s", errUnsupported, ext)
}

func wrap7z(err error) error {
	var re *sevenzip.ReadError
	if errors.As(err, &re) && re.Encrypted {
		return fmt.Errorf("%w: %v", errEncrypted, err)
	}
	return err
}

// readArchive reads every member completely, hashing it (F11) and thereby
// verifying its checksum (F7). Members are streamed and never written to disk (D8).
// countBytes is called with the number of decompressed bytes read.
func readArchive(ctx context.Context, file, ext string, data []byte, countBytes func(int64)) archiveResult {
	it, err := openArchive(file, ext, data)
	if err != nil {
		return classify(err, nil)
	}
	defer it.close()

	var members []archiveMember
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return archiveResult{Status: "", Detail: err.Error(), Members: members}
		}
		hdr, r, err := it.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return classify(err, members)
		}
		if hdr.isDir {
			continue
		}
		if hdr.encrypted {
			return classify(errEncrypted, members)
		}
		h := sha256.New()
		var w io.Writer = h
		c := crc32.NewIEEE()
		if hdr.checkCRC {
			w = io.MultiWriter(h, c)
		}
		n, err := io.CopyBuffer(w, &countingReader{r: r, ctx: ctx, add: countBytes}, buf)
		if err != nil {
			return classify(fmt.Errorf("%s: %w", hdr.name, err), members)
		}
		if hdr.checkCRC && c.Sum32() != hdr.crc32 {
			return classify(fmt.Errorf("%s: %w", hdr.name, errCRC), members)
		}
		m := archiveMember{Path: cleanMemberPath(hdr.name), Size: n}
		if !hdr.modified.IsZero() {
			m.Mtime = hdr.modified.UnixNano()
		}
		copy(m.SHA256[:], h.Sum(nil))
		if m.Path != "" {
			members = append(members, m)
		}
	}
	return archiveResult{Status: archiveOK, Members: members}
}

var errCRC = errors.New("checksum mismatch")

func classify(err error, members []archiveMember) archiveResult {
	res := archiveResult{Members: members, Detail: err.Error()}
	switch {
	case errors.Is(err, context.Canceled):
		res.Status = "" // caller handles cancellation
	case errors.Is(err, errEncrypted),
		errors.Is(err, rardecode.ErrArchiveEncrypted),
		errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		res.Status, res.ErrKey = archiveEncrypted, "scan.archive.encrypted"
	case errors.Is(err, errUnsupported),
		errors.Is(err, zip.ErrAlgorithm),
		errors.Is(err, rardecode.ErrUnsupportedDecoder),
		errors.Is(err, rardecode.ErrUnknownDecoder),
		errors.Is(err, rardecode.ErrUnknownVersion):
		res.Status, res.ErrKey = archiveUnsupported, "scan.archive.unsupported"
	default:
		res.Status, res.ErrKey = archiveBroken, "scan.archive.broken"
	}
	return res
}

// cleanMemberPath normalizes a member name to a relative '/'-path.
func cleanMemberPath(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = path.Clean("/" + name)
	return strings.TrimPrefix(name, "/")
}

// --- zip ---

type zipIter struct {
	files  []*zip.File
	i      int
	cur    io.ReadCloser
	closer io.Closer
}

func (z *zipIter) next() (memberHeader, io.Reader, error) {
	if z.cur != nil {
		z.cur.Close()
		z.cur = nil
	}
	if z.i >= len(z.files) {
		return memberHeader{}, nil, io.EOF
	}
	f := z.files[z.i]
	z.i++
	hdr := memberHeader{
		name:      f.Name,
		size:      int64(f.UncompressedSize64),
		modified:  f.Modified,
		isDir:     strings.HasSuffix(f.Name, "/") || f.FileInfo().IsDir(),
		encrypted: f.Flags&0x1 != 0,
	}
	if hdr.isDir || hdr.encrypted {
		return hdr, nil, nil
	}
	rc, err := f.Open() // verifies CRC32 at EOF (zip.ErrChecksum)
	if err != nil {
		return hdr, nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	z.cur = rc
	return hdr, rc, nil
}

func (z *zipIter) close() error {
	if z.cur != nil {
		z.cur.Close()
	}
	if z.closer != nil {
		return z.closer.Close()
	}
	return nil
}

// --- 7z ---

type sevenIter struct {
	files  []*sevenzip.File
	i      int
	cur    io.ReadCloser
	closer io.Closer
}

func (s *sevenIter) next() (memberHeader, io.Reader, error) {
	if s.cur != nil {
		s.cur.Close()
		s.cur = nil
	}
	if s.i >= len(s.files) {
		return memberHeader{}, nil, io.EOF
	}
	f := s.files[s.i]
	s.i++
	hdr := memberHeader{
		name:     f.Name,
		size:     int64(f.UncompressedSize),
		modified: f.Modified,
		isDir:    f.FileInfo().IsDir(),
		crc32:    f.CRC32,
		// sevenzip does not verify member CRCs itself.
		checkCRC: f.CRC32 != 0,
	}
	if hdr.isDir {
		return hdr, nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return hdr, nil, fmt.Errorf("%s: %w", f.Name, wrap7z(err))
	}
	s.cur = rc
	return hdr, &sevenReader{rc: rc}, nil
}

func (s *sevenIter) close() error {
	if s.cur != nil {
		s.cur.Close()
	}
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

type sevenReader struct{ rc io.Reader }

func (r *sevenReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = wrap7z(err)
	}
	return n, err
}

// --- rar ---

type rarIter struct {
	r      *rardecode.Reader
	closer io.Closer
}

func (r *rarIter) next() (memberHeader, io.Reader, error) {
	h, err := r.r.Next() // verifies the previous file's checksum
	if err != nil {
		return memberHeader{}, nil, err
	}
	hdr := memberHeader{
		name:      h.Name,
		size:      h.UnPackedSize,
		modified:  h.ModificationTime,
		isDir:     h.IsDir,
		encrypted: h.Encrypted,
	}
	return hdr, r.r, nil
}

func (r *rarIter) close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// --- helpers ---

// countingReader reports progress and aborts on context cancellation.
type countingReader struct {
	r   io.Reader
	ctx context.Context
	add func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	if n > 0 && c.add != nil {
		c.add(int64(n))
	}
	return n, err
}

// hashFile streams a file through SHA-256.
func hashFile(ctx context.Context, file string, buf []byte, countBytes func(int64)) ([32]byte, int64, error) {
	var sum [32]byte
	f, err := os.Open(file)
	if err != nil {
		return sum, 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, &countingReader{r: f, ctx: ctx, add: countBytes}, buf)
	if err != nil {
		return sum, n, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, n, nil
}
