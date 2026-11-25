package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"modelkeeper/internal/appdir"
	"modelkeeper/internal/backup"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/settings"
	"modelkeeper/internal/userexport"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Readable export and import of everything the user entered (J2, TODO 2.3),
// changing the path of a root folder, and undo (TODO 2.1).

var exportFileFilter = []runtime.FileFilter{{DisplayName: "modelkeeper export (*.json)", Pattern: "*.json"}}

// ExportResult describes a written export file.
type ExportResult struct {
	userexport.Summary
	Path string `json:"path"`
}

// ExportUserData writes everything the user entered (tags, notes, favorites,
// print status, corrections, manual thumbnails, root folders, detection
// settings) into a JSON file the user chooses. The file is enough to restore
// the work into an empty catalog, on this or another PC. Returns nil if the
// dialog was cancelled.
func (a *App) ExportUserData() (*ExportResult, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: "modelkeeper-export-" + time.Now().Format("2006-01-02") + ".json",
		Filters:         exportFileFilter,
	})
	if err != nil || file == "" {
		return nil, err
	}
	if !strings.HasSuffix(strings.ToLower(file), ".json") {
		file += ".json"
	}
	sum, err := userexport.WriteFile(a.ctx, a.db, file, userexport.Options{
		AppVersion: appVersion(), SettingsFile: a.layout.Settings, ThumbDir: a.layout.Thumbnails, EmbedThumbnails: true,
	})
	if err != nil {
		return nil, msg.Wrap(err, "export.failed", "path", file)
	}
	log.Printf("export: %d models to %s", sum.Models, file)
	return &ExportResult{Summary: sum, Path: file}, nil
}

// pendingImport is the export file chosen for import, until it is applied.
type pendingImport struct {
	mu   sync.Mutex
	file string
	exp  *userexport.Export
}

// ImportPreview is shown before an import: what the file holds, how its
// models match this catalog, and the root folders to map.
type ImportPreview struct {
	File  string          `json:"file"`
	Plan  userexport.Plan `json:"plan"`
	Roots []catalog.Root  `json:"roots"` // root folders of this catalog (active and deactivated)
}

// PickImportFile lets the user choose an export file (or the
// catalog-export.json of a snapshot) and returns the preview. Nothing is
// changed yet. Returns nil if the dialog was cancelled.
func (a *App) PickImportFile() (*ImportPreview, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Filters: exportFileFilter})
	if err != nil || file == "" {
		return nil, err
	}
	exp, err := userexport.Read(file)
	if err != nil {
		switch {
		case errors.Is(err, userexport.ErrTooNew):
			return nil, msg.Wrap(err, "import.tooNew", "path", file)
		case errors.Is(err, userexport.ErrNotAnExport):
			return nil, msg.Wrap(err, "import.notAnExport", "path", file)
		}
		return nil, msg.Wrap(err, "import.readFailed", "path", file)
	}
	a.imports.mu.Lock()
	a.imports.file, a.imports.exp = file, exp
	a.imports.mu.Unlock()
	return a.importPreview(file, exp, nil)
}

func (a *App) importPreview(file string, exp *userexport.Export, choices []userexport.RootChoice) (*ImportPreview, error) {
	plan, err := userexport.Preview(a.ctx, a.db, exp, choices)
	if err != nil {
		return nil, wrapDB(err)
	}
	active, err := catalog.ListRoots(a.ctx, a.db)
	if err != nil {
		return nil, wrapDB(err)
	}
	removed, err := catalog.ListRemovedRoots(a.ctx, a.db)
	if err != nil {
		return nil, wrapDB(err)
	}
	return &ImportPreview{File: file, Plan: plan, Roots: append(active, removed...)}, nil
}

func (a *App) pendingExport() (string, *userexport.Export, error) {
	a.imports.mu.Lock()
	defer a.imports.mu.Unlock()
	if a.imports.exp == nil {
		return "", nil, msg.Wrap(nil, "import.noFile")
	}
	return a.imports.file, a.imports.exp, nil
}

