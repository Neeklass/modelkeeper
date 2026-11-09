// Package archive reads zip, 7z and rar archives in place; nothing is ever
// extracted to disk (D8).
package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// Iter walks the files of an archive: Next returns the next entry and a
// reader for its content, or io.EOF when done.
type Iter interface {
	Next() (hdr Header, r io.Reader, err error)
	Close() error
}

// Header describes one archive entry.
type Header struct {
	Name      string
	Size      int64
	Modified  time.Time
	IsDir     bool
	Encrypted bool
	CRC32     uint32 // expected CRC when the library does not verify it (7z), 0 = none
	CheckCRC  bool
}

// ErrEncrypted marks archives or members that need a password.
var ErrEncrypted = errors.New("encrypted")

// ErrUnsupported marks formats or compression methods we cannot read.
var ErrUnsupported = errors.New("unsupported")

// laterRarVolume matches the second and following volumes of a multi-volume
// RAR (name.part2.rar ...). They are read through the first volume.
var laterRarVolume = regexp.MustCompile(`(?i)\.part0*([2-9]|[1-9]\d+)\.rar$`)

// Open opens an archive by extension. data, if not nil, is the full
// archive content already in memory (avoids reading it a second time).
func Open(file, ext string, data []byte) (Iter, error) {
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
			return nil, fmt.Errorf("%w: later volume of a multi-volume archive", ErrUnsupported)
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
	return nil, fmt.Errorf("%w: .%s", ErrUnsupported, ext)
}

// OpenReaderAt opens a zip or 7z archive through r (e.g. a reader that
// hashes the file while it is read). Other formats need Open.
func OpenReaderAt(ext string, r io.ReaderAt, size int64) (Iter, error) {
	switch ext {
	case "zip":
		zr, err := zip.NewReader(r, size)
		if err != nil {
			return nil, err
		}
		return &zipIter{files: zr.File}, nil
	case "7z":
		sr, err := sevenzip.NewReader(r, size)
		if err != nil {
			return nil, wrap7z(err)
		}
		return &sevenIter{files: sr.File}, nil
	}
	return nil, fmt.Errorf("%w: .%s via ReaderAt", ErrUnsupported, ext)
}

// Skipper is implemented by iterators that can skip entries without reading
// them (zip: entries are independent). Used to resume an interrupted read.
type Skipper interface {
	// Pos is the number of entries returned so far.
	Pos() int
	// SkipTo continues with entry i (i >= Pos()).
	SkipTo(i int) error
}

func wrap7z(err error) error {
	var re *sevenzip.ReadError
	if errors.As(err, &re) && re.Encrypted {
		return fmt.Errorf("%w: %v", ErrEncrypted, err)
	}
	return err
}

// CleanPath normalizes a member name to a relative '/'-path.
func CleanPath(name string) string {
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

func (z *zipIter) Next() (Header, io.Reader, error) {
	if z.cur != nil {
		z.cur.Close()
		z.cur = nil
	}
	if z.i >= len(z.files) {
		return Header{}, nil, io.EOF
	}
	f := z.files[z.i]
	z.i++
	hdr := Header{
		Name:      f.Name,
		Size:      int64(f.UncompressedSize64),
		Modified:  f.Modified,
		IsDir:     strings.HasSuffix(f.Name, "/") || f.FileInfo().IsDir(),
		Encrypted: f.Flags&0x1 != 0,
	}
	if hdr.IsDir || hdr.Encrypted {
		return hdr, nil, nil
	}
	rc, err := f.Open() // verifies CRC32 at EOF (zip.ErrChecksum)
	if err != nil {
		return hdr, nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	z.cur = rc
	return hdr, rc, nil
}

func (z *zipIter) Pos() int { return z.i }

func (z *zipIter) SkipTo(i int) error {
	if i < z.i || i > len(z.files) {
		return fmt.Errorf("cannot skip to entry %d of %d", i, len(z.files))
	}
	if z.cur != nil {
		z.cur.Close()
		z.cur = nil
	}
	z.i = i
	return nil
}

func (z *zipIter) Close() error {
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

func (s *sevenIter) Next() (Header, io.Reader, error) {
	if s.cur != nil {
		s.cur.Close()
		s.cur = nil
	}
	if s.i >= len(s.files) {
		return Header{}, nil, io.EOF
	}
	f := s.files[s.i]
	s.i++
	hdr := Header{
		Name:     f.Name,
		Size:     int64(f.UncompressedSize),
		Modified: f.Modified,
		IsDir:    f.FileInfo().IsDir(),
		CRC32:    f.CRC32,
		// sevenzip does not verify member CRCs itself.
		CheckCRC: f.CRC32 != 0,
	}
	if hdr.IsDir {
		return hdr, nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return hdr, nil, fmt.Errorf("%s: %w", f.Name, wrap7z(err))
	}
	s.cur = rc
	return hdr, &sevenReader{rc: rc}, nil
}

func (s *sevenIter) Close() error {
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

func (r *rarIter) Next() (Header, io.Reader, error) {
	h, err := r.r.Next() // verifies the previous file's checksum
	if err != nil {
		return Header{}, nil, err
	}
	hdr := Header{
		Name:      h.Name,
		Size:      h.UnPackedSize,
		Modified:  h.ModificationTime,
		IsDir:     h.IsDir,
		Encrypted: h.Encrypted,
	}
	return hdr, r.r, nil
}

func (r *rarIter) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// ErrTooLarge is returned by ReadMember for members above the size limit.
var ErrTooLarge = errors.New("archive member too large")

// ReadMember returns the content of one member (matched by CleanPath name),
// held in memory only. Members larger than maxSize are refused.
func ReadMember(file, ext, name string, maxSize int64) ([]byte, error) {
	it, err := Open(file, ext, nil)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	for {
		h, r, err := it.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("%s: not found in archive", name)
			}
			return nil, err
		}
		if h.IsDir || CleanPath(h.Name) != name {
			continue
		}
		if h.Encrypted {
			return nil, ErrEncrypted
		}
		if h.Size > maxSize {
			return nil, ErrTooLarge
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, io.LimitReader(r, maxSize+1)); err != nil {
			return nil, err
		}
		if int64(buf.Len()) > maxSize {
			return nil, ErrTooLarge
		}
		return buf.Bytes(), nil
	}
}
