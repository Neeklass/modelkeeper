package main

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"modelkeeper/internal/appdir"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/db"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/osopen"
	"modelkeeper/internal/scanner"
	"modelkeeper/internal/settings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// EventScanProgress carries scanner.Progress to the frontend.
const EventScanProgress = "scan:progress"

// App is bound to the frontend. Bound methods stay thin and delegate to
// internal packages. Errors returned to the frontend are *msg.Error.
type App struct {
	ctx      context.Context
	layout   appdir.Layout
	settings *settings.Store
	db       *sql.DB
	scans    *scanner.Manager

	// startupErr is set when the data folder or catalog could not be opened;
	// the frontend shows it instead of the main UI.
	startupErr *msg.Msg
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.init(); err != nil {
		m := msg.From(err)
		a.startupErr = &m
		log.Printf("startup failed: %v", err)
	}
}

func (a *App) init() error {
	layout, err := appdir.Resolve()
	if err != nil {
		return msg.Wrap(err, "error.dataDirResolve")
	}
	a.layout = layout
	if err := layout.Ensure(); err != nil {
		return msg.Wrap(err, "error.dataDirNotWritable", "path", layout.Root)
	}
	st, err := settings.Load(layout.Settings)
	if err != nil {
		return msg.Wrap(err, "error.settingsLoad", "path", layout.Settings)
	}
	a.settings = st
	conn, err := db.Open(layout.Catalog)
	if err != nil {
		return msg.Wrap(err, "error.catalogOpen", "path", layout.Catalog)
	}
	a.db = conn
	scans, err := scanner.NewManager(conn, st.Get, func(p scanner.Progress) {
		runtime.EventsEmit(a.ctx, EventScanProgress, p)
	}, scanner.Options{})
	if err != nil {
		return msg.Wrap(err, "error.catalogOpen", "path", layout.Catalog)
	}
	a.scans = scans
	return nil
}

func (a *App) shutdown(ctx context.Context) {
	if a.scans != nil {
		a.scans.Close() // committed scan work is kept
	}
	if a.db != nil {
		if err := db.Close(a.db); err != nil {
			log.Printf("closing catalog: %v", err)
		}
	}
}

// AppStatus is what the frontend needs to render its shell.
type AppStatus struct {
	DataDir      string   `json:"dataDir"`
	Language     string   `json:"language"`
	StartupError *msg.Msg `json:"startupError,omitempty"`
}

// GetStatus returns the data folder, the configured language and any startup error.
func (a *App) GetStatus() AppStatus {
	s := AppStatus{DataDir: a.layout.Root, StartupError: a.startupErr}
	if a.settings != nil {
		s.Language = a.settings.Get().Language
	}
	return s
}

// SetLanguage stores the UI language ("" = follow system locale).
func (a *App) SetLanguage(lang string) error {
	if a.settings == nil {
		return msg.Wrap(nil, "error.notReady")
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.Language = lang }); err != nil {
		return msg.Wrap(err, "error.settingsSave")
	}
	return nil
}

// OpenDataFolder opens the data folder in the system file manager.
func (a *App) OpenDataFolder() error {
	if err := osopen.Folder(a.layout.Root); err != nil {
		return msg.Wrap(err, "error.openFolder", "path", a.layout.Root)
	}
	return nil
}

// ready returns an error when startup failed.
func (a *App) ready() error {
	if a.db == nil || a.scans == nil {
		return msg.Wrap(nil, "error.notReady")
	}
	return nil
}

// wrapDB maps unexpected errors to a generic database message.
func wrapDB(err error) error {
	if err == nil {
		return nil
	}
	var me *msg.Error
	if errors.As(err, &me) {
		return err
	}
	return msg.Wrap(err, "error.database")
}

// ListRoots returns all root folders.
func (a *App) ListRoots() ([]catalog.Root, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	roots, err := catalog.ListRoots(a.ctx, a.db)
	return roots, wrapDB(err)
}

// PickFolder opens a native folder dialog; returns "" if cancelled.
func (a *App) PickFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{})
}

// AddRoot registers a root folder.
func (a *App) AddRoot(path string) (catalog.Root, error) {
	if err := a.ready(); err != nil {
		return catalog.Root{}, err
	}
	r, err := catalog.AddRoot(a.ctx, a.db, path)
	return r, wrapDB(err)
}

// RemoveRoot removes a root and its entries from the catalog (files are not touched).
func (a *App) RemoveRoot(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	if p := a.scans.Progress(); p != nil && p.Running && p.RootID == id {
		return msg.Wrap(nil, "scan.error.busy")
	}
	return wrapDB(catalog.RemoveRoot(a.ctx, a.db, id))
}

// GetRootStats returns catalog statistics for a root.
func (a *App) GetRootStats(id int64) (catalog.RootStats, error) {
	if err := a.ready(); err != nil {
		return catalog.RootStats{}, err
	}
	s, err := catalog.GetRootStats(a.ctx, a.db, id)
	return s, wrapDB(err)
}

// ListLastScanIssues returns problems found by the last scan of a root.
func (a *App) ListLastScanIssues(id int64) ([]catalog.Issue, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	is, err := catalog.ListLastScanIssues(a.ctx, a.db, id, 500)
	return is, wrapDB(err)
}

// StartScan starts a background scan of a root; progress arrives as "scan:progress" events.
func (a *App) StartScan(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(a.scans.Start(id))
}

// CancelScan stops the running scan; work done so far is kept.
func (a *App) CancelScan() {
	if a.scans != nil {
		a.scans.Cancel()
	}
}

// GetScanProgress returns the current or last scan's progress (nil if none).
func (a *App) GetScanProgress() *scanner.Progress {
	if a.scans == nil {
		return nil
	}
	return a.scans.Progress()
}