// PreviewImport recomputes the preview with the user's root folder choices.
func (a *App) PreviewImport(choices []userexport.RootChoice) (*ImportPreview, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	file, exp, err := a.pendingExport()
	if err != nil {
		return nil, err
	}
	return a.importPreview(file, exp, choices)
}

// CancelImport forgets the chosen export file.
func (a *App) CancelImport() {
	a.imports.mu.Lock()
	a.imports.file, a.imports.exp = "", nil
	a.imports.mu.Unlock()
}

// ImportOutcome is the result of ApplyImport.
type ImportOutcome struct {
	userexport.Result
	Backup           string `json:"backup"`           // the snapshot taken before
	SettingsImported bool   `json:"settingsImported"` // detection rules and file types taken over
}

// ApplyImport imports the chosen export with the given root folder choices.
// A running scan is stopped for it and continues afterwards. A snapshot is
// taken first (if it fails, nothing is imported). The import
// only adds: nothing the user entered in this catalog is removed or
// overwritten. Added root folders are scanned, changed ones detected again.
// importSettings also takes over the file types and detection rules.
func (a *App) ApplyImport(choices []userexport.RootChoice, importSettings bool) (*ImportOutcome, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	file, exp, err := a.pendingExport()
	if err != nil {
		return nil, err
	}
	out := &ImportOutcome{}
	err = a.scans.Interrupting(func(ctx context.Context) error {
		s, err := a.snapshot(a.db, backup.ReasonImport)
		if err != nil {
			return msg.Wrap(err, "error.backupFailed", "path", a.layout.Backups)
		}
		out.Backup = s.Path
		res, err := userexport.Apply(ctx, a.db, exp, choices, userexport.ApplyOptions{
			ThumbDir: a.layout.Thumbnails, SourceDir: filepath.Dir(file), DataDir: a.layout.Root,
		})
		out.Result = res
		return err
	})
	if err != nil {
		return nil, wrapDB(err)
	}
	log.Printf("import %s: %+v", file, out.Result)
	if importSettings && len(exp.Settings) > 0 {
		ok, err := a.importSettings(exp.Settings)
		if err != nil {
			return nil, err
		}
		out.SettingsImported = ok
	}
	for _, id := range out.RootsAdded {
		a.scans.EnqueueScan(id)
	}
	if out.SettingsImported {
		a.redetectAll()
	} else {
		for _, id := range out.RootsChanged {
			if r, err := catalog.GetRoot(a.ctx, a.db, id); err == nil && r.RemovedAt == nil {
				a.scans.EnqueueRedetect(id)
			}
		}
	}
	a.CancelImport()
	go a.syncWatch()
	runtime.EventsEmit(a.ctx, EventRootsChanged, nil)
	return out, nil
}

// importSettings takes over file types and detection rules from an export
// (the rest is specific to the PC: language, slicer, backup folder).
func (a *App) importSettings(raw json.RawMessage) (bool, error) {
	var in settings.Settings
	if err := json.Unmarshal(raw, &in); err != nil {
		return false, nil // unusable: the rest of the import stands
	}
	ft, err := settings.NormalizeFileTypes(in.FileTypes)
	if err != nil {
		return false, nil
	}
	parts, err1 := settings.NormalizeList(in.PartNames)
	sizes, err2 := settings.NormalizeList(in.SizeTokens)
	if err1 != nil || err2 != nil {
		return false, nil
	}
	if err := a.settings.Update(func(s *settings.Settings) {
		s.FileTypes, s.PartNames, s.SizeTokens = ft, parts, sizes
	}); err != nil {
		return false, msg.Wrap(err, "error.settingsSave")
	}
	return true, nil
}

// CheckRelocateRoot looks for the files of a root folder at a new path (a new
// NAS, another drive letter) without changing anything.
func (a *App) CheckRelocateRoot(id int64, path string) (catalog.RelocateCheck, error) {
	if err := a.ready(); err != nil {
		return catalog.RelocateCheck{}, err
	}
	c, err := catalog.CheckRelocate(a.ctx, a.db, id, path, a.layout.Root)
	return c, wrapDB(err)
}

