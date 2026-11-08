// Package appdir resolves and prepares the portable data folder that lives
// next to the executable (concept section M).
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvOverride lets developers and tests point the app at another data folder.
const EnvOverride = "MODELKEEPER_DATA"

// Layout describes the files and folders inside the data folder.
type Layout struct {
	Root       string // .../data
	Catalog    string // .../data/catalog.db
	Settings   string // .../data/settings.json
	Thumbnails string // .../data/thumbnails
	Log        string // .../data/modelkeeper.log
}

// Resolve returns the data folder layout: <dir of exe>/data, unless
// MODELKEEPER_DATA is set.
func Resolve() (Layout, error) {
	dir := os.Getenv(EnvOverride)
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return Layout{}, fmt.Errorf("locate executable: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir = filepath.Join(filepath.Dir(exe), "data")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Layout{}, err
	}
	return NewLayout(abs), nil
}

// NewLayout builds the layout for a given data folder.
func NewLayout(root string) Layout {
	return Layout{
		Root:       root,
		Catalog:    filepath.Join(root, "catalog.db"),
		Settings:   filepath.Join(root, "settings.json"),
		Thumbnails: filepath.Join(root, "thumbnails"),
		Log:        filepath.Join(root, "modelkeeper.log"),
	}
}

// Ensure creates the folders and verifies that the data folder is writable
// (it must not live under e.g. C:\Program Files).
func (l Layout) Ensure() error {
	for _, d := range []string{l.Root, l.Thumbnails} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	probe, err := os.CreateTemp(l.Root, ".write-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}

// maxLogSize: a larger log is cut to its last part at startup.
const maxLogSize = 5 << 20

// OpenLog opens the log file for appending, keeping only the last ~1 MB if
// it grew beyond maxLogSize.
func (l Layout) OpenLog() (*os.File, error) {
	if fi, err := os.Stat(l.Log); err == nil && fi.Size() > maxLogSize {
		if b, err := os.ReadFile(l.Log); err == nil {
			os.WriteFile(l.Log, b[len(b)-(1<<20):], 0o644)
		}
	}
	return os.OpenFile(l.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
