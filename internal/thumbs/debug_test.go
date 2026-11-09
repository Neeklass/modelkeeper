package thumbs

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"strings"
	"testing"
	"time"
)

// TestDebugRender: MODELKEEPER_DEBUG_STL=<file> MODELKEEPER_DEBUG_OUT=<jpg>
func TestDebugRender(t *testing.T) {
	in, out := os.Getenv("MODELKEEPER_DEBUG_STL"), os.Getenv("MODELKEEPER_DEBUG_OUT")
	if in == "" {
		t.Skip()
	}
	data, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	img, err := renderSTL(data, Size)
	if err != nil {
		t.Fatal(err)
	}
	jpg, err := encode(img)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d bytes rendered in %v", len(data), time.Since(start))
	os.WriteFile(out, jpg, 0o644)
	if os.Getenv("MODELKEEPER_DEBUG_ALL_AXES") != "" {
		sheet := image.NewRGBA(image.Rect(0, 0, 6*192, 192))
		for up := 0; up < 6; up++ {
			tris, _ := parseSTL(data)
			small := renderTris(tris, 192, up)
			draw.Draw(sheet, image.Rect(up*192, 0, up*192+192, 192), small, image.Point{}, draw.Src)
		}
		var buf bytes.Buffer
		jpeg.Encode(&buf, sheet, nil)
		os.WriteFile(out+".axes.jpg", buf.Bytes(), 0o644)
	}
}

// TestDebugSheet: MODELKEEPER_DEBUG_STLS=<file>|<file>... renders auto-oriented thumbnails side by side.
func TestDebugSheet(t *testing.T) {
	list, out := os.Getenv("MODELKEEPER_DEBUG_STLS"), os.Getenv("MODELKEEPER_DEBUG_OUT")
	if list == "" {
		t.Skip()
	}
	files := strings.Split(list, "|")
	sheet := image.NewRGBA(image.Rect(0, 0, len(files)*192, 192))
	for i, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tris, _ := parseSTL(data)
		sc := axisScores(tris)
		up := upAxis(tris)
		img := renderTris(tris, 192, up)
		draw.Draw(sheet, image.Rect(i*192, 0, i*192+192, 192), img, image.Point{}, draw.Src)
		t.Logf("%d: up=%d scores=%.2f %s", i, up, sc, f[strings.LastIndex(f, `\`)+1:])
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, sheet, nil)
	os.WriteFile(out, buf.Bytes(), 0o644)
}
