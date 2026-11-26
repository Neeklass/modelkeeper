package appdir

import (
	"path/filepath"
	"testing"
)

func TestRisk(t *testing.T) {
	tmp := filepath.FromSlash("/tmp/x")
	home := filepath.FromSlash("/home/jana")
	for dir, want := range map[string]string{
		"/tmp/x/Temp1_modelkeeper.zip/data":                   RiskTemp,
		"/tmp/X/data":                                         RiskTemp, // case-insensitive (Windows)
		"/users/jana/AppData/Local/Temp/Rar$EXa1.2/data":      RiskTemp,
		"/home/jana/Downloads/modelkeeper/data":               RiskDownloads,
		"/home/jana/OneDrive/modelkeeper/data":                RiskSynced,
		"/home/jana/OneDrive - Firma/modelkeeper/data":        RiskSynced,
		"/home/jana/Dropbox/modelkeeper/data":                 RiskSynced,
		"/home/jana/modelkeeper/data":                         "",
		"/home/jana/Downloadsfolder-is-not-downloads/data":    "",
		"/home/jana/Documents/Dropboxes-are-not-dropbox/data": "",
	} {
		if got := risk(filepath.FromSlash(dir), []string{tmp}, home); got != want {
			t.Errorf("%s: %q, want %q", dir, got, want)
		}
	}
}

func TestUsable(t *testing.T) {
	dir := t.TempDir()
	if !usable(filepath.Join(dir, "data")) {
		t.Error("a new folder in a writable folder is usable")
	}
	if usable(filepath.Join(dir, "missing", "deeper", "data")) {
		t.Error("a folder whose parent does not exist is not usable")
	}
	if got := userDataDir(); !filepath.IsAbs(got) || filepath.Base(got) != "modelkeeper" {
		t.Errorf("user data dir %q", got)
	}
}
