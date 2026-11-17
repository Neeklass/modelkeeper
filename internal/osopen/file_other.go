//go:build !windows

package osopen

import (
	"os/exec"
	"runtime"
)

// File opens a file with the program the desktop associates with its type.
func File(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}
