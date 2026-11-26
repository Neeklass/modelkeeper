package thumbs

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// withOrientation puts an EXIF APP1 segment with orientation o into a JPEG.
func withOrientation(t *testing.T, img image.Image, o uint16) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	j := buf.Bytes()
	tiff := []byte("II*\x00\x08\x00\x00\x00")
	ifd := make([]byte, 2+12+4)
	binary.LittleEndian.PutUint16(ifd, 1)
	binary.LittleEndian.PutUint16(ifd[2:], 0x0112)
	binary.LittleEndian.PutUint16(ifd[4:], 3)
	binary.LittleEndian.PutUint32(ifd[6:], 1)
	binary.LittleEndian.PutUint16(ifd[10:], o)
	seg := append([]byte("Exif\x00\x00"), append(tiff, ifd...)...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(seg)+2))
	out := append([]byte{}, j[:2]...)
	out = append(out, app1...)
	out = append(out, seg...)
	return append(out, j[2:]...)
}

func TestPhotoUpright(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 20)) // landscape, left half red
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x < 20 {
				c = color.RGBA{255, 0, 0, 255}
			}
			src.Set(x, y, c)
		}
	}
	raw := withOrientation(t, src, 6) // camera held upright: turn 90° clockwise
	if o := exifOrientation(raw); o != 6 {
		t.Fatalf("orientation %d", o)
	}
	file := filepath.Join(t.TempDir(), "p.jpg")
	os.WriteFile(file, raw, 0o644)
	data, w, h, err := Photo(file)
	if err != nil {
		t.Fatal(err)
	}
	if w != 20 || h != 40 {
		t.Fatalf("size %dx%d", w, h)
	}
	img, _ := DecodeBytes(data)
	r, _, _, _ := img.At(10, 5).RGBA() // after turning clockwise the red half is on top
	if r>>8 < 180 {
		t.Errorf("not turned: top is %v", img.At(10, 5))
	}
}
