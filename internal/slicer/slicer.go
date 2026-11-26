// Package slicer reads print information from sliced files (concept L2):
// estimated print time, layer height and count, material, exposure. It reads
// only the parts it needs (a header, the start and end of a G-code file, the
// settings inside a 3MF) and never changes the file.
package slicer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ErrUnsupported: the format is not read, or the file carries no information.
var ErrUnsupported = errors.New("no print information in this file")

// Exts are the file types Read understands.
var Exts = map[string]bool{"ctb": true, "cbddlp": true, "photon": true, "gcode": true, "gco": true, "3mf": true}

// Info is what a sliced file says about the print. Zero values are unknown.
type Info struct {
	Format         string  `json:"format"`         // e.g. "Chitubox", "G-code", "3MF"
	Slicer         string  `json:"slicer"`         // e.g. "PrusaSlicer 2.7.1"
	PrintSeconds   int64   `json:"printSeconds"`   // estimated print time
	LayerHeight    float64 `json:"layerHeight"`    // mm
	Layers         int     `json:"layers"`         //
	HeightMM       float64 `json:"heightMm"`       // print height
	MaterialML     float64 `json:"materialMl"`     // resin
	MaterialG      float64 `json:"materialG"`      // resin or filament weight
	MaterialM      float64 `json:"materialM"`      // filament length in metres
	Exposure       float64 `json:"exposure"`       // s, resin
	BottomExposure float64 `json:"bottomExposure"` // s, resin
	BottomLayers   int     `json:"bottomLayers"`   //
	Material       string  `json:"material"`       // e.g. "PLA"
	Nozzle         float64 `json:"nozzle"`         // mm
	ResolutionX    int     `json:"resolutionX"`    // px, resin
	ResolutionY    int     `json:"resolutionY"`
}

func (i Info) empty() bool {
	return i.PrintSeconds == 0 && i.LayerHeight == 0 && i.Layers == 0 && i.MaterialML == 0 &&
		i.MaterialG == 0 && i.MaterialM == 0 && i.Exposure == 0 && i.Slicer == ""
}

// Read reads the information of a file of type ext (lower case, no dot).
func Read(r io.ReaderAt, size int64, ext string) (Info, error) {
	var info Info
	var err error
	switch ext {
	case "ctb", "cbddlp", "photon":
		info, err = readChitubox(r, size)
	case "gcode", "gco":
		info, err = readGcode(r, size)
	case "3mf":
		info, err = read3MF(r, size)
	default:
		return info, ErrUnsupported
	}
	if err != nil {
		return info, err
	}
	if info.empty() {
		return info, ErrUnsupported
	}
	return info, nil
}

// Chitubox (.ctb, .cbddlp, .photon): a little-endian header. Encrypted CTB
// files (version 4 with the newer magic) are not read.
const (
	ctbMagicCBDDLP = 0x12FD0019
	ctbMagicCTB    = 0x12FD0086
)

func f32(b []byte) float64 {
	v := float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e7 {
		return 0
	}
	return math.Round(v*1000) / 1000
}

func readChitubox(r io.ReaderAt, size int64) (Info, error) {
	h := make([]byte, 112)
	if _, err := r.ReadAt(h, 0); err != nil {
		return Info{}, err
	}
	le := binary.LittleEndian
	magic := le.Uint32(h)
	if magic != ctbMagicCBDDLP && magic != ctbMagicCTB {
		return Info{}, ErrUnsupported
	}
	u := func(off int) int { return int(le.Uint32(h[off:])) }
	info := Info{
		Format:         "Chitubox",
		HeightMM:       f32(h[28:]),
		LayerHeight:    f32(h[32:]),
		Exposure:       f32(h[36:]),
		BottomExposure: f32(h[40:]),
		BottomLayers:   u(48),
		ResolutionX:    u(52),
		ResolutionY:    u(56),
		Layers:         u(68),
		PrintSeconds:   int64(le.Uint32(h[76:])),
	}
	if info.Layers > 1_000_000 || info.BottomLayers > 10_000 {
		return Info{}, ErrUnsupported // not a header we understand
	}
	// Print parameters (version 2 and later): volume and weight.
	if off, n := int64(le.Uint32(h[84:])), int64(le.Uint32(h[88:])); off > 0 && n >= 28 && off+28 <= size {
		p := make([]byte, 28)
		if _, err := r.ReadAt(p, off); err == nil {
			info.MaterialML = f32(p[20:])
			info.MaterialG = f32(p[24:])
		}
	}
	return info, nil
}

