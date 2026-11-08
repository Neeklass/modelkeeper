package detect

import (
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"modelkeeper/internal/settings"
)

func defaultRules() Rules {
	d := settings.Defaults()
	return Rules{CreatorLevel: true, PartNames: d.PartNames, SizeTokens: d.SizeTokens}
}

func cat(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".stl", ".obj", ".3mf":
		return CatModel
	case ".lys", ".chitubox", ".ctb":
		return CatSlicer
	case ".zip", ".rar", ".7z":
		return CatArchive
	}
	return "supplementary"
}

// entries builds entries from paths; ids are 1..n in order.
func entries(paths ...string) []Entry {
	var out []Entry
	for i, p := range paths {
		out = append(out, Entry{LocID: int64(i + 1), Path: p, Category: cat(p), FileID: int64(i + 1)})
	}
	return out
}

type got struct {
	Name, Creator, Supports string
	Tags                    []string
	Files                   []string // "path[variant]"
}

func summarize(es []Entry, cs []Candidate) map[string]got {
	byID := map[int64]string{}
	for _, e := range es {
		byID[e.LocID] = e.Path
		for _, m := range e.Members {
			byID[m.LocID] = e.Path + "!" + m.Path
		}
	}
	out := map[string]got{}
	for _, c := range cs {
		g := got{Name: c.Name, Creator: c.Creator, Supports: c.Supports, Tags: c.Tags}
		for _, a := range c.Assign {
			f := byID[a.LocID]
			if a.Variant != "" {
				f += "[" + a.Variant + "]"
			}
			g.Files = append(g.Files, f)
		}
		sort.Strings(g.Files)
		out[c.Anchor] = g
	}
	return out
}

func TestReferenceExample(t *testing.T) {
	// Concept F, reference example.
	es := entries(
		"Creator/Collection/Model/S_PART/S_PART_BASE.stl",
		"Creator/Collection/Model/S_PART/S_PART_BODY.stl",
		"Creator/Collection/Model/M_PART/M_PART_BASE.stl",
		"Creator/Collection/Model/M_PART/M_PART_BODY.stl",
		"Creator/Collection/Model/preview.jpg",
		"Creator/Collection/Other Model/Supported/other.stl",
		"Creator/Collection/Other Model/Unsupported/other.stl",
		"Creator/Collection/Other Model/Unsupported/other.lys",
	)
	res := summarize(es, Detect(es, defaultRules()))
	want := map[string]got{
		"Creator/Collection/Model": {Name: "Model", Creator: "Creator", Tags: []string{"Collection"}, Files: []string{
			"Creator/Collection/Model/M_PART/M_PART_BASE.stl[M]",
			"Creator/Collection/Model/M_PART/M_PART_BODY.stl[M]",
			"Creator/Collection/Model/S_PART/S_PART_BASE.stl[S]",
			"Creator/Collection/Model/S_PART/S_PART_BODY.stl[S]",
			"Creator/Collection/Model/preview.jpg",
		}},
		"Creator/Collection/Other Model": {Name: "Other Model", Creator: "Creator", Supports: "both",
			Tags: []string{"Collection"}, Files: []string{
				"Creator/Collection/Other Model/Supported/other.stl",
				"Creator/Collection/Other Model/Unsupported/other.lys",
				"Creator/Collection/Other Model/Unsupported/other.stl",
			}},
	}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("got  %#v\nwant %#v", res, want)
	}
}

