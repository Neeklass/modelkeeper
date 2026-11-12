package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithin(t *testing.T) {
	sep := string(filepath.Separator)
	root := sep + "m" + sep + "3D"
	for p, want := range map[string]bool{
		root:                    true,
		root + sep:              true,
		root + sep + "a":        true,
		root + sep + "a" + sep:  true,
		sep + "m" + sep + "3DX": false,
		sep + "m":               false,
	} {
		if got := Within(p, root); got != want {
			t.Errorf("Within(%q) = %v, want %v", p, got, want)
		}
	}
	if !Overlap(sep+"m", root) || Overlap(root, sep+"m"+sep+"3DX") {
		t.Error("Overlap")
	}
}

func TestSameIgnoresCaseAndTrailingSlash(t *testing.T) {
	a, b := `C:\Models\3D`, `c:\models\3d\`
	if os.PathSeparator != '\\' {
		a, b = "/Models/3D", "/Models/3D/"
	}
	if !Same(a, b) {
		t.Errorf("Same(%q, %q) = false", a, b)
	}
	if os.PathSeparator == '\\' && !Within(`C:\MODELS\3d\x`, `c:\models\3D`) {
		t.Error("Within must ignore case on Windows")
	}
}
