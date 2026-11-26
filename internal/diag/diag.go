// Package diag writes the diagnostics archive (TODO 5.4): what is needed to
// understand a problem, without the cataloging work itself. It holds the log,
// the settings and the app's status files, and a system.json with versions,
// sizes and the root folders; never the catalog, the backups, thumbnails or
// any model file.
package diag

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxFileBytes: of a larger file (a long log) only the end is included.
const MaxFileBytes = 4 << 20

// Input is what goes into the archive.
type Input struct {
	// DataDir: its top-level *.json files and sync/state.json are included
	// (settings, health report, restore test, sync state).
	DataDir string
	// Log is the log file (its last MaxFileBytes).
	Log string
	// System is written as system.json.
	System any
	Now    time.Time
}

// excluded: files never included, even if they end in .json.
var excluded = map[string]bool{"modelkeeper-export.json": true}

// Write writes the zip archive to w and returns the names it contains.
func Write(w io.Writer, in Input) ([]string, error) {
	z := zip.NewWriter(w)
	var names []string
	add := func(name string, b []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: in.Now}
		f, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		names = append(names, name)
		_, err = f.Write(b)
		return err
	}
	var files []string
	if in.DataDir != "" {
		if entries, err := os.ReadDir(in.DataDir); err == nil {
			for _, e := range entries {
				n := e.Name()
				if !e.IsDir() && strings.EqualFold(filepath.Ext(n), ".json") && !excluded[strings.ToLower(n)] {
					files = append(files, n)
				}
			}
		}
		files = append(files, filepath.Join("sync", "state.json"))
	}
	sort.Strings(files)
	for _, n := range files {
		b, err := readTail(filepath.Join(in.DataDir, n))
		if err != nil {
			continue
		}
		if err := add(filepath.ToSlash(n), b); err != nil {
			return names, err
		}
	}
	if in.Log != "" {
		if b, err := readTail(in.Log); err == nil {
			if err := add(filepath.Base(in.Log), b); err != nil {
				return names, err
			}
		}
	}
	if in.System != nil {
		b, err := json.MarshalIndent(in.System, "", "  ")
		if err != nil {
			return names, err
		}
		if err := add("system.json", b); err != nil {
			return names, err
		}
	}
	readme := "modelkeeper diagnostics, " + in.Now.Format(time.RFC3339) + "\n\n" +
		"Contains the log, the settings and status files of the app and system.json\n" +
		"(versions, sizes, root folders). It does not contain the catalog, backups,\n" +
		"thumbnails or any model file.\n"
	if err := add("README.txt", []byte(readme)); err != nil {
		return names, err
	}
	return names, z.Close()
}

func readTail(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > MaxFileBytes {
		if _, err := f.Seek(-MaxFileBytes, io.SeekEnd); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}
