package scanner

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"modelkeeper/internal/archive"
	"os"

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

// readArchive reads every member completely, hashing it (F11) and thereby
// verifying its checksum (F7). Members are streamed and never written to disk (D8).
// countBytes is called with the number of decompressed bytes read.
func readArchive(ctx context.Context, file, ext string, data []byte, countBytes func(int64)) archiveResult {
	it, err := archive.Open(file, ext, data)
	if err != nil {
		return classify(err, nil)
	}
	defer it.Close()

	var members []archiveMember
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return archiveResult{Status: "", Detail: err.Error(), Members: members}
		}
		hdr, r, err := it.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return classify(err, members)
		}
		if hdr.IsDir {
			continue
		}
		if hdr.Encrypted {
			return classify(archive.ErrEncrypted, members)
		}
		h := sha256.New()
		var w io.Writer = h
		c := crc32.NewIEEE()
		if hdr.CheckCRC {
			w = io.MultiWriter(h, c)
		}
		n, err := io.CopyBuffer(w, &countingReader{r: r, ctx: ctx, add: countBytes}, buf)
		if err != nil {
			return classify(fmt.Errorf("%s: %w", hdr.Name, err), members)
		}
		if hdr.CheckCRC && c.Sum32() != hdr.CRC32 {
			return classify(fmt.Errorf("%s: %w", hdr.Name, errCRC), members)
		}
		m := archiveMember{Path: archive.CleanPath(hdr.Name), Size: n}
		if !hdr.Modified.IsZero() {
			m.Mtime = hdr.Modified.UnixNano()
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
	case errors.Is(err, archive.ErrEncrypted),
		errors.Is(err, rardecode.ErrArchiveEncrypted),
		errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		res.Status, res.ErrKey = archiveEncrypted, "scan.archive.encrypted"
	case errors.Is(err, archive.ErrUnsupported),
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
