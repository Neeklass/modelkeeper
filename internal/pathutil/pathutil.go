// Package pathutil compares absolute folder paths the way the file system
// does: case-insensitive on Windows, ignoring trailing separators.
package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// Normalize returns a comparison key for an absolute path: cleaned, without
// trailing separators and, on Windows, lowercase. It is not meant to be used
// as a path again (a drive root becomes "c:").
func Normalize(p string) string {
	p = strings.TrimRight(filepath.Clean(p), `\/`)
	if os.PathSeparator == '\\' {
		p = strings.ToLower(p)
	}
	return p
}

// Same reports whether a and b name the same folder.
func Same(a, b string) bool { return Normalize(a) == Normalize(b) }

// Within reports whether p is dir or lies below it.
func Within(p, dir string) bool {
	np, nd := Normalize(p), Normalize(dir)
	return np == nd || strings.HasPrefix(np, nd+string(os.PathSeparator))
}

// Overlap reports whether a equals b or one contains the other.
func Overlap(a, b string) bool { return Within(a, b) || Within(b, a) }
