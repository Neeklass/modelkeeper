package appdir

import (
	"path/filepath"
	"strings"
)

// Moving the app (concept M). The data folder is self-contained: snapshots
// are found by name, thumbnails by file name, so the whole modelkeeper folder
// (program and data) can be moved or copied as it is. What can break are the
// root folders, which hold absolute paths: on a portable disk whose drive
// letter changed (E: → F:), or for model folders next to the app folder.
//
// MovedRoot guesses where a root folder went: the data folder moved from
// oldData to newData; their common end (e.g. "modelkeeper\data") is cut off,
// which leaves the part that changed (E:\ → F:\, or Desktop → Tools). A root
// below the old part is assumed below the new one. The caller only uses the
// guess if the old path is gone and the new one exists.
func MovedRoot(oldData, newData, root string) (string, bool) {
	oldBase, newBase := changedParts(filepath.Clean(oldData), filepath.Clean(newData))
	if oldBase == "" || newBase == "" || samePath(oldBase, newBase) {
		return "", false
	}
	root = filepath.Clean(root)
	rel, ok := below(root, oldBase)
	if !ok {
		return "", false
	}
	if rel == "" {
		return newBase, true
	}
	return filepath.Join(newBase, rel), true
}

func samePath(a, b string) bool { return key(a) == key(b) }

// below returns root relative to base if root is base or lies below it.
func below(root, base string) (string, bool) {
	r, b := key(root), key(base)
	if !strings.HasPrefix(r, b) {
		return "", false
	}
	bt := strings.TrimRight(filepath.ToSlash(base), "/")
	rs := filepath.ToSlash(root)
	if len(rs) < len(bt) {
		return "", false
	}
	return filepath.FromSlash(strings.Trim(rs[len(bt):], "/")), true
}

// changedParts strips the common trailing path elements of a and b.
func changedParts(a, b string) (string, string) {
	for {
		ba, bb := filepath.Base(a), filepath.Base(b)
		da, db := filepath.Dir(a), filepath.Dir(b)
		if !strings.EqualFold(ba, bb) || da == a || db == b {
			return a, b
		}
		a, b = da, db
	}
}
