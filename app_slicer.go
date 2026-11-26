package main

import (
	"bytes"
	"errors"
	"io"

	"modelkeeper/internal/msg"
	"modelkeeper/internal/preview"
	"modelkeeper/internal/slicer"
)

// GetSlicerInfo reads the print information of a sliced file (L2): print
// time, layers, material, exposure. Only the needed parts are read; the file
// is not changed. Error "slicer.none" if the file carries none.
func (a *App) GetSlicerInfo(locationID int64) (slicer.Info, error) {
	if err := a.ready(); err != nil {
		return slicer.Info{}, err
	}
	src, err := preview.LookupAny(a.ctx, a.db, locationID)
	if err != nil {
		return slicer.Info{}, msg.Wrap(err, "slicer.unreadable")
	}
	if !slicer.Exts[src.Ext] {
		return slicer.Info{}, msg.Wrap(nil, "slicer.none")
	}
	f, _, err := src.Open()
	if err != nil {
		return slicer.Info{}, msg.Wrap(err, "slicer.unreadable")
	}
	defer f.Close()
	ra, ok := f.(io.ReaderAt)
	if !ok { // not a file on disk: read it (archive members are small enough, preview limits)
		b, err := io.ReadAll(f)
		if err != nil {
			return slicer.Info{}, msg.Wrap(err, "slicer.unreadable")
		}
		ra = bytes.NewReader(b)
	}
	info, err := slicer.Read(ra, src.Size, src.Ext)
	if errors.Is(err, slicer.ErrUnsupported) {
		return info, msg.Wrap(nil, "slicer.none")
	}
	if err != nil {
		return info, msg.Wrap(err, "slicer.unreadable")
	}
	return info, nil
}
