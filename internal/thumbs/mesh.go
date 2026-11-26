package thumbs

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"image"
	"io"
	"strconv"
	"strings"
)

// maxTriangles bounds the triangles read for a thumbnail (memory; a picture
// of 384 px does not need more).
const maxTriangles = 3_000_000

var errTooManyTriangles = errors.New("model too large for a thumbnail")

// renderMesh draws already parsed triangles like renderSTL.
func renderMesh(tris [][3]vec, size int) (image.Image, error) {
	if len(tris) == 0 {
		return nil, errNoTriangles
	}
	img := renderTris(tris, size, upAxis(tris))
	if img == nil {
		return nil, errNoTriangles
	}
	return img, nil
}

// parseOBJ reads the vertices and faces of a Wavefront OBJ file (polygons
// are split into triangles; normals and textures are ignored).
func parseOBJ(data []byte) ([][3]vec, error) {
	var verts []vec
	var tris [][3]vec
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "v "):
			f := strings.Fields(line)
			if len(f) < 4 {
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
			verts = append(verts, vec{v[0], v[1], v[2]})
		case strings.HasPrefix(line, "f "):
			f := strings.Fields(line)[1:]
			idx := make([]int, 0, len(f))
			for _, p := range f {
				s, _, _ := strings.Cut(p, "/")
				i, err := strconv.Atoi(s)
				if err != nil {
					return nil, err
				}
				if i < 0 { // relative to the end
					i = len(verts) + i + 1
				}
				if i < 1 || i > len(verts) {
					return nil, errors.New("face refers to a missing vertex")
				}
				idx = append(idx, i-1)
			}
			for k := 1; k+1 < len(idx); k++ {
				tris = append(tris, [3]vec{verts[idx[0]], verts[idx[k]], verts[idx[k+1]]})
			}
			if len(tris) > maxTriangles {
				return nil, errTooManyTriangles
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(tris) == 0 {
		return nil, errNoTriangles
	}
	// OBJ files are usually Y-up; upAxis picks the standing direction anyway.
	return tris, nil
}

// parse3MF reads the meshes of a 3MF file (all objects, placed as in the
// build where given; components are not resolved further than one level).
func parse3MF(data []byte) ([][3]vec, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var tris [][3]vec
	for _, f := range zr.File {
		n := strings.ToLower(f.Name)
		if !strings.HasSuffix(n, ".model") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		err = read3MFModel(io.LimitReader(rc, maxModelBytes), &tris)
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(tris) == 0 {
		return nil, errNoTriangles
	}
	return tris, nil
}

// read3MFModel streams one 3dmodel.model XML and appends its triangles.
func read3MFModel(r io.Reader, tris *[][3]vec) error {
	d := xml.NewDecoder(r)
	var verts []vec
	attr := func(se xml.StartElement, name string) string {
		for _, a := range se.Attr {
			if a.Name.Local == name {
				return a.Value
			}
		}
		return ""
	}
	num := func(s string) float64 {
		x, _ := strconv.ParseFloat(s, 64)
		return x
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "mesh":
			verts = verts[:0]
		case "vertex":
			verts = append(verts, vec{num(attr(se, "x")), num(attr(se, "y")), num(attr(se, "z"))})
		case "triangle":
			v1, e1 := strconv.Atoi(attr(se, "v1"))
			v2, e2 := strconv.Atoi(attr(se, "v2"))
			v3, e3 := strconv.Atoi(attr(se, "v3"))
			if e1 != nil || e2 != nil || e3 != nil || v1 < 0 || v2 < 0 || v3 < 0 ||
				v1 >= len(verts) || v2 >= len(verts) || v3 >= len(verts) {
				continue
			}
			*tris = append(*tris, [3]vec{verts[v1], verts[v2], verts[v3]})
			if len(*tris) > maxTriangles {
				return errTooManyTriangles
			}
		}
	}
}
