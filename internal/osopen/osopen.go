// Package osopen opens folders / reveals files in the system file manager (H5).
package osopen

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// Folder opens dir in the file manager.
func Folder(dir string) error {
	switch runtime.GOOS {
	case "windows":
		// explorer.exe returns exit code 1 even on success; ignore Wait's result.
		return exec.Command("explorer", filepath.Clean(dir)).Start()
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// Reveal shows file selected in its folder where supported, otherwise opens the folder.
func Reveal(file string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("explorer", "/select,", filepath.Clean(file)).Start()
	}
	return Folder(filepath.Dir(file))
}
