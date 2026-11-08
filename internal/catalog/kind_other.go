//go:build !windows

package catalog

import "strings"

// DetectKind returns "nas" for typical network mount points, else "local".
// Linux support is best effort for now (A2).
func DetectKind(path string) string {
	for _, p := range []string{"/mnt/", "/media/", "/run/user/"} {
		if strings.HasPrefix(path, p) && strings.Contains(path, "smb") {
			return "nas"
		}
	}
	return "local"
}