// G-code: the comments slicers write at the start or the end.
var (
	reKV       = regexp.MustCompile(`^;\s*([A-Za-z_ \[\]()]+?)\s*[=:]\s*(.+)$`)
	reDuration = regexp.MustCompile(`(\d+)\s*([dhms])`)
)

// duration parses "1d 2h 3m 4s" (PrusaSlicer) or plain seconds (Cura ";TIME:").
func duration(s string) int64 {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(n)
	}
	var total int64
	for _, m := range reDuration.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		switch m[2] {
		case "d":
			total += n * 86400
		case "h":
			total += n * 3600
		case "m":
			total += n * 60
		case "s":
			total += n
		}
	}
	return total
}

func number(s string) float64 {
	f := strings.Fields(strings.NewReplacer(",", " ", "m", " ", "g", " ").Replace(s))
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || math.IsNaN(v) || v < 0 {
		return 0
	}
	return math.Round(v*1000) / 1000
}

// applyKV takes over one "key = value" setting (G-code comments, PrusaSlicer configs).
func applyKV(info *Info, key, val string) {
	k := strings.ToLower(strings.TrimSpace(key))
	val = strings.TrimSpace(val)
	switch {
	case k == "time" || k == "estimated printing time (normal mode)" || k == "estimated printing time" || k == "total estimated time":
		if d := duration(val); d > 0 && info.PrintSeconds == 0 {
			info.PrintSeconds = d
		}
	case k == "layer_height" || k == "layer height":
		if info.LayerHeight == 0 {
			info.LayerHeight = number(val)
		}
	case k == "layer_count" || k == "layer count" || k == "total layers count" || k == "total layer number":
		if n := int(number(val)); n > 0 {
			info.Layers = n
		}
	case k == "filament used [g]" || k == "total filament used [g]" || k == "total filament weight [g]":
		if g := number(val); g > 0 {
			info.MaterialG = g
		}
	case k == "filament used [mm]":
		if mm := number(val); mm > 0 && info.MaterialM == 0 {
			info.MaterialM = math.Round(mm/10) / 100
		}
	case k == "filament used": // Cura: "1.23456m"
		if m := number(val); m > 0 && info.MaterialM == 0 {
			info.MaterialM = m
		}
	case k == "filament_type" || k == "filament type":
		if info.Material == "" {
			info.Material = strings.Trim(strings.Split(val, ";")[0], `"`)
		}
	case k == "nozzle_diameter" || k == "nozzle diameter":
		if info.Nozzle == 0 {
			info.Nozzle = number(strings.Split(val, ",")[0])
		}
	case k == "generated by" || k == "generator":
		if info.Slicer == "" {
			info.Slicer = val
		}
	}
}

const gcodeChunk = 256 << 10

