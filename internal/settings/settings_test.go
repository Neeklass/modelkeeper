package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNASRescanDefault(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		json string
		want int
	}{
		"older file without the field": {`{"version":1,"language":"de"}`, 24},
		"switched off":                 {`{"version":1,"nasRescanHours":0}`, 0},
		"weekly":                       {`{"version":1,"nasRescanHours":168}`, 168},
	} {
		p := filepath.Join(dir, name+".json")
		os.WriteFile(p, []byte(c.json), 0o644)
		s, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Get().NASRescanHours; got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
}

func TestNormalizeFileTypes(t *testing.T) {
	ft, err := NormalizeFileTypes(FileTypes{Model: []string{" .STL", "obj", "stl", ""}, Archive: []string{"zip"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ft.Model) != 2 || ft.Model[0] != "stl" || ft.Model[1] != "obj" || ft.Slicer == nil {
		t.Errorf("normalized: %+v", ft)
	}
	for name, in := range map[string]FileTypes{
		"twice":    {Model: []string{"stl"}, Supplementary: []string{".stl"}},
		"invalid":  {Model: []string{"st l"}},
		"no model": {Archive: []string{"zip"}},
	} {
		if _, err := NormalizeFileTypes(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if added := (FileTypes{Model: []string{"stl", "obj"}}).Added(FileTypes{Model: []string{"stl"}}); len(added) != 1 || added[0] != "obj" {
		t.Errorf("added: %v", added)
	}
}

func TestNormalizeList(t *testing.T) {
	got, err := NormalizeList([]string{" Supported ", "supported", "", "Parts"})
	if err != nil || len(got) != 2 || got[0] != "Supported" || got[1] != "Parts" {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestFileTypesChanged(t *testing.T) {
	prev := FileTypes{Model: []string{"stl", "obj"}, Slicer: []string{"lys"}, Supplementary: []string{"txt"}}
	next := FileTypes{Model: []string{"stl", "ply"}, Supplementary: []string{"lys", "txt"}}
	if got := strings.Join(next.Changed(prev), ","); got != "lys,obj" {
		t.Errorf("Changed = %s, want lys (moved), obj (removed); ply is only added", got)
	}
	if got := prev.Changed(prev); len(got) != 0 {
		t.Errorf("no change: %v", got)
	}
}
