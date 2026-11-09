package thumbs

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modelkeeper/internal/db"
)

// cubeSTL returns a binary STL of a unit cube (12 triangles).
func cubeSTL() []byte {
	v := [8][3]float32{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}, {0, 0, 1}, {1, 0, 1}, {1, 1, 1}, {0, 1, 1}}
	faces := [12][3]int{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4},
		{1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}}
	var buf bytes.Buffer
	buf.Write(make([]byte, 80))
	binary.Write(&buf, binary.LittleEndian, uint32(len(faces)))
	for _, f := range faces {
		binary.Write(&buf, binary.LittleEndian, [3]float32{})
		for _, i := range f {
			binary.Write(&buf, binary.LittleEndian, v[i])
		}
		buf.Write([]byte{0, 0})
	}
	return buf.Bytes()
}

func asciiSTL() []byte {
	return []byte("solid t\nfacet normal 0 0 1\nouter loop\nvertex 0 0 0\nvertex 10 0 0\nvertex 0 10 5\nendloop\nendfacet\nendsolid t\n")
}

func distinctColors(img image.Image) int {
	seen := map[color.RGBA]bool{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := img.At(x, y).RGBA()
			seen[color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 255}] = true
		}
	}
	return len(seen)
}

func TestRenderSTL(t *testing.T) {
	for name, data := range map[string][]byte{"binary": cubeSTL(), "ascii": asciiSTL()} {
		img, err := renderSTL(data, 128)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if img.Bounds().Dx() != 128 {
			t.Errorf("%s: size %v", name, img.Bounds())
		}
		// Background plus several shades: the model was drawn.
		if n := distinctColors(img); n < 3 {
			t.Errorf("%s: only %d colors, model not drawn", name, n)
		}
		// Center pixel is covered by the cube.
		if name == "binary" {
			r, g, b, _ := img.At(64, 64).RGBA()
			if uint8(r>>8) == background.R && uint8(g>>8) == background.G && uint8(b>>8) == background.B {
				t.Errorf("center pixel is background")
			}
		}
	}
	if _, err := renderSTL([]byte("solid empty\nendsolid\n"), 64); err == nil {
		t.Error("empty STL rendered")
	}
	nan := cubeSTL()
	binary.LittleEndian.PutUint32(nan[84+12:], math.Float32bits(float32(math.NaN())))
	if _, err := renderSTL(nan, 64); err == nil {
		t.Error("NaN coordinates not rejected")
	}
}

func pngBytes(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func zipBytes(files map[string][]byte) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for n, d := range files {
		f, _ := w.Create(n)
		f.Write(d)
	}
	w.Close()
	return buf.Bytes()
}

func TestEmbedded3MF(t *testing.T) {
	data := zipBytes(map[string][]byte{
		"3D/3dmodel.model":         []byte("<model/>"),
		"Metadata/thumbnail.png":   pngBytes(20, 20, color.RGBA{255, 0, 0, 255}),
		"Metadata/plate_small.png": pngBytes(4, 4, color.RGBA{0, 255, 0, 255}),
	})
	img, err := embedded3MF(data)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 20 {
		t.Errorf("picked %v, want the largest preview", img.Bounds())
	}
	if _, err := embedded3MF(zipBytes(map[string][]byte{"3D/3dmodel.model": []byte("x")})); err == nil {
		t.Error("3MF without preview accepted")
	}
}

type fixture struct {
	t    *testing.T
	db   *sql.DB
	dir  string // root folder on disk
	st   *Store
	next int64
}

func newFixture(t *testing.T) *fixture {
	conn, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	dir := t.TempDir()
	conn.Exec(`INSERT INTO root (id, path, kind, created_at) VALUES (1, ?, 'local', 0)`, dir)
	thumbDir := t.TempDir()
	return &fixture{t: t, db: conn, dir: dir, st: &Store{DB: conn, Dir: thumbDir}}
}

// model creates a model and returns its id.
func (f *fixture) model(name string) int64 {
	f.next++
	r, err := f.db.Exec(`INSERT INTO model (uid, name, created_at, updated_at) VALUES (?, ?, 0, 0)`,
		fmt.Sprintf("uid-%d", f.next), name)
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	f.db.Exec(`INSERT INTO model_anchor (model_id, root_id, path, kind) VALUES (?, 1, ?, 'folder')`, id, name)
	return id
}

// file writes a file below the root and records it as location of the model.
func (f *fixture) file(modelID int64, rel string, data []byte) int64 {
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, data, 0o644)
	return f.loc(modelID, rel, 0, len(data))
}

