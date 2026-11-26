package thumbs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const cubeOBJ = `# cube
v 0 0 0
v 10 0 0
v 10 10 0
v 0 10 0
v 0 0 10
v 10 0 10
v 10 10 10
v 0 10 10
f 1 2 3 4
f 5 8 7 6
f 1 5 6 2
f 2/1 6/1 7/1 3/1
f 3//1 7//1 8//1 4//1
f -8 -4 -1 -5
`

const cube3MF = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
 <resources><object id="1" type="model"><mesh>
  <vertices>
   <vertex x="0" y="0" z="0"/><vertex x="10" y="0" z="0"/><vertex x="10" y="10" z="0"/><vertex x="0" y="10" z="0"/>
   <vertex x="0" y="0" z="10"/><vertex x="10" y="0" z="10"/><vertex x="10" y="10" z="10"/><vertex x="0" y="10" z="10"/>
  </vertices>
  <triangles>
   <triangle v1="0" v2="1" v3="2"/><triangle v1="0" v2="2" v3="3"/>
   <triangle v1="4" v2="6" v3="5"/><triangle v1="4" v2="7" v3="6"/>
   <triangle v1="0" v2="4" v3="5"/><triangle v1="0" v2="5" v3="1"/>
   <triangle v1="1" v2="5" v3="6"/><triangle v1="1" v2="6" v3="2"/>
   <triangle v1="2" v2="6" v3="7"/><triangle v1="2" v2="7" v3="3"/>
   <triangle v1="3" v2="7" v3="4"/><triangle v1="3" v2="4" v3="0"/>
   <triangle v1="0" v2="1" v3="99"/>
  </triangles>
 </mesh></object></resources>
 <build><item objectid="1"/></build>
</model>`

func TestRenderOBJAnd3MF(t *testing.T) {
	tris, err := parseOBJ([]byte(cubeOBJ))
	if err != nil {
		t.Fatal(err)
	}
	if len(tris) != 12 {
		t.Errorf("obj: %d triangles", len(tris))
	}
	if _, err := parseOBJ([]byte("v 0 0 0\nf 1 2 3\n")); err == nil {
		t.Error("missing vertex accepted")
	}
	data := zipBytes(map[string][]byte{"3D/3dmodel.model": []byte(cube3MF)})
	tris3, err := parse3MF(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(tris3) != 12 {
		t.Errorf("3mf: %d triangles (a bad index is skipped)", len(tris3))
	}
	// A 3MF without preview image is drawn from its mesh.
	if _, err := embedded3MF(data); !errors.Is(err, errNoSource) {
		t.Fatalf("embedded: %v", err)
	}
	for name, tr := range map[string][][3]vec{"obj": tris, "3mf": tris3} {
		img, err := renderMesh(tr, 96)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n := distinctColors(img); n < 3 {
			t.Errorf("%s: only %d colors", name, n)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.3mf"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := load(Candidate{Kind: KindEmbedded, ext: "3mf", rootPath: dir, path: "x.3mf"})
	if err != nil || img == nil {
		t.Errorf("load 3mf mesh: %v", err)
	}
}