// RelocateRoot gives a root folder its new path, keeping its ID and
// everything below it; a snapshot is taken first. A running scan is stopped
// for it (and continues afterwards). A scan follows, which
// finds the files at the new place. Files are not touched (J1).
func (a *App) RelocateRoot(id int64, path string) (catalog.Root, error) {
	if err := a.ready(); err != nil {
		return catalog.Root{}, err
	}
	var r catalog.Root
	err := a.scans.Interrupting(func(ctx context.Context) error {
		var err error
		r, err = catalog.RelocateRoot(ctx, a.db, id, path, a.layout.Root, func() error {
			if _, err := a.snapshot(a.db, backup.ReasonRelocate); err != nil {
				return msg.Wrap(err, "error.backupFailed", "path", a.layout.Backups)
			}
			return nil
		})
		return err
	})
	if err != nil {
		return r, wrapDB(err)
	}
	log.Printf("root %d moved to %s", r.ID, r.Path)
	a.scans.EnqueueScan(r.ID)
	go a.syncWatch()
	runtime.EventsEmit(a.ctx, EventRootsChanged, nil)
	return r, nil
}

// Undo reverts the newest change: of user data (tags, fields, favorites) or
// a correction of the detection (merge, split, mark, detach, reset). A
// correction needs the detection; a running scan is stopped for it and
// continues afterwards.
func (a *App) Undo() (catalog.UndoResult, error) {
	if err := a.ready(); err != nil {
		return catalog.UndoResult{}, err
	}
	h, err := catalog.NextUndo(a.ctx, a.db)
	if err != nil {
		return catalog.UndoResult{}, wrapDB(err)
	}
	if !strings.HasPrefix(h.Action, catalog.CorrectionPrefix) {
		r, err := catalog.Undo(a.ctx, a.db, nil)
		return r, wrapDB(err)
	}
	var r catalog.UndoResult
	err = a.scans.Interrupting(func(ctx context.Context) error {
		var err error
		r, err = catalog.Undo(ctx, a.db, func(ctx context.Context, roots []int64) error {
			for _, id := range roots {
				if _, err := detect.Apply(ctx, a.db, id, a.scans.DetectRules()); err != nil {
					return err
				}
			}
			return nil
		})
		return err
	})
	if err == nil {
		runtime.EventsEmit(a.ctx, EventRootsChanged, nil)
	}
	return r, wrapDB(err)
}

// ListHistory returns the newest recorded changes, newest first.
func (a *App) ListHistory() ([]catalog.HistoryEntry, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	h, err := catalog.ListHistory(a.ctx, a.db, 100)
	return h, wrapDB(err)
}

// dataDirWarning checks where the data folder lies (appdir.Risk). A temporary
// folder stops the start; Downloads or a synced folder give a warning.
func dataDirWarning(root string) (*msg.Msg, error) {
	if os.Getenv(appdir.EnvOverride) != "" {
		return nil, nil // developers and tests choose their folder
	}
	switch r := appdir.Risk(root); r {
	case "":
		return nil, nil
	case appdir.RiskTemp:
		return nil, msg.Wrap(nil, "error.dataDirTemp", "path", root)
	default:
		m := msg.New("warning.dataDir."+r, "path", root)
		return &m, nil
	}
}

// RenameTag renames a tag on all its models (merging it into an existing tag
// of that name). Undoable. Returns the number of models.
func (a *App) RenameTag(from, to string) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	n, err := catalog.RenameTag(a.ctx, a.db, from, to)
	return n, wrapDB(err)
}

// DeleteTag removes a tag from all its models. Undoable.
func (a *App) DeleteTag(name string) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	n, err := catalog.DeleteTag(a.ctx, a.db, name)
	return n, wrapDB(err)
}

// ForgetWaitingModels removes models that have no files in the catalog
// (imported models whose files are gone for good). A snapshot is taken
// first. Models with files are never removed this way.
func (a *App) ForgetWaitingModels(ids []int64) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	if _, err := a.snapshot(a.db, backup.ReasonForget); err != nil {
		return 0, msg.Wrap(err, "error.backupFailed", "path", a.layout.Backups)
	}
	n, err := catalog.ForgetWaiting(a.ctx, a.db, ids)
	return n, wrapDB(err)
}
