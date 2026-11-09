package thumbs

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"
)

// Background of rendered thumbnails: a neutral mid-tone that works on light
// and dark UI themes.
var background = color.RGBA{R: 0x3a, G: 0x3c, B: 0x44, A: 0xff}

type vec struct{ x, y, z float64 }

func (a vec) sub(b vec) vec       { return vec{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec) cross(b vec) vec     { return vec{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x} }
func (a vec) dot(b vec) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec) scale(f float64) vec { return vec{a.x * f, a.y * f, a.z * f} }
func (a vec) norm() vec {
	l := math.Sqrt(a.dot(a))
	if l == 0 {
		return a
	}
	return a.scale(1 / l)
}

var errNoTriangles = errors.New("no triangles in model")

// parseSTL reads binary and ASCII STL files.
func parseSTL(data []byte) ([][3]vec, error) {
	if len(data) >= 84 {
		n := binary.LittleEndian.Uint32(data[80:84])
		if uint64(n)*50+84 == uint64(len(data)) {
			tris := make([][3]vec, n)
			for i := range tris {
				off := 84 + i*50 + 12 // skip normal
				for k := 0; k < 3; k++ {
					o := off + k*12
					tris[i][k] = vec{
						float64(math.Float32frombits(binary.LittleEndian.Uint32(data[o:]))),
						float64(math.Float32frombits(binary.LittleEndian.Uint32(data[o+4:]))),
						float64(math.Float32frombits(binary.LittleEndian.Uint32(data[o+8:]))),
					}
				}
			}
			return tris, nil
		}
	}
	// ASCII: "vertex x y z" lines, three per facet.
	var tris [][3]vec
	var cur [3]vec
	k := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 4 || f[0] != "vertex" {
			continue
		}
		var v [3]float64
		for i := 0; i < 3; i++ {
			x, err := strconv.ParseFloat(f[i+1], 64)
			if err != nil {
				return nil, err
			}
			v[i] = x
		}
		cur[k] = vec{v[0], v[1], v[2]}
		if k++; k == 3 {
			tris = append(tris, cur)
			k = 0
		}
	}
	if len(tris) == 0 {
		return nil, errNoTriangles
	}
	return tris, sc.Err()
}

// renderSTL draws the model in a three-quarter view from the front (STL is Z-up),
// with flat shading and a z-buffer. size is the edge length of the square result.
func renderSTL(data []byte, size int) (image.Image, error) {
	tris, err := parseSTL(data)
	if err != nil {
		return nil, err
	}
	if len(tris) == 0 {
		return nil, errNoTriangles
	}
	img := renderTris(tris, size, upAxis(tris))
	if img == nil {
		return nil, errNoTriangles
	}
	return img, nil
}

// orient rotates a vertex so that the given axis points up (+Z).
// up: 0=+Z, 1=-Z, 2=+Y, 3=-Y, 4=+X, 5=-X.
func orient(v vec, up int) vec {
	switch up {
	case 1:
		return vec{v.x, -v.y, -v.z}
	case 2:
		return vec{v.x, -v.z, v.y}
	case 3:
		return vec{v.x, v.z, -v.y}
	case 4:
		return vec{-v.z, v.y, v.x}
	case 5:
		return vec{v.z, v.y, -v.x}
	}
	return v
}

