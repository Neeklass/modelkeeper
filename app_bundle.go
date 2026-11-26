package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"modelkeeper/internal/bundle"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/pathutil"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Events of a running zip export.
const (
	EventBundleProgress = "bundle:progress"
	EventBundleDone     = "bundle:done"
)

// BundleStart describes a started zip export.
type BundleStart struct {
	Path    string `json:"path"` // "" = cancelled in the dialog
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	Skipped int    `json:"skipped"` // files of offline folders or missing
}

// BundleDone is sent when the export ends.
type BundleDone struct {
	Path  string   `json:"path"`
	Error *msg.Msg `json:"error,omitempty"`
}

var bundleMu sync.Mutex
var bundleCancel context.CancelFunc

// ExportCollectionZip writes the files of a collection's models into a zip
// file the user chooses (model files are only read). It runs in the
// background; progress and the end come as events.
func (a *App) ExportCollectionZip(collectionID int64) (BundleStart, error) {
	if err := a.ready(); err != nil {
		return BundleStart{}, err
	}
	var name string
	if err := a.db.QueryRowContext(a.ctx, `SELECT name FROM collection WHERE id = ?`, collectionID).Scan(&name); err != nil {
		return BundleStart{}, msg.Wrap(err, "collections.notFound")
	}
	rows, err := a.db.QueryContext(a.ctx, `SELECT model_id FROM collection_model WHERE collection_id = ? ORDER BY position`, collectionID)
	if err != nil {
		return BundleStart{}, wrapDB(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	return a.exportZip(ids, name)
}

// ExportModelsZip writes the files of the given models into a zip file.
func (a *App) ExportModelsZip(ids []int64) (BundleStart, error) {
	if err := a.ready(); err != nil {
		return BundleStart{}, err
	}
	return a.exportZip(ids, "modelkeeper-models")
}

// CancelZipExport stops a running zip export (the incomplete file is removed).
func (a *App) CancelZipExport() {
	bundleMu.Lock()
	defer bundleMu.Unlock()
	if bundleCancel != nil {
		bundleCancel()
	}
}

func (a *App) exportZip(ids []int64, name string) (BundleStart, error) {
	bundleMu.Lock()
	busy := bundleCancel != nil
	bundleMu.Unlock()
	if busy {
		return BundleStart{}, msg.Wrap(nil, "bundle.busy")
	}
	if len(ids) == 0 {
		return BundleStart{}, msg.Wrap(nil, "bundle.empty")
	}
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: strings.Trim(unsafeName.ReplaceAllString(name, "-"), "-") + "-" + time.Now().Format("2006-01-02") + ".zip",
		Filters:         []runtime.FileFilter{{DisplayName: "Zip", Pattern: "*.zip"}},
	})
	if err != nil || file == "" {
		return BundleStart{}, err
	}
	if !strings.HasSuffix(strings.ToLower(file), ".zip") {
		file += ".zip"
	}
	// Never into a model folder or the data folder (J1).
	roots, err := catalog.ListRoots(a.ctx, a.db)
	if err != nil {
		return BundleStart{}, wrapDB(err)
	}
	for _, r := range roots {
		if pathutil.Within(file, r.Path) {
			return BundleStart{}, msg.Wrap(nil, "bundle.inLibrary", "path", r.Path)
		}
	}
	if pathutil.Within(file, a.layout.Root) {
		return BundleStart{}, msg.Wrap(nil, "bundle.inLibrary", "path", a.layout.Root)
	}
	plan, err := bundle.Build(a.ctx, a.db, ids)
	if err != nil {
		return BundleStart{}, wrapDB(err)
	}
	if len(plan.Files) == 0 {
		return BundleStart{}, msg.Wrap(nil, "bundle.noFiles")
	}
	ctx, cancel := context.WithCancel(context.Background())
	bundleMu.Lock()
	bundleCancel = cancel
	bundleMu.Unlock()
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		defer func() {
			bundleMu.Lock()
			bundleCancel = nil
			bundleMu.Unlock()
			cancel()
		}()
		go func() { // the app is closing: stop
			select {
			case <-a.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		err := bundle.Write(ctx, plan, file, func(p bundle.Progress) {
			runtime.EventsEmit(a.ctx, EventBundleProgress, p)
		})
		done := BundleDone{Path: file}
		if err != nil {
			m := msg.New("bundle.failed", "detail", err.Error())
			if errors.Is(err, context.Canceled) {
				m = msg.New("bundle.cancelled")
			}
			done.Error = &m
			log.Printf("zip export %s: %v", file, err)
		}
		runtime.EventsEmit(a.ctx, EventBundleDone, done)
	}()
	return BundleStart{Path: file, Files: len(plan.Files), Bytes: plan.Bytes, Skipped: plan.Skipped}, nil
}
