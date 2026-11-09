// Package thumbs generates and stores model thumbnails (concept I).
//
// Source priority (I1): 1. set manually, 2. an image in the model folder or
// archive, 3. a preview embedded in a .3mf file, 4. rendered from an STL.
// Thumbnails are generated once and cached as data/thumbnails/<model uid>.jpg (I3).
// Manually set thumbnails are never replaced automatically (I4).
package thumbs

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // register decoders
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"modelkeeper/internal/archive"
)

// Size is the longest edge of a stored thumbnail in pixels.
const Size = 384

// Read limits: larger sources are skipped.
const (
	maxImageBytes = 40 << 20
	maxModelBytes = 300 << 20
)

// Candidate kinds (model.thumb_source values except "manual").
const (
	KindImage    = "image"
	KindEmbedded = "embedded"
	KindRendered = "rendered"
)

// Candidate is a possible thumbnail source of a model.
type Candidate struct {
	LocationID int64  `json:"locationId"`
	Kind       string `json:"kind"`
	Label      string `json:"label"` // file name, for the picker
	Size       int64  `json:"size"`

	rootPath, path, archivePath, ext string
	score                            int
}

var imageExts = map[string]bool{"jpg": true, "jpeg": true, "png": true, "webp": true}

// preferred words in image names: product renders usually carry them.
var imageHints = []string{"preview", "render", "cover", "thumb", "main", "front", "box", "promo"}

// Candidates lists the thumbnail sources of a model, best first.
func Candidates(ctx context.Context, db *sql.DB, modelID int64) ([]Candidate, error) {
	rows, err := db.QueryContext(ctx, `SELECT l.id, r.path, l.path, coalesce(a.path, ''), l.ext, l.size
		FROM location l JOIN root r ON r.id = l.root_id LEFT JOIN location a ON a.id = l.archive_id
		WHERE l.model_id = ? AND l.present = 1 AND r.status <> 'offline'
		  AND l.ext IN ('jpg', 'jpeg', 'png', 'webp', '3mf', 'stl')`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.LocationID, &c.rootPath, &c.path, &c.archivePath, &c.ext, &c.Size); err != nil {
			return nil, err
		}
		c.Label = path.Base(c.path)
		name := strings.ToLower(c.Label)
		switch {
		case imageExts[c.ext]:
			if c.Size > maxImageBytes {
				continue
			}
			c.Kind, c.score = KindImage, 3000
			for _, h := range imageHints {
				if strings.Contains(name, h) {
					c.score += 500
					break
				}
			}
		case c.ext == "3mf":
			if c.Size > maxModelBytes {
				continue
			}
			c.Kind, c.score = KindEmbedded, 2000
		case c.ext == "stl":
			if c.Size > maxModelBytes {
				continue
			}
			// Supports clutter the picture: prefer files without them.
			c.Kind, c.score = KindRendered, 1000
			lower := strings.ToLower(c.path)
			stem := strings.TrimSuffix(strings.ToLower(c.Label), ".stl")
			if strings.Contains(lower, "sup") || strings.HasSuffix(stem, "_ps") || strings.HasSuffix(stem, "-ps") {
				c.score -= 500
			}
		}
		// Within a kind, larger files are usually the main image / main body.
		c.score += int(min(c.Size>>16, 400))
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out, rows.Err()
}

// Store writes thumbnails into the thumbnail folder.
type Store struct {
	DB  *sql.DB
	Dir string // data/thumbnails
}

// File returns the file name of a model's thumbnail.
func File(uid string) string { return uid + ".jpg" }

// errNoSource: the model has nothing a thumbnail can be made from.
var errNoSource = errors.New("no thumbnail source")

// Generate creates the automatic thumbnail of a model, trying the candidates
// in priority order. A manual thumbnail is kept.
func (s *Store) Generate(ctx context.Context, modelID int64) error {
	var uid string
	var source sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT uid, thumb_source FROM model WHERE id = ?`, modelID).Scan(&uid, &source); err != nil {
		return err
	}
	if source.String == "manual" {
		return nil
	}
	cands, err := Candidates(ctx, s.DB, modelID)
	if err != nil {
		return err
	}
	var lastErr error = errNoSource
	for i, c := range cands {
		if i >= 3 || ctx.Err() != nil {
			break
		}
		img, err := load(c)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", c.Label, err)
			continue
		}
		return s.save(ctx, modelID, uid, img, c.Kind)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE model SET thumb_tried_at = ? WHERE id = ?`, time.Now().UnixMilli(), modelID); err != nil {
		return err
	}
	if errors.Is(lastErr, errNoSource) {
		return nil
	}
	return lastErr
}

// SetFromCandidate makes one of the model's files its thumbnail (manual, I4).
func (s *Store) SetFromCandidate(ctx context.Context, modelID, locationID int64) error {
	cands, err := Candidates(ctx, s.DB, modelID)
	if err != nil {
		return err
	}
	for _, c := range cands {
		if c.LocationID == locationID {
			img, err := load(c)
			if err != nil {
				return err
			}
			return s.saveFor(ctx, modelID, img, "manual")
		}
	}
	return errors.New("not a thumbnail candidate of this model")
}