func TestRealWorldNesting(t *testing.T) {
	// Pattern seen in a real collection: repeated nesting + "… SUPPORTS" folder + _SUP suffix.
	es := entries(
		"Atlas 3DSS (Merchant)/Stella, the Moon Priestess/ATLAS_StellaBust/Stella Bust/Stella Bust SUPPORTS/Braid_SUP.stl",
		"Atlas 3DSS (Merchant)/Stella, the Moon Priestess/ATLAS_StellaBust/Stella Bust/Stella Bust SUPPORTS/Head_SUP.stl",
		"Atlas 3DSS (Merchant)/Stella, the Moon Priestess/ATLAS_StellaBust/Stella Bust/Braid.stl",
		"Atlas 3DSS (Merchant)/Stella, the Moon Priestess/ATLAS_StellaFull/Stella Full/Body.stl",
	)
	res := summarize(es, Detect(es, defaultRules()))
	if len(res) != 2 {
		t.Fatalf("want 2 models, got %d: %#v", len(res), res)
	}
	// Single subfolder "Stella Bust" merges into "ATLAS_StellaBust"; the SUPPORTS folder is a part.
	bust, ok := res["Atlas 3DSS (Merchant)/Stella, the Moon Priestess/ATLAS_StellaBust"]
	if !ok {
		t.Fatalf("bust model missing: %#v", res)
	}
	if bust.Creator != "Atlas 3DSS (Merchant)" || bust.Supports != "presupported" || len(bust.Files) != 3 {
		t.Errorf("bust = %#v", bust)
	}
	if !reflect.DeepEqual(bust.Tags, []string{"Stella, the Moon Priestess"}) {
		t.Errorf("tags = %#v", bust.Tags)
	}
}

func TestArchivesAndLooseFiles(t *testing.T) {
	es := entries(
		"Creator/Orc Warband.zip",
		"Creator/Orc Warband.jpg", // same stem: belongs to the archive model
		"Creator/loose.stl",       // loose file at creator level
		"Creator/Model/Model.stl",
		"Creator/Model/Supported.zip", // part archive merges into the folder model
		"root-level.stl",
	)
	es[0].Members = []Member{
		{LocID: 100, Path: "Orc/S_Orc/orc.stl", Category: CatModel},
		{LocID: 101, Path: "Orc/M_Orc/orc.stl", Category: CatModel},
	}
	res := summarize(es, Detect(es, defaultRules()))
	wantAnchors := []string{"Creator/Model", "Creator/Orc Warband.zip", "Creator/loose.stl", "root-level.stl"}
	var anchors []string
	for a := range res {
		anchors = append(anchors, a)
	}
	sort.Strings(anchors)
	if !reflect.DeepEqual(anchors, wantAnchors) {
		t.Fatalf("anchors = %v", anchors)
	}
	orc := res["Creator/Orc Warband.zip"]
	wantOrc := []string{
		"Creator/Orc Warband.jpg",
		"Creator/Orc Warband.zip",
		"Creator/Orc Warband.zip!Orc/M_Orc/orc.stl[M]",
		"Creator/Orc Warband.zip!Orc/S_Orc/orc.stl[S]",
	}
	if orc.Name != "Orc Warband" || orc.Creator != "Creator" || !reflect.DeepEqual(orc.Files, wantOrc) {
		t.Errorf("orc = %#v", orc)
	}
	if m := res["Creator/Model"]; len(m.Files) != 2 || m.Supports != "presupported" {
		t.Errorf("model = %#v", m)
	}
	if r := res["root-level.stl"]; r.Creator != "" || r.Name != "root-level" {
		t.Errorf("root file = %#v", r)
	}
}

func TestCreatorLevelOff(t *testing.T) {
	es := entries("Orc/Supported/orc.stl", "Goblin/goblin.stl")
	r := defaultRules()
	r.CreatorLevel = false
	res := summarize(es, Detect(es, r))
	if _, ok := res["Orc"]; !ok || res["Orc"].Creator != "" {
		t.Errorf("res = %#v", res)
	}
	if _, ok := res["Goblin"]; !ok {
		t.Errorf("res = %#v", res)
	}
}

func TestPartFolderDirectlyBelowCreatorStays(t *testing.T) {
	// Never climb into the creator folder, even for part names.
	es := entries("Creator/STL/a.stl")
	res := summarize(es, Detect(es, defaultRules()))
	if _, ok := res["Creator/STL"]; !ok {
		t.Errorf("res = %#v", res)
	}
}