func (f *fixture) loc(modelID int64, rel string, archiveID int64, size int) int64 {
	ext := strings.TrimPrefix(filepath.Ext(rel), ".")
	var arch any
	if archiveID != 0 {
		arch = archiveID
	}
	r, err := f.db.Exec(`INSERT INTO location (root_id, archive_id, path, ext, category, size, mtime, model_id, first_seen_at)
		VALUES (1, ?, ?, ?, 'model', ?, 0, ?, 0)`, arch, rel, ext, size, modelID)
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	return id
}

func (f *fixture) thumb(modelID int64) (source, file string) {
	var s, fl sql.NullString
	f.db.QueryRow(`SELECT thumb_source, thumb_file FROM model WHERE id = ?`, modelID).Scan(&s, &fl)
	return s.String, fl.String
}

func TestGeneratePriorityAndManual(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Image beats 3MF beats STL; "render" in the name beats a larger plain image.
	m := f.model("Orc")
	f.file(m, "Orc/orc.stl", cubeSTL())
	f.file(m, "Orc/orc.3mf", zipBytes(map[string][]byte{"Metadata/thumbnail.png": pngBytes(8, 8, color.RGBA{0, 0, 255, 255})}))
	f.file(m, "Orc/photo.png", pngBytes(900, 300, color.RGBA{0, 255, 0, 255}))
	f.file(m, "Orc/orc_render.jpg", func() []byte {
		var b bytes.Buffer
		jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 50, 50)), nil)
		return b.Bytes()
	}())
	cands, err := Candidates(ctx, f.db, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 4 || cands[0].Label != "orc_render.jpg" || cands[3].Kind != KindRendered {
		t.Fatalf("candidates = %+v", cands)
	}
	if err := f.st.Generate(ctx, m); err != nil {
		t.Fatal(err)
	}
	if src, file := f.thumb(m); src != KindImage || file != "uid-1.jpg" {
		t.Errorf("thumb = %q %q", src, file)
	}

	// STL only: rendered; stored image fits Size and is a JPEG.
	s := f.model("Cube")
	f.file(s, "Cube/cube_sup.stl", cubeSTL())
	if err := f.st.Generate(ctx, s); err != nil {
		t.Fatal(err)
	}
	src, file := f.thumb(s)
	if src != KindRendered {
		t.Fatalf("source = %q", src)
	}
	data, _ := os.ReadFile(filepath.Join(f.st.Dir, file))
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != Size || cfg.Height != Size {
		t.Errorf("stored jpeg: %+v, %v", cfg, err)
	}

	// Manual thumbnail (I4) is never replaced by generation.
	wide := filepath.Join(t.TempDir(), "mine.png")
	os.WriteFile(wide, pngBytes(1000, 500, color.RGBA{9, 9, 9, 255}), 0o644)
	if err := f.st.SetFromFile(ctx, s, wide); err != nil {
		t.Fatal(err)
	}
	f.st.Generate(ctx, s)
	if src, _ := f.thumb(s); src != "manual" {
		t.Errorf("manual thumbnail replaced: %q", src)
	}
	data, _ = os.ReadFile(filepath.Join(f.st.Dir, file))
	cfg, _ = jpeg.DecodeConfig(bytes.NewReader(data))
	if cfg.Width != Size || cfg.Height != Size/2 {
		t.Errorf("aspect not kept: %dx%d", cfg.Width, cfg.Height)
	}
	// Reset goes back to automatic.
	if err := f.st.Reset(ctx, s); err != nil {
		t.Fatal(err)
	}
	if src, _ := f.thumb(s); src != KindRendered {
		t.Errorf("after reset: %q", src)
	}

	// Nothing usable: marked as tried, not pending anymore.
	e := f.model("Empty")
	f.file(e, "Empty/readme.txt", []byte("hi"))
	if err := f.st.Generate(ctx, e); err != nil {
		t.Fatal(err)
	}
	pending, _ := Pending(ctx, f.db, 1)
	if len(pending) != 0 {
		t.Errorf("pending = %v", pending)
	}
}

func TestImageInsideArchive(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	m := f.model("Pack")
	z := zipBytes(map[string][]byte{"pack/cover.png": pngBytes(30, 30, color.RGBA{200, 10, 10, 255}), "pack/a.stl": cubeSTL()})
	arch := f.file(m, "Pack.zip", z)
	f.loc(m, "pack/cover.png", arch, 100)
	f.loc(m, "pack/a.stl", arch, len(cubeSTL()))
	if err := f.st.Generate(ctx, m); err != nil {
		t.Fatal(err)
	}
	if src, _ := f.thumb(m); src != KindImage {
		t.Errorf("source = %q", src)
	}
}