// SetFromFile makes any image file the thumbnail (manual, I4). The file is
// read, scaled and stored; the original is not touched (J1).
func (s *Store) SetFromFile(ctx context.Context, modelID int64, file string) error {
	data, err := readLimited(file, maxImageBytes)
	if err != nil {
		return err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return s.saveFor(ctx, modelID, img, "manual")
}

// SetFromImage stores an already decoded image as manual thumbnail
// (e.g. the current view of the 3D preview).
func (s *Store) SetFromImage(ctx context.Context, modelID int64, img image.Image) error {
	return s.saveFor(ctx, modelID, img, "manual")
}

// Reset drops a manual thumbnail and lets generation choose again.
func (s *Store) Reset(ctx context.Context, modelID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE model SET thumb_source = NULL, thumb_file = NULL, thumb_tried_at = NULL
		WHERE id = ?`, modelID)
	if err != nil {
		return err
	}
	return s.Generate(ctx, modelID)
}

func (s *Store) saveFor(ctx context.Context, modelID int64, img image.Image, kind string) error {
	var uid string
	if err := s.DB.QueryRowContext(ctx, `SELECT uid FROM model WHERE id = ?`, modelID).Scan(&uid); err != nil {
		return err
	}
	return s.save(ctx, modelID, uid, img, kind)
}

func (s *Store) save(ctx context.Context, modelID int64, uid string, img image.Image, kind string) error {
	data, err := encode(img)
	if err != nil {
		return err
	}
	name := File(uid)
	tmp, err := os.CreateTemp(s.Dir, ".thumb-*.jpg")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), filepath.Join(s.Dir, name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	now := time.Now().UnixMilli()
	_, err = s.DB.ExecContext(ctx, `UPDATE model SET thumb_source = ?, thumb_file = ?, thumb_tried_at = ?, updated_at = ?
		WHERE id = ?`, kind, name, now, now, modelID)
	return err
}

// load produces the image for a candidate.
func load(c Candidate) (image.Image, error) {
	data, err := c.read()
	if err != nil {
		return nil, err
	}
	switch c.Kind {
	case KindImage:
		img, _, err := image.Decode(bytes.NewReader(data))
		return img, err
	case KindEmbedded:
		return embedded3MF(data)
	case KindRendered:
		return renderSTL(data, Size)
	}
	return nil, errNoSource
}

// read returns the content of the candidate's file (inside an archive: held in memory only, D8).
func (c Candidate) read() ([]byte, error) {
	limit := int64(maxModelBytes)
	if c.Kind == KindImage {
		limit = maxImageBytes
	}
	if c.archivePath != "" {
		arch := filepath.Join(c.rootPath, filepath.FromSlash(c.archivePath))
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(c.archivePath), "."))
		return archive.ReadMember(arch, ext, c.path, limit)
	}
	return readLimited(filepath.Join(c.rootPath, filepath.FromSlash(c.path)), limit)
}

func readLimited(file string, limit int64) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, archive.ErrTooLarge
	}
	return data, nil
}

// embedded3MF returns the largest thumbnail image stored inside a 3MF (zip) file.
func embedded3MF(data []byte) (image.Image, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	// Pick the preview with the most pixels (byte size says little for PNGs).
	var best *zip.File
	bestArea := -1
	for _, f := range zr.File {
		n := strings.ToLower(f.Name)
		if !(strings.HasSuffix(n, ".png") || strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")) ||
			!(strings.Contains(n, "thumbnail") || strings.HasPrefix(n, "metadata/")) || f.UncompressedSize64 > maxImageBytes {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		cfg, _, err := image.DecodeConfig(rc)
		rc.Close()
		if err == nil && cfg.Width*cfg.Height > bestArea {
			best, bestArea = f, cfg.Width*cfg.Height
		}
	}
	if best == nil {
		return nil, errNoSource
	}
	rc, err := best.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	img, _, err := image.Decode(io.LimitReader(rc, maxImageBytes))
	return img, err
}

// encode scales the image to fit Size x Size and writes a JPEG; transparent
// areas get the render background.
func encode(img image.Image) ([]byte, error) {
	b := img.Bounds()
	if b.Empty() {
		return nil, errors.New("empty image")
	}
	w, h := b.Dx(), b.Dy()
	if w > Size || h > Size {
		if w >= h {
			w, h = Size, max(1, h*Size/b.Dx())
		} else {
			w, h = max(1, w*Size/b.Dy()), Size
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{R: background.R, G: background.G, B: background.B, A: 255}),
		image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Pending returns models of a root that have no thumbnail and were not tried yet.
func Pending(ctx context.Context, db *sql.DB, rootID int64) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.id FROM model m
		WHERE m.thumb_source IS NULL AND m.thumb_tried_at IS NULL
		  AND EXISTS (SELECT 1 FROM model_anchor a WHERE a.model_id = m.id AND a.root_id = ?)
		ORDER BY m.name COLLATE NOCASE`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
