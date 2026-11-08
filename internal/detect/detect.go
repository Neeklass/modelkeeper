// Package detect suggests models from the files found by the scanner (F4).
//
// The core (Detect) is a pure function over paths, so the heuristics can be
// tested without a database. Apply runs it for a root and stores the result
// while keeping model IDs stable across rescans (D6, F4e, F6).
//
// Heuristics, bottom-up from each content file (3D model, slicer file, archive):
//   - Start at the folder containing the file (an archive is its own model, F4b).
//   - Climb while the folder is a part folder (Supported, STL, Base, 32mm …, F4c),
//     a size variant (S_, M_, 75mm …, F4f), or the only subfolder of its parent.
//   - Never climb into the creator level (first level below the root, F4a) or the root.
//   - Folders between creator and model become tag suggestions (F4g).
//   - Loose content files directly in the root or a creator folder are single-file models.
package detect

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Content categories that make a model; supplementary files only attach.
const (
	CatModel   = "model"
	CatSlicer  = "slicer"
	CatArchive = "archive"
)

// Entry is a present file on disk below a root.
type Entry struct {
	LocID    int64
	Path     string // '/'-separated, relative to root
	Category string
	FileID   int64 // 0 = not hashed
	Members  []Member
}

// Member is a file inside an archive.
type Member struct {
	LocID    int64
	Path     string // relative to the archive
	Category string
	FileID   int64
}

// Rules configure the heuristics (F10).
type Rules struct {
	CreatorLevel bool
	PartNames    []string
	SizeTokens   []string
}

// Candidate is a suggested model.
type Candidate struct {
	Anchor   string   // folder, archive or file path relative to root
	Extra    []string // further folders of the same model ("Pre-Supported/Orc" next to "Orc")
	Kind     string   // folder | archive | file
	Name     string
	Creator  string
	Tags     []string // tag suggestions from intermediate folders
	Supports string   // presupported | unsupported | both | ""
	Assign   []Assign
}

// Assign maps a location to the candidate, with an optional size variant.
type Assign struct {
	LocID   int64
	Variant string
	FileID  int64
	Content bool // model/slicer/archive (counts for content matching)
}

type rules struct {
	creatorDepth int
	parts        map[string]bool
	sizes        []string
}

func compile(r Rules) rules {
	c := rules{parts: map[string]bool{}}
	if r.CreatorLevel {
		c.creatorDepth = 1
	}
	for _, p := range r.PartNames {
		c.parts[normalize(p)] = true
	}
	for _, s := range r.SizeTokens {
		if s = strings.TrimSpace(s); s != "" {
			c.sizes = append(c.sizes, s)
		}
	}
	return c
}

