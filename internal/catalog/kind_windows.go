package catalog

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// DetectKind returns "nas" for UNC paths and mapped network drives, else "local".
func DetectKind(path string) string {
	if strings.HasPrefix(path, `\\`) {
		return "nas"
	}
	vol := filepath.VolumeName(path)
	if vol == "" {
		return "local"
	}
	p, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return "local"
	}
	if windows.GetDriveType(p) == windows.DRIVE_REMOTE {
		return "nas"
	}
	return "local"
}