func renderTris(tris [][3]vec, size, up int) image.Image {
	for i := range tris {
		for k := 0; k < 3; k++ {
			tris[i][k] = orient(tris[i][k], up)
		}
	}

	// View rotation: turn 30° around Z, then look down 20°.
	yaw, pitch := 30*math.Pi/180, 20*math.Pi/180
	cy, sy, cp, sp := math.Cos(yaw), math.Sin(yaw), math.Cos(pitch), math.Sin(pitch)
	view := func(v vec) vec {
		x := v.x*cy - v.y*sy
		y := v.x*sy + v.y*cy
		// screen: x right, y up (from z), depth d (towards the viewer is smaller)
		return vec{x, v.z*cp + y*sp, y*cp - v.z*sp}
	}

	min, max := vec{math.Inf(1), math.Inf(1), math.Inf(1)}, vec{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for i := range tris {
		for k := 0; k < 3; k++ {
			p := view(tris[i][k])
			tris[i][k] = p
			min = vec{math.Min(min.x, p.x), math.Min(min.y, p.y), math.Min(min.z, p.z)}
			max = vec{math.Max(max.x, p.x), math.Max(max.y, p.y), math.Max(max.z, p.z)}
		}
	}
	extent := math.Max(max.x-min.x, max.y-min.y)
	if extent <= 0 || math.IsNaN(extent) || math.IsInf(extent, 0) {
		return nil
	}

	ss := size * 2 // supersampling
	scale := float64(ss) * 0.9 / extent
	cx, cyc := (min.x+max.x)/2, (min.y+max.y)/2
	toScreen := func(p vec) vec {
		return vec{(p.x-cx)*scale + float64(ss)/2, float64(ss)/2 - (p.y-cyc)*scale, p.z}
	}

	img := image.NewRGBA(image.Rect(0, 0, ss, ss))
	draw.Draw(img, img.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	depth := make([]float64, ss*ss)
	for i := range depth {
		depth[i] = math.Inf(1)
	}
	light := vec{-0.4, 0.6, -0.7}.norm() // from upper left, towards the model
	base := vec{0xc9, 0xcd, 0xd6}

	for _, t := range tris {
		n := t[1].sub(t[0]).cross(t[2].sub(t[0])).norm()
		// Two-sided lighting: meshes are not always consistently oriented.
		lambert := math.Abs(n.dot(light))
		shade := 0.25 + 0.75*lambert
		col := color.RGBA{uint8(base.x * shade), uint8(base.y * shade), uint8(base.z * shade), 255}

		a, b, c := toScreen(t[0]), toScreen(t[1]), toScreen(t[2])
		area := (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x)
		if area == 0 {
			continue
		}
		x0 := int(math.Max(0, math.Floor(math.Min(a.x, math.Min(b.x, c.x)))))
		x1 := int(math.Min(float64(ss-1), math.Ceil(math.Max(a.x, math.Max(b.x, c.x)))))
		y0 := int(math.Max(0, math.Floor(math.Min(a.y, math.Min(b.y, c.y)))))
		y1 := int(math.Min(float64(ss-1), math.Ceil(math.Max(a.y, math.Max(b.y, c.y)))))
		for y := y0; y <= y1; y++ {
			py := float64(y) + 0.5
			for x := x0; x <= x1; x++ {
				px := float64(x) + 0.5
				w0 := ((b.x-px)*(c.y-py) - (b.y-py)*(c.x-px)) / area
				w1 := ((c.x-px)*(a.y-py) - (c.y-py)*(a.x-px)) / area
				w2 := 1 - w0 - w1
				if w0 < 0 || w1 < 0 || w2 < 0 {
					continue
				}
				d := w0*a.z + w1*b.z + w2*c.z
				if i := y*ss + x; d < depth[i] {
					depth[i] = d
					img.SetRGBA(x, y, col)
				}
			}
		}
	}
	return downsample2(img)
}

// upAxis guesses which axis points up. Files come Z-up (STL convention) as
// well as Y-up or upside down from sculpting tools. A model stands on its base
// or feet: in the right orientation, its lowest points are spread across the
// footprint; lying on a side or upside down, only a ridge or a few tips touch.
// When no orientation is clearly standing (a figure on small feet), the
// longest dimension points up and the side with more contact is the bottom.
// Returns the orient() code.
func upAxis(tris [][3]vec) int {
	const confident = 0.25
	scores := axisScores(tris)
	best := 0
	for up := range scores {
		if scores[up] > scores[best] {
			best = up
		}
	}
	if scores[best] >= confident {
		return best
	}
	// Longest extent: orient codes come in pairs (+Z,-Z), (+Y,-Y), (+X,-X).
	min, max := vec{math.Inf(1), math.Inf(1), math.Inf(1)}, vec{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for i := range tris {
		for k := 0; k < 3; k++ {
			p := tris[i][k]
			min = vec{math.Min(min.x, p.x), math.Min(min.y, p.y), math.Min(min.z, p.z)}
			max = vec{math.Max(max.x, p.x), math.Max(max.y, p.y), math.Max(max.z, p.z)}
		}
	}
	pair := 0 // Z
	ext := max.sub(min)
	if ext.y > ext.z && ext.y >= ext.x {
		pair = 2
	} else if ext.x > ext.z && ext.x > ext.y {
		pair = 4
	}
	if scores[pair+1] > scores[pair] {
		return pair + 1
	}
	return pair
}

// axisScores rates each orientation: spread of the touching points relative to the footprint.
func axisScores(tris [][3]vec) [6]float64 {
	const band = 0.02 // vertices within 2% of the extent count as touching the ground
	var scores [6]float64
	for up := 0; up < 6; up++ {
		min, max := vec{math.Inf(1), math.Inf(1), math.Inf(1)}, vec{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for i := range tris {
			for k := 0; k < 3; k++ {
				p := orient(tris[i][k], up)
				min = vec{math.Min(min.x, p.x), math.Min(min.y, p.y), math.Min(min.z, p.z)}
				max = vec{math.Max(max.x, p.x), math.Max(max.y, p.y), math.Max(max.z, p.z)}
			}
		}
		footprint := (max.x - min.x) * (max.y - min.y)
		if footprint <= 0 {
			continue
		}
		limit := min.z + band*(max.z-min.z)
		cmin, cmax := vec{math.Inf(1), math.Inf(1), 0}, vec{math.Inf(-1), math.Inf(-1), 0}
		for i := range tris {
			for k := 0; k < 3; k++ {
				p := orient(tris[i][k], up)
				if p.z <= limit {
					cmin = vec{math.Min(cmin.x, p.x), math.Min(cmin.y, p.y), 0}
					cmax = vec{math.Max(cmax.x, p.x), math.Max(cmax.y, p.y), 0}
				}
			}
		}
		scores[up] = (cmax.x - cmin.x) * (cmax.y - cmin.y) / footprint
	}
	return scores
}

// downsample2 halves an image by averaging 2x2 blocks (anti-aliasing).
func downsample2(src *image.RGBA) *image.RGBA {
	w, h := src.Rect.Dx()/2, src.Rect.Dy()/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b int
			for _, o := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
				i := src.PixOffset(2*x+o[0], 2*y+o[1])
				r += int(src.Pix[i])
				g += int(src.Pix[i+1])
				b += int(src.Pix[i+2])
			}
			j := dst.PixOffset(x, y)
			dst.Pix[j], dst.Pix[j+1], dst.Pix[j+2], dst.Pix[j+3] = uint8(r/4), uint8(g/4), uint8(b/4), 255
		}
	}
	return dst
}
