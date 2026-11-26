package thumbs

import (
	"bytes"
	"encoding/binary"
	"image"
)

// PhotoSize is the longest edge of a stored photo (print log, L1).
const PhotoSize = 1600

// MaxPhotoBytes: larger photo files are refused.
const MaxPhotoBytes = maxImageBytes

// Photo reads an image file (JPEG, PNG, WebP), turns it upright (the
// orientation a phone camera writes into the JPEG) and returns it as JPEG of
// at most PhotoSize pixels. The file itself is not changed.
func Photo(file string) (data []byte, w, h int, err error) {
	raw, err := readLimited(file, MaxPhotoBytes)
	if err != nil {
		return nil, 0, 0, err
	}
	img, err := DecodeUpright(raw)
	if err != nil {
		return nil, 0, 0, err
	}
	return encodeMax(img, PhotoSize, 85)
}

// DecodeUpright decodes an image and applies its EXIF orientation.
func DecodeUpright(raw []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return orientImage(img, exifOrientation(raw)), nil
}

// DecodeBytes decodes a stored JPEG (e.g. a photo used as thumbnail).
func DecodeBytes(raw []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

// exifOrientation returns the EXIF orientation (1..8) of a JPEG, 1 if none.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan / end: no EXIF before it
			return 1
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+n]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off+2 > len(t) {
		return 1
	}
	count := int(bo.Uint16(t[off:]))
	for k := 0; k < count; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			v := int(bo.Uint16(t[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orientImage turns img according to EXIF orientation o.
func orientImage(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
