package main

import (
	"errors"
	"log"
	"runtime"
	"time"

	"modelkeeper/internal/archive"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/opencopy"
	"modelkeeper/internal/osopen"
	"modelkeeper/internal/preview"
	"modelkeeper/internal/settings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// OpenInSlicer opens a file of a model in the slicer (H5). The slicer gets a
// fresh, read-only copy in data/open, never the original: no program can
// change, overwrite or lock the file in the library (J1). Files inside
// archives are copied out of the archive the same way.
func (a *App) OpenInSlicer(locationID int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	src, err := preview.LookupAny(a.ctx, a.db, locationID)
	if err != nil {
		return openError(err, src.Name)
	}
	f, _, err := src.Open()
	if err != nil {
		return openError(err, src.Name)
	}
	defer f.Close()
	p, err := opencopy.Write(a.layout.Open, locationID, src.Name, f, time.Now())
	if err != nil {
		return msg.Wrap(err, "error.openCopy", "name", src.Name)
	}
	if exe := a.settings.Get().SlicerPath; exe != "" {
		err = osopen.With(exe, p)
	} else {
		err = osopen.File(p)
	}
	if err != nil {
		return msg.Wrap(err, "error.openProgram", "name", src.Name)
	}
	return nil
}

func openError(err error, name string) error {
	switch {
	case errors.Is(err, preview.ErrOffline):
		return msg.Wrap(nil, "error.fileOffline", "name", name)
	case errors.Is(err, preview.ErrNotFound):
		return msg.Wrap(nil, "error.fileNotFound", "name", name)
	case errors.Is(err, preview.ErrTooLarge):
		return msg.Wrap(nil, "error.fileTooLargeToCopy", "name", name)
	case errors.Is(err, archive.ErrEncrypted):
		return msg.Wrap(nil, "error.fileEncrypted", "name", name)
	}
	return msg.Wrap(err, "error.openCopy", "name", name)
}

// ChooseSlicer lets the user pick the slicer program for OpenInSlicer.
// Returns the chosen path, or "" if the dialog was cancelled.
func (a *App) ChooseSlicer() (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	opts := wruntime.OpenDialogOptions{Title: "Slicer"}
	if runtime.GOOS == "windows" {
		opts.Filters = []wruntime.FileFilter{{DisplayName: "Programs (*.exe)", Pattern: "*.exe"}}
	}
	file, err := wruntime.OpenFileDialog(a.ctx, opts)
	if err != nil || file == "" {
		return "", err
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.SlicerPath = file }); err != nil {
		return "", msg.Wrap(err, "error.settingsSave")
	}
	return file, nil
}

// ClearSlicer makes OpenInSlicer use the program the system associates with
// the file type again.
func (a *App) ClearSlicer() error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.SlicerPath = "" }); err != nil {
		return msg.Wrap(err, "error.settingsSave")
	}
	return nil
}

// cleanupOpenCopies removes old slicer copies (run in the background at start).
func (a *App) cleanupOpenCopies() {
	n, err := opencopy.Cleanup(a.layout.Open, time.Now())
	if err != nil {
		log.Printf("open copies: %v", err)
	} else if n > 0 {
		log.Printf("open copies: %d old copies removed", n)
	}
}