func TestVariantOf(t *testing.T) {
	c := compile(defaultRules())
	for in, want := range map[string]string{
		"S_Orc": "S", "Orc_XL": "XL", "m-body": "M", "32mm": "32mm", "Orc 75mm": "75mm",
		"Orc_75mm_Base": "75mm", "Sword": "", "Supported": "", "Lsomething": "", "XL": "XL",
	} {
		if got := c.variantOf(in); got != want {
			t.Errorf("variantOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVariantSort(t *testing.T) {
	ls := []string{"75mm", "XL", "S", "32mm", "M"}
	sort.Slice(ls, func(i, j int) bool { return VariantSort(ls[i]) < VariantSort(ls[j]) })
	if !reflect.DeepEqual(ls, []string{"S", "M", "XL", "32mm", "75mm"}) {
		t.Errorf("sorted = %v", ls)
	}
}

func TestSingleSubfolderMerges(t *testing.T) {
	// Concept F4c: a single subfolder merges into the folder above, which keeps
	// the user's own wrapper names ("Schwarzbär/<download folder>").
	es := entries(
		"Animal Den/Schwarzbär/64f1d60012ab7_black-bear-angry/bear.stl",
		"Animal Den/Schwarzbär/64f1d60012ab7_black-bear-angry/bear_base.stl",
		"Creator/Wrapper/Wrapper/goblin.stl",
	)
	res := summarize(es, Detect(es, defaultRules()))
	if g, ok := res["Animal Den/Schwarzbär"]; !ok || g.Name != "Schwarzbär" || len(g.Files) != 2 {
		t.Errorf("wrapper not merged: %#v", res)
	}
	if _, ok := res["Creator/Wrapper"]; !ok {
		t.Errorf("double folder not merged: %v", res)
	}
}

func TestWeakPartsAndSupportsAbove(t *testing.T) {
	es := entries(
		"Anyone/Pirate/pirate stl sfw/a.stl",
		"Anyone/Pirate/pirate stl nsfw/b.stl",
		"Anyone/Street Fighter - Juri/presupport_juri_NSFW_parts_stl/c.stl",
		"Anyone/Street Fighter - Juri/juri_base/d.stl",
		"Bastet/Chibis/JinxChibi_STL/e.stl", // a real model, not a part
		"Bastet/Chibis/VayneChibi_STL/f.stl",
		"Bite the Bullet/Bullet Hell/23-06 Pre-Supported/Betrayer/g.stl",
		"Bite the Bullet/Bullet Hell/23-06 Pre-Supported/Brute/h.stl",
		"Bite the Bullet/BITE THE BULLET/Knight/i.stl",
		"Bite the Bullet/BITE THE BULLET/Mage/j.stl",
	)
	res := summarize(es, Detect(es, defaultRules()))
	if p := res["Anyone/Pirate"]; len(p.Files) != 2 {
		t.Errorf("pirate = %#v", p)
	}
	if _, ok := res["Bastet/Chibis/JinxChibi_STL"]; !ok {
		t.Errorf("chibi models merged: %v", res)
	}
	if b := res["Bite the Bullet/Bullet Hell/23-06 Pre-Supported/Betrayer"]; b.Supports != "presupported" {
		t.Errorf("betrayer = %#v", b)
	}
	if k := res["Bite the Bullet/BITE THE BULLET/Knight"]; len(k.Tags) != 0 {
		t.Errorf("creator name suggested as tag: %#v", k.Tags)
	}
	// "juri_base" has its own name: it's a part only because "base" is a part name.
	if j := res["Anyone/Street Fighter - Juri"]; len(j.Files) != 2 {
		t.Errorf("juri = %#v", j)
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"64f1d60012ab7_black-bear-angry":            "black-bear-angry",
		"blue_footed_booby_fly_1_16 _66acdb049add4": "blue_footed_booby_fly_1_16",
		"Orc Warrior":   "Orc Warrior",
		"64f1d60012ab7": "64f1d60012ab7",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSimilarNames(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"ATLAS_StellaBust_wShadowheartHead", "Stella Bust with Shadowheart Head", true},
		{"Orc", "Orc", true},
		{"Orc Warrior", "orc_warrior_v2", true},
		{"Collection", "Orc", false},
		{"Stella, the Moon Priestess", "ATLAS_StellaFull", false},
	} {
		if got := similarNames(c.a, c.b); got != c.want {
			t.Errorf("similarNames(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestTokens(t *testing.T) {
	for in, want := range map[string][]string{
		"ATLAS_StellaBust": {"atlas", "stella", "bust"},
		"Isla120_LYS":      {"isla", "120", "lys"},
		"32mm":             {"32", "mm"},
		"pirate stl sfw":   {"pirate", "stl", "sfw"},
	} {
		if got := tokens(in); !reflect.DeepEqual(got, want) {
			t.Errorf("tokens(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSplitTreesMerge(t *testing.T) {
	es := entries(
		"Bite the Bullet/Grove Haven/24-10 Pre-Supported/Frogfolk Wizard/frog_sup.stl",
		"Bite the Bullet/Grove Haven/Frogfolk Wizard/frog.stl",
		"Bite the Bullet/Grove Haven/Hedgehogfolk Bard/bard.stl",
		"Bite the Bullet/Lonely/25-01 Pre-Supported/Only Model/only_sup.stl", // only model in Pre-Supported
		"Bite the Bullet/Lonely/Only Model/only.stl",
		"Bite the Bullet/Lonely/Other/other.stl",
		"Atlas/Foxy Fierce Fiona/supported/fiona_sup.stl",
		"Atlas/Foxy Fierce Fiona/unsupported/sniperwolf_STLs.zip",
		"Bastet/Sisterhood/Isla/Isla_120mm/Isla120_LYS/isla.lys",
		"Bastet/Sisterhood/Isla/Isla_75mm/isla.stl",
		"Bastet/Sisterhood/Zara/zara.stl",
	)
	res := summarize(es, Detect(es, defaultRules()))
	frog, ok := res["Bite the Bullet/Grove Haven/Frogfolk Wizard"]
	if !ok || len(frog.Files) != 2 || frog.Supports != "presupported" {
		t.Errorf("frog = %#v (all: %v)", frog, res)
	}
	if fiona := res["Atlas/Foxy Fierce Fiona"]; len(fiona.Files) != 2 {
		t.Errorf("fiona = %#v", fiona)
	}
	isla := res["Bastet/Sisterhood/Isla"]
	if len(isla.Files) != 2 || !strings.Contains(strings.Join(isla.Files, " "), "[120mm]") {
		t.Errorf("isla = %#v", isla)
	}
	if only := res["Bite the Bullet/Lonely/Only Model"]; len(only.Files) != 2 {
		t.Errorf("only = %#v", only)
	}
	if len(res) != 7 {
		t.Errorf("models = %d: %v", len(res), res)
	}
}

func TestUserCorrections(t *testing.T) {
	es := entries(
		"Creator/Pack/Orc/orc.stl",
		"Creator/Pack/Elf/elf.stl",
		"Creator/Pack/pack.zip",
		"Creator/Chibis/Jinx/a.stl",
		"Creator/Chibis/Vi/b.stl",
		"Creator/Line/Pre-Supported/Knight/k_sup.stl",
		"Creator/Line/Knight/k.stl",
	)
	r := defaultRules()
	res := summarize(es, Detect(es, r))
	if len(res) != 6 { // Orc, Elf, pack.zip, Jinx, Vi, Knight (merged trees)
		t.Fatalf("baseline = %d: %v", len(res), res)
	}

	r.IsModel = map[string]bool{"Creator/Pack": true}
	r.NotModel = map[string]bool{"Creator/Chibis/Jinx": true}
	r.Detached = map[string]bool{"Creator/Line/Pre-Supported/Knight": true}
	res = summarize(es, Detect(es, r))
	if p := res["Creator/Pack"]; len(p.Files) != 3 {
		t.Errorf("pack (is_model) = %#v", p)
	}
	if c := res["Creator/Chibis"]; len(c.Files) != 1 {
		t.Errorf("chibis (jinx not a model) = %#v", c)
	}
	if _, ok := res["Creator/Chibis/Vi"]; !ok {
		t.Errorf("vi should stay: %v", res)
	}
	if _, ok := res["Creator/Line/Pre-Supported/Knight"]; !ok {
		t.Errorf("detached tree merged again: %v", res)
	}
	if k := res["Creator/Line/Knight"]; len(k.Files) != 1 {
		t.Errorf("knight = %#v", k)
	}
}
