// Package opencopy opens model files in another program (a slicer) through a
// copy in the data folder, so that program can never change, overwrite or
// lock the original (J1, H5).
//
// Every open makes a fresh, read-only copy in its own folder:
//
//	data/open/<location>-<unix ms>/<file name>
//
// Copies older than Keep are removed by Cleanup. Only the copy itself is
// removed: anything else the user saved into such a folder (e.g. a slicer
// project) stays.
package opencopy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Keep is how long copies are kept.
const Keep = 7 * 24 * time.Hour

// marker names the copy inside its folder, so Cleanup removes only that file.
const marker = ".modelkeeper-copy"

// Write copies r into a new folder below dir and returns the copy's path.
// The copy is written to a temporary name first and marked read-only, so a
// program never sees a half-written file and cannot save over it by mistake.
func Write(dir string, locationID int64, name string, r io.Reader, now time.Time) (string, error) {
	name = cleanName(name)
	sub := filepath.Join(dir, strconv.FormatInt(locationID, 10)+"-"+strconv.FormatInt(now.UnixMilli(), 10))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(sub, ".part-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		os.Remove(sub)
		return "", err
	}
	dst := filepath.Join(sub, name)
	if err := os.WriteFile(filepath.Join(sub, marker), []byte(name), 0o644); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(dst, 0o444); err != nil {
		return "", err
	}
	os.Chtimes(sub, now, now) // the folder's age decides the cleanup
	return dst, nil
}

// Cleanup removes copies older than Keep and returns how many it removed.
// Folders are only touched if they carry the marker; files the user added
// stay, and a folder is only removed when it is empty afterwards. Copies
// still open in a program (locked on Windows) are tried again next time.
func Cleanup(dir string, now time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		fi, err := os.Stat(sub)
		if err != nil || now.Sub(fi.ModTime()) < Keep {
			continue
		}
		name, err := os.ReadFile(filepath.Join(sub, marker))
		if err != nil {
			continue // not ours
		}
		copyPath := filepath.Join(sub, cleanName(string(name)))
		os.Chmod(copyPath, 0o644) // read-only files cannot be removed on Windows
		if err := os.Remove(copyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			os.Chmod(copyPath, 0o444)
			continue
		}
		removed++
		os.Remove(filepath.Join(sub, marker))
		os.Remove(sub) // fails if the user saved something there: it stays
	}
	return removed, nil
}

// cleanName returns a safe file name: no folders, no characters Windows
// does not allow.
func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, ". ")
	if name == "" || name == "." || name == ".." {
		name = "model"
	}
	return name
}