// normalize lowercases and keeps only letters and digits ("Pre-Supported STL" -> "presupportedstl").
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var mmToken = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{2,3})\s?mm(?:$|[^a-z0-9])`)

// variantOf returns the size variant label of a folder/archive name, or "".
func (c rules) variantOf(name string) string {
	if m := mmToken.FindStringSubmatch(name); m != nil {
		return m[1] + "mm"
	}
	for _, tok := range c.sizes {
		t := strings.ToLower(tok)
		n := strings.ToLower(name)
		if strings.HasSuffix(t, "mm") {
			continue // handled by mmToken
		}
		if n == t {
			return strings.ToUpper(tok)
		}
		for _, sep := range []string{"_", "-", " "} {
			if strings.HasPrefix(n, t+sep) || strings.HasSuffix(n, sep+t) {
				return strings.ToUpper(tok)
			}
		}
	}
	return ""
}

// isPart reports whether a folder name is a typical part folder (F4c).
func (c rules) isPart(name string) bool {
	n := normalize(name)
	if n == "" || c.parts[n] || partTokens[n] {
		return true
	}
	// "Orc Presupported", "STL Supported", "Unsupported Files" …
	return strings.Contains(n, "supported") || strings.Contains(n, "supports")
}

// Supports detection (L2) from the names below a model.
// Each path segment is checked separately; "_SUP" as a name suffix also counts.
func supportsOf(paths []string) string {
	sup, unsup := false, false
	for _, p := range paths {
		for _, seg := range splitPath(p) {
			n := normalize(stem(seg))
			if strings.Contains(n, "unsupported") || strings.Contains(n, "nosupport") {
				unsup = true
				n = strings.ReplaceAll(n, "unsupported", "")
			}
			if strings.Contains(n, "supported") || strings.Contains(n, "supports") ||
				strings.Contains(n, "presupport") ||
				strings.HasSuffix(strings.ToLower(stem(seg)), "_sup") {
				sup = true
			}
		}
	}
	switch {
	case sup && unsup:
		return "both"
	case sup:
		return "presupported"
	case unsup:
		return "unsupported"
	}
	return ""
}

func isContent(cat string) bool { return cat == CatModel || cat == CatSlicer || cat == CatArchive }

func splitPath(p string) []string {
	if p == "" || p == "." {
		return nil
	}
	return strings.Split(p, "/")
}

func stem(name string) string { return strings.TrimSuffix(name, path.Ext(name)) }

// Detect computes model candidates for one root.
func Detect(entries []Entry, r Rules) []Candidate {
	c := compile(r)

	// Content folders and their direct content-subfolders (for the single-child rule).
	children := map[string]map[string]bool{}
	for _, e := range entries {
		if !isContent(e.Category) {
			continue
		}
		parts := splitPath(path.Dir(e.Path))
		for i := 0; i < len(parts); i++ {
			parent := strings.Join(parts[:i], "/")
			if children[parent] == nil {
				children[parent] = map[string]bool{}
			}
			children[parent][strings.Join(parts[:i+1], "/")] = true
		}
	}

	cands := map[string]*Candidate{}
	// climbed records for every visited folder which anchor it resolved to,
	// so supplementary files in part folders find their model.
	folderAnchor := map[string]string{}

	get := func(anchor, kind string) *Candidate {
		if cd, ok := cands[anchor]; ok {
			return cd
		}
		cd := &Candidate{Anchor: anchor, Kind: kind}
		cands[anchor] = cd
		return cd
	}

	// resolve climbs from folder dir to its model folder.
	resolve := func(dir string) string {
		cur := splitPath(dir)
		visited := []string{}
		for len(cur) > c.creatorDepth+1 {
			name := cur[len(cur)-1]
			parent := strings.Join(cur[:len(cur)-1], "/")
			// F4c: part folders, size variants and single subfolders merge into the
			// folder above. "Weak" parts ("pirate stl sfw", "presupport_juri_parts")
			// merge when the rest of their name only repeats the parent's name.
			// Never via a part folder: "Pack/Pre-Supported/Orc" must not merge into "Pack".
			single := len(children[parent]) == 1 && !c.isPart(path.Base(parent))
			merge := c.isPart(name) || c.variantOf(name) != "" || single ||
				c.isWeakPart(name, path.Base(parent))
			if !merge {
				break
			}
			visited = append(visited, strings.Join(cur, "/"))
			cur = cur[:len(cur)-1]
		}
		anchor := strings.Join(cur, "/")
		for _, v := range visited {
			folderAnchor[v] = anchor
		}
		folderAnchor[anchor] = anchor
		return anchor
	}

	// variantBetween returns the first size variant on the way from anchor down to rel.
	variantBetween := func(anchor, dir string) string {
		rest := strings.TrimPrefix(strings.TrimPrefix(dir, anchor), "/")
		for _, seg := range splitPath(rest) {
			if v := c.variantOf(seg); v != "" {
				return v
			}
		}
		return ""
	}

	var supplementary []Entry
	for _, e := range entries {
		if !isContent(e.Category) {
			supplementary = append(supplementary, e)
			continue
		}
		dir := path.Dir(e.Path)
		if dir == "." {
			dir = ""
		}
		depth := len(splitPath(dir))
		base := path.Base(e.Path)

		switch {
		case e.Category == CatArchive && depth > c.creatorDepth && (c.isPart(stem(base)) ||
			c.variantOf(stem(base)) != "" || c.isWeakPart(stem(base), path.Base(dir)) ||
			c.isPart(path.Base(dir)) || c.variantOf(path.Base(dir)) != ""):
			// "Supported.zip", "S_Orc.zip", "Orc/unsupported/orc_STLs.zip": part of the model folder.
			anchor := resolve(dir)
			cd := get(anchor, "folder")
			v := variantBetween(anchor, dir)
			if v == "" {
				v = c.variantOf(stem(base))
			}
			addArchive(cd, e, v, c)
		case e.Category == CatArchive:
			cd := get(e.Path, "archive") // F4b
			addArchive(cd, e, "", c)
		case depth <= c.creatorDepth:
			cd := get(e.Path, "file")
			cd.Assign = append(cd.Assign, Assign{LocID: e.LocID, FileID: e.FileID, Content: true})
		default:
			anchor := resolve(dir)
			cd := get(anchor, "folder")
			cd.Assign = append(cd.Assign, Assign{
				LocID: e.LocID, FileID: e.FileID, Content: true, Variant: variantBetween(anchor, dir),
			})
		}
	}

	// Supplementary files (images, PDFs …): nearest model folder above them,
	// or an archive/file model with the same name stem next to them.
	stemIndex := map[string]*Candidate{}
	for _, cd := range cands {
		if cd.Kind != "folder" {
			stemIndex[strings.ToLower(stem(cd.Anchor))] = cd
		}
	}
	for _, e := range supplementary {
		dir := path.Dir(e.Path)
		if dir == "." {
			dir = ""
		}
		if cd, ok := stemIndex[strings.ToLower(stem(e.Path))]; ok {
			cd.Assign = append(cd.Assign, Assign{LocID: e.LocID, FileID: e.FileID})
			continue
		}
		parts := splitPath(dir)
		for i := len(parts); i > c.creatorDepth; i-- {
			p := strings.Join(parts[:i], "/")
			if a, ok := folderAnchor[p]; ok {
				cd := cands[a]
				cd.Assign = append(cd.Assign, Assign{LocID: e.LocID, FileID: e.FileID, Variant: variantBetween(a, dir)})
				break
			}
		}
	}

	locPath := map[int64]string{} // location -> path used for supports detection
	for _, e := range entries {
		locPath[e.LocID] = e.Path
		for _, m := range e.Members {
			locPath[m.LocID] = m.Path
		}
	}
	for _, cd := range cands {
		finish(cd, c, locPath)
	}
	out := mergeSplitTrees(cands, c)
	sort.Slice(out, func(i, j int) bool { return out[i].Anchor < out[j].Anchor })
	return out
}

// mergeSplitTrees joins folder models whose paths differ only by part
// folders, e.g. "Pack/Orc" and "Pack/24-10 Pre-Supported/Orc" (supported and
// unsupported versions kept in parallel trees). The path with the fewest part
// folders becomes the main anchor.
func mergeSplitTrees(cands map[string]*Candidate, c rules) []Candidate {
	groups := map[string][]*Candidate{}
	var order []string
	for _, cd := range cands {
		key := cd.Kind + ":" + cd.Anchor
		if cd.Kind == "folder" {
			var segs []string
			for _, seg := range splitPath(cd.Anchor) {
				if !c.isPart(seg) {
					segs = append(segs, normalize(cleanName(seg)))
				}
			}
			key = "folder:" + strings.Join(segs, "/")
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], cd)
	}
	partCount := func(a string) int {
		n := 0
		for _, seg := range splitPath(a) {
			if c.isPart(seg) {
				n++
			}
		}
		return n
	}
	out := make([]Candidate, 0, len(groups))
	for _, key := range order {
		g := groups[key]
		sort.Slice(g, func(i, j int) bool {
			pi, pj := partCount(g[i].Anchor), partCount(g[j].Anchor)
			if pi != pj {
				return pi < pj
			}
			return g[i].Anchor < g[j].Anchor
		})
		main := *g[0]
		for _, o := range g[1:] {
			main.Extra = append(main.Extra, o.Anchor)
			main.Assign = append(main.Assign, o.Assign...)
			main.Supports = combineSupports(main.Supports, o.Supports)
			for _, t := range o.Tags {
				if !containsFold(main.Tags, t) {
					main.Tags = append(main.Tags, t)
				}
			}
		}
		out = append(out, main)
	}
	return out
}

func combineSupports(a, b string) string {
	switch {
	case a == b || b == "":
		return a
	case a == "":
		return b
	}
	return "both"
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func addArchive(cd *Candidate, e Entry, variant string, c rules) {
	cd.Assign = append(cd.Assign, Assign{LocID: e.LocID, FileID: e.FileID, Content: true, Variant: variant})
	for _, m := range e.Members {
		v := variant
		if v == "" {
			for _, seg := range splitPath(path.Dir(m.Path)) {
				if v = c.variantOf(seg); v != "" {
					break
				}
			}
		}
		cd.Assign = append(cd.Assign, Assign{LocID: m.LocID, FileID: m.FileID, Content: isContent(m.Category), Variant: v})
	}
}

// finish fills name, creator, tag suggestions and supports.
func finish(cd *Candidate, c rules, locPath map[int64]string) {
	parts := splitPath(cd.Anchor)
	name := parts[len(parts)-1]
	if cd.Kind != "folder" {
		name = stem(name)
	}
	cd.Name = cleanName(name)
	if c.creatorDepth == 1 && len(parts) >= 2 {
		cd.Creator = parts[0]
	}
	// Intermediate levels between creator and model (F4g).
	for i := c.creatorDepth; i < len(parts)-1; i++ {
		seg := parts[i]
		if c.isPart(seg) || c.variantOf(seg) != "" || (cd.Creator != "" && similarNames(seg, cd.Creator)) {
			continue
		}
		cd.Tags = append(cd.Tags, cleanName(seg))
	}
	// Supports: the model's own path counts too ("…/Pre-Supported/Betrayer").
	paths := []string{strings.Join(parts[min(c.creatorDepth, len(parts)-1):], "/")}
	for _, a := range cd.Assign {
		p := locPath[a.LocID]
		if cd.Kind == "folder" {
			p = strings.TrimPrefix(strings.TrimPrefix(p, cd.Anchor), "/")
		}
		paths = append(paths, p)
	}
	cd.Supports = supportsOf(paths)
}

// VariantSort orders variant labels: XS < S < M < L < XL < XXL, then sizes in mm.
func VariantSort(label string) int {
	order := map[string]int{"XS": 1, "S": 2, "M": 3, "L": 4, "XL": 5, "XXL": 6, "XXXL": 7}
	if o, ok := order[strings.ToUpper(label)]; ok {
		return o
	}
	if strings.HasSuffix(label, "mm") {
		if n, err := strconv.Atoi(strings.TrimSuffix(label, "mm")); err == nil {
			return 100 + n
		}
	}
	return 10000
}

// tokens splits a name into lowercase words at separators, camelCase and
// letter/digit boundaries ("ATLAS_StellaBust" -> atlas, stella, bust;
// "Isla120_LYS" -> isla, 120, lys).
func tokens(name string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if i > 0 && len(cur) > 0 {
			prev := rs[i-1]
			camel := unicode.IsUpper(r) && unicode.IsLower(prev)
			kindChange := unicode.IsDigit(r) != unicode.IsDigit(prev)
			if camel || kindChange {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// similarNames reports whether two folder names describe the same thing:
// one contains the other, or most words of the shorter one appear in the other.
func similarNames(a, b string) bool {
	na, nb := normalize(a), normalize(b)
	if na == "" || nb == "" {
		return false
	}
	if strings.Contains(na, nb) || strings.Contains(nb, na) {
		return true
	}
	ta, tb := tokens(a), tokens(b)
	set := map[string]bool{}
	for _, t := range ta {
		if len(t) > 1 {
			set[t] = true
		}
	}
	common, total := 0, 0
	for _, t := range tb {
		if len(t) > 1 {
			total++
			if set[t] {
				common++
			}
		}
	}
	smaller := min(total, len(set))
	return smaller > 0 && float64(common)/float64(smaller) >= 0.6
}

// partTokens mark a folder name as being about files rather than a model.
var partTokens = map[string]bool{
	"stl": true, "stls": true, "obj": true, "3mf": true, "lys": true, "lychee": true, "chitubox": true,
	"parts": true, "part": true, "files": true, "presupport": true, "presupported": true, "supported": true,
	"unsupported": true, "supports": true, "sup": true, "unsup": true, "base": true, "bases": true,
}

// genericTokens carry no model identity.
var genericTokens = map[string]bool{
	"sfw": true, "nsfw": true, "extra": true, "extras": true, "bonus": true, "version": true, "full": true,
	"pre": true, "un": true, "and": true, "with": true, "the": true, "for": true,
}

// isWeakPart: the name contains a part token, and every other word is
// generic or appears in the parent's name ("pirate stl sfw" below "Pirate").
func (c rules) isWeakPart(name, parent string) bool {
	ts := tokens(name)
	hasPart := false
	for _, t := range ts {
		if partTokens[t] {
			hasPart = true
		}
	}
	if !hasPart {
		return false
	}
	pt := map[string]bool{}
	for _, t := range tokens(parent) {
		pt[t] = true
	}
	for _, t := range ts {
		if partTokens[t] || genericTokens[t] || pt[t] || isNumber(t) || len(t) < 2 {
			continue
		}
		return false
	}
	return true
}

func isNumber(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// mmfID matches the 13-character hex IDs MyMiniFactory puts in download names.
var mmfID = regexp.MustCompile(`(?i)^[0-9a-f]{13}[_ -]+|[_ -]+[0-9a-f]{13}$`)

// cleanName removes download IDs and surrounding separators from a name.
func cleanName(name string) string {
	n := strings.TrimSpace(mmfID.ReplaceAllString(name, ""))
	if n == "" {
		return strings.TrimSpace(name)
	}
	return n
}
