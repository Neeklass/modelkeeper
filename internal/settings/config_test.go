package settings

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigRoundTrip(t *testing.T) {
	s := Defaults()
	s.Language, s.SlicerPath, s.DataDir = "de", `C:\Chitubox\chitubox.exe`, `C:\modelkeeper\data`
	s.PartNames = append(s.PartNames, "Teile")
	file := filepath.Join(t.TempDir(), "config.json")
	c := NewConfig(s, []ConfigRoot{{Path: `\\nas\3D-Modelle`, Kind: "nas", CreatorLevel: true}}, "v1", time.Now())
	if err := WriteConfigFile(file, c); err != nil {
		t.Fatal(err)
	}
	got, err := ReadConfigFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.Language != "de" || got.Settings.SlicerPath != s.SlicerPath || got.Settings.DataDir != "" {
		t.Errorf("settings %+v", got.Settings)
	}
	if !strings.Contains(strings.Join(got.Settings.PartNames, ","), "teile") && !strings.Contains(strings.Join(got.Settings.PartNames, ","), "Teile") {
		t.Errorf("part names %v", got.Settings.PartNames)
	}
	if len(got.Roots) != 1 || got.Roots[0].Path != `\\nas\3D-Modelle` {
		t.Errorf("roots %+v", got.Roots)
	}
}

func TestConfigRejects(t *testing.T) {
	for name, in := range map[string]string{
		"no json":      "x",
		"other":        `{"format": "something"}`,
		"newer":        `{"format": "modelkeeper-config", "formatVersion": 9}`,
		"bad filetype": `{"format": "modelkeeper-config", "formatVersion": 1, "settings": {"fileTypes": {"model": ["stl"], "slicer": ["stl"]}}}`,
	} {
		if _, err := DecodeConfig(strings.NewReader(in)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Missing settings get the defaults.
	c, err := DecodeConfig(strings.NewReader(`{"format": "modelkeeper-config", "formatVersion": 1}`))
	if err != nil || len(c.Settings.FileTypes.Model) == 0 || c.Roots == nil {
		t.Errorf("minimal config: %+v, %v", c, err)
	}
}
