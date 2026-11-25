package appdir

import (
	"os"
	"path/filepath"
	"strings"
)

// Places where the data folder would be lost or damaged without warning.
const (
	// RiskTemp: a temporary folder, e.g. the .exe was started from inside a
	// zip file in Explorer (Windows extracts it to %TEMP%). The catalog would
	// vanish when the temporary files are cleaned up. The app refuses to start.
	RiskTemp = "temp"
	// RiskDownloads: the Downloads folder, which people tidy up.
	RiskDownloads = "downloads"
	// RiskSynced: a folder a sync client (OneDrive, Dropbox, Google Drive,
	// iCloud) works on: it may replace catalog.db while it is open, which
	// damages SQLite databases.
	RiskSynced = "synced"
)

// Risk returns why the data folder dir is in a bad place (one of the Risk
// constants), or "" if it is fine.
func Risk(dir string) string {
	return risk(dir, tempDirs(), os.Getenv("USERPROFILE"), os.Getenv("HOME"))
}

func tempDirs() []string {
	var out []string
	for _, v := range []string{os.TempDir(), os.Getenv("TEMP"), os.Getenv("TMP")} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func key(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	return strings.ToLower(strings.TrimRight(p, "/")) + "/"
}

func risk(dir string, temps []string, homes ...string) string {
	d := key(dir)
	for _, t := range temps {
		if t != "" && strings.HasPrefix(d, key(t)) {
			return RiskTemp
		}
	}
	// Explorer's "open the zip and double-click" and 7-Zip's equivalents.
	for _, marker := range []string{"/appdata/local/temp/", "/temp/rar$", "/temp/7z", "/temp1_"} {
		if strings.Contains(d, marker) {
			return RiskTemp
		}
	}
	for _, h := range homes {
		if h != "" && strings.HasPrefix(d, key(filepath.Join(h, "Downloads"))) {
			return RiskDownloads
		}
	}
	for _, s := range []string{"/onedrive/", "/onedrive - ", "/dropbox/", "/google drive/", "/my drive/",
		"/icloud drive/", "/iclouddrive/", "/nextcloud/", "/library/mobile documents/"} {
		if strings.Contains(d, s) {
			return RiskSynced
		}
	}
	return ""
}
