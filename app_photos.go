package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/thumbs"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Photos of printed results (L1). They are stored resized in the catalog;
// the picked files are only read.

// AddPrintPhotos lets the user pick photos and adds them to a print log
// entry. Returns the number added (0 if the dialog was cancelled).
func (a *App) AddPrintPhotos(printID int64) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	files, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Filters: []runtime.FileFilter{{DisplayName: "Images (*.jpg;*.png;*.webp)", Pattern: "*.jpg;*.jpeg;*.png;*.webp"}},
	})
	if err != nil || len(files) == 0 {
		return 0, err
	}
	n := 0
	for _, f := range files {
		data, w, h, err := thumbs.Photo(f)
		if err != nil {
			return n, msg.Wrap(err, "prints.photoUnreadable", "file", f)
		}
		if _, err := catalog.AddPrintPhoto(a.ctx, a.db, printID, data, w, h); err != nil {
			return n, wrapDB(err)
		}
		n++
	}
	return n, nil
}

// DeletePrintPhoto removes a photo of a print log entry.
func (a *App) DeletePrintPhoto(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.DeletePrintPhoto(a.ctx, a.db, id))
}

// UsePhotoAsThumbnail makes a print photo the model's thumbnail (manual).
func (a *App) UsePhotoAsThumbnail(photoID int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	data, modelID, err := catalog.PrintPhoto(a.ctx, a.db, photoID)
	if err != nil {
		return wrapDB(err)
	}
	img, err := thumbs.DecodeBytes(data)
	if err != nil {
		return msg.Wrap(err, "thumbs.failed")
	}
	if err := a.thumbStore().SetFromImage(a.ctx, modelID, img); err != nil {
		return msg.Wrap(err, "thumbs.failed")
	}
	return nil
}

// servePhoto serves /photos/<id>.jpg from the catalog.
func (h assetHandler) servePhoto(w http.ResponseWriter, r *http.Request, name string) {
	a := h.a
	id, err := strconv.ParseInt(strings.TrimSuffix(name, ".jpg"), 10, 64)
	if err != nil || id <= 0 || !a.started() || a.db == nil {
		http.NotFound(w, r)
		return
	}
	data, _, err := catalog.PrintPhoto(r.Context(), a.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// A photo never changes (a new one gets a new id).
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data)
}
