package settings

import (
	"os"
	"path/filepath"
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