func readGcode(r io.ReaderAt, size int64) (Info, error) {
	info := Info{Format: "G-code"}
	scan := func(off, n int64) error {
		if n <= 0 {
			return nil
		}
		b := make([]byte, n)
		if _, err := r.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, ";") {
				continue
			}
			if v, ok := strings.CutPrefix(line, ";TIME:"); ok { // Cura
				applyKV(&info, "time", v)
				continue
			}
			if v, ok := strings.CutPrefix(line, ";LAYER_COUNT:"); ok {
				applyKV(&info, "layer_count", v)
				continue
			}
			if v, ok := strings.CutPrefix(line, ";Generated with "); ok {
				applyKV(&info, "generated by", v)
				continue
			}
			if strings.HasPrefix(line, "; generated by ") {
				applyKV(&info, "generated by", strings.Fields(strings.TrimPrefix(line, "; generated by "))[0]+
					" "+firstField(strings.Fields(strings.TrimPrefix(line, "; generated by ")), 1))
				continue
			}
			if m := reKV.FindStringSubmatch(line); m != nil {
				applyKV(&info, m[1], m[2])
			}
		}
		return nil
	}
	head := min(size, gcodeChunk)
	if err := scan(0, head); err != nil {
		return info, err
	}
	if size > head {
		start := max(head, size-gcodeChunk)
		if err := scan(start, size-start); err != nil {
			return info, err
		}
	}
	info.Slicer = strings.TrimSpace(info.Slicer)
	return info, nil
}

func firstField(f []string, i int) string {
	if i < len(f) {
		return f[i]
	}
	return ""
}

// 3MF: PrusaSlicer / Orca / Bambu settings and, in a sliced project, the
// estimate Bambu Studio writes.
func read3MF(r io.ReaderAt, size int64) (Info, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Info{}, err
	}
	info := Info{Format: "3MF"}
	open := func(f *zip.File, limit int64) ([]byte, error) {
		if int64(f.UncompressedSize64) > limit {
			return nil, ErrUnsupported
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, limit))
	}
	for _, f := range zr.File {
		switch strings.ToLower(f.Name) {
		case "metadata/slic3r_pe.config", "metadata/prusaslicer.config":
			b, err := open(f, 8<<20)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(b), "\n") {
				if m := reKV.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
					applyKV(&info, m[1], m[2])
				}
			}
		case "metadata/project_settings.config": // Bambu / Orca: JSON
			b, err := open(f, 16<<20)
			if err != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			for _, k := range []string{"layer_height", "filament_type", "nozzle_diameter"} {
				switch v := m[k].(type) {
				case string:
					applyKV(&info, k, v)
				case []any:
					if len(v) > 0 {
						if s, ok := v[0].(string); ok {
							applyKV(&info, k, s)
						}
					}
				}
			}
		case "metadata/slice_info.config": // Bambu: <metadata key="prediction" value="..."/>
			b, err := open(f, 8<<20)
			if err != nil {
				continue
			}
			d := xml.NewDecoder(bytes.NewReader(b))
			for {
				tok, err := d.Token()
				if err != nil {
					break
				}
				se, ok := tok.(xml.StartElement)
				if !ok || se.Name.Local != "metadata" {
					continue
				}
				var key, val string
				for _, a := range se.Attr {
					switch a.Name.Local {
					case "key":
						key = a.Value
					case "value":
						val = a.Value
					}
				}
				switch key {
				case "prediction":
					info.PrintSeconds += int64(number(val))
				case "weight":
					info.MaterialG += number(val)
				}
			}
		}
	}
	if info.Slicer == "" {
		for _, f := range zr.File {
			if strings.EqualFold(f.Name, "3D/3dmodel.model") {
				if b, err := open(f, 1<<20); err == nil || errors.Is(err, ErrUnsupported) {
					info.Slicer = applicationOf(f, b)
				}
			}
		}
	}
	return info, nil
}

// applicationOf reads <metadata name="Application"> from the start of the model XML.
func applicationOf(f *zip.File, b []byte) string {
	if b == nil {
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		b, _ = io.ReadAll(io.LimitReader(rc, 64<<10))
	}
	if len(b) > 64<<10 {
		b = b[:64<<10]
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "resources" {
			return ""
		}
		if se.Name.Local != "metadata" {
			continue
		}
		for _, a := range se.Attr {
			if a.Name.Local == "name" && strings.EqualFold(a.Value, "Application") {
				var s string
				if d.DecodeElement(&s, &se) == nil {
					return strings.TrimSpace(s)
				}
			}
		}
	}
}
