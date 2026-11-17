package osopen

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// File opens a file with the program Windows associates with its type.
func File(path string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}
