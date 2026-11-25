package main

import (
	"errors"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"modelkeeper/internal/appdir"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/pathutil"
	"modelkeeper/internal/settings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Configuration as JSON (settings + root folders) and moving the app folder
// (concept M).

var configFileFilter = []runtime.FileFilter{{DisplayName: "modelkeeper configuration (*.json)", Pattern: "*.json"}}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// ExportConfig writes the settings and the list of root folders into a JSON
// file the user chooses. Returns "" if the dialog was cancelled.
func (a *App) ExportConfig() (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: "modelkeeper-config-" + time.Now().Format("2006-01-02") + ".json",
		Filters:         configFileFilter,
	})
	if err != nil || file == "" {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(file), ".json") {
		file += ".json"
	}
	roots, err := catalog.ListRoots(a.ctx, a.db)
	if err != nil {
		return "", wrapDB(err)
	}
	var list []settings.ConfigRoot
	for _, r := range roots {
		list = append(list, settings.ConfigRoot{Path: r.Path, Kind: r.Kind, CreatorLevel: r.CreatorLevel})
	}
	if err := settings.WriteConfigFile(file, settings.NewConfig(a.settings.Get(), list, appVersion(), time.Now())); err != nil {
		return "", msg.Wrap(err, "export.failed", "path", file)
	}
	log.Printf("configuration exported to %s", file)
	return file, nil
}

// ConfigRootPreview is a root folder of a configuration file.
type ConfigRootPreview struct {
	settings.ConfigRoot
	Exists bool `json:"exists"` // reachable from this PC
	Added  bool `json:"added"`  // already a root folder here
}

// ConfigPreview is shown before a configuration is applied.
type ConfigPreview struct {
	File     string              `json:"file"`
	Settings settings.Settings   `json:"settings"`
	Roots    []ConfigRootPreview `json:"roots"`
	Exported string              `json:"exported"`
	Version  string              `json:"version"`
}

type pendingConfig struct {
	mu  sync.Mutex
	cfg *settings.Config
}

// PickConfigFile lets the user choose a configuration file and returns what
// it contains. Nothing is applied yet. Returns nil if cancelled.
func (a *App) PickConfigFile() (*ConfigPreview, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Filters: configFileFilter})
	if err != nil || file == "" {
		return nil, err
	}
	cfg, err := settings.ReadConfigFile(file)
	if err != nil {
		var ve *settings.ValidationError
		if errors.As(err, &ve) {
			return nil, settingsError(ve)
		}
		return nil, msg.Wrap(err, "config.notAConfig", "path", file)
	}
	a.configs.mu.Lock()
	a.configs.cfg = cfg
	a.configs.mu.Unlock()
	active, err := catalog.ListRoots(a.ctx, a.db)
	if err != nil {
		return nil, wrapDB(err)
	}
	p := &ConfigPreview{File: file, Settings: cfg.Settings, Roots: []ConfigRootPreview{}, Exported: cfg.ExportedAt, Version: cfg.AppVersion}
	for _, r := range cfg.Roots {
		rp := ConfigRootPreview{ConfigRoot: r}
		for _, l := range active {
			if pathutil.Same(l.Path, r.Path) {
				rp.Added = true
			}
		}
		if !rp.Added {
			rp.Exists = isDir(r.Path)
		}
		p.Roots = append(p.Roots, rp)
	}
	return p, nil
}

// ConfigApply says which parts of a configuration are taken over.
type ConfigApply struct {
	Detection bool     `json:"detection"` // file types, part names, size tokens
	Program   bool     `json:"program"`   // language, NAS rescan interval
	Machine   bool     `json:"machine"`   // slicer program, second backup folder (paths on this PC)
	Roots     []string `json:"roots"`     // root folders to add
}

// ConfigOutcome describes an applied configuration.
type ConfigOutcome struct {
	RootsAdded int      `json:"rootsAdded"`
	Skipped    []string `json:"skipped"` // paths that could not be used (not found here)
}

// ApplyConfig applies the chosen parts of the picked configuration. Changed
// file types take a snapshot first and update the catalog like the settings
// screen does; added root folders are scanned.
func (a *App) ApplyConfig(o ConfigApply) (*ConfigOutcome, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	a.configs.mu.Lock()
	cfg := a.configs.cfg
	a.configs.mu.Unlock()
	if cfg == nil {
		return nil, msg.Wrap(nil, "import.noFile")
	}
	in := cfg.Settings
	out := &ConfigOutcome{Skipped: []string{}}
	if o.Detection {
		if err := a.applyDetection(in.FileTypes, in.PartNames, in.SizeTokens); err != nil {
			return nil, err
		}
	}
	if o.Program {
		if err := a.settings.Update(func(s *settings.Settings) {
			s.Language = in.Language
			if slices.Contains(NASRescanChoices, in.NASRescanHours) {
				s.NASRescanHours = in.NASRescanHours
			}
		}); err != nil {
			return nil, msg.Wrap(err, "error.settingsSave")
		}
	}
	if o.Machine {
		slicer, backupDir := "", ""
		if in.SlicerPath != "" {
			if isFile(in.SlicerPath) {
				slicer = in.SlicerPath
			} else {
				out.Skipped = append(out.Skipped, in.SlicerPath)
			}
		}
		if in.ExternalBackupDir != "" {
			if isDir(in.ExternalBackupDir) && a.checkExternalDir(in.ExternalBackupDir) == nil {
				backupDir = in.ExternalBackupDir
			} else {
				out.Skipped = append(out.Skipped, in.ExternalBackupDir)
			}
		}
		if err := a.settings.Update(func(s *settings.Settings) {
			if slicer != "" {
				s.SlicerPath = slicer
			}
			if backupDir != "" {
				s.ExternalBackupDir = backupDir
			}
		}); err != nil {
			return nil, msg.Wrap(err, "error.settingsSave")
		}
		if backupDir != "" {
			a.afterBackupAsync(true)
		}
	}
	for _, path := range o.Roots {
		var want *settings.ConfigRoot
		for i := range cfg.Roots {
			if cfg.Roots[i].Path == path {
				want = &cfg.Roots[i]
			}
		}
		if want == nil {
			continue
		}
		r, err := catalog.AddRoot(a.ctx, a.db, path, a.layout.Root)
		if err != nil {
			out.Skipped = append(out.Skipped, path)
			continue
		}
		if r.CreatorLevel != want.CreatorLevel {
			catalog.SetCreatorLevel(a.ctx, a.db, r.ID, want.CreatorLevel)
		}
		a.scans.EnqueueScan(r.ID)
		out.RootsAdded++
	}
	a.configs.mu.Lock()
	a.configs.cfg = nil
	a.configs.mu.Unlock()
	go a.syncWatch()
	runtime.EventsEmit(a.ctx, EventRootsChanged, nil)
	return out, nil
}

// applyDetection takes over file types and detection rules the way the
// settings screen saves them (snapshot before changed file types, catalog
// updated, detection run again). Unchanged parts are left alone.
func (a *App) applyDetection(ft settings.FileTypes, parts, sizes []string) error {
	prev := a.settings.Get()
	nft, err := settings.NormalizeFileTypes(ft)
	if err != nil {
		return settingsError(err)
	}
	if len(nft.Changed(prev.FileTypes)) > 0 || len(nft.Added(prev.FileTypes)) > 0 {
		if _, err := a.SaveFileTypes(nft); err != nil {
			return err
		}
	}
	if !slices.Equal(parts, prev.PartNames) || !slices.Equal(sizes, prev.SizeTokens) {
		return a.SaveDetectionRules(parts, sizes)
	}
	return nil
}

// --- the app folder was moved ---

// RootMove is a root folder that seems to have moved along with the app.
type RootMove struct {
	RootID int64  `json:"rootId"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// checkMoved compares the data folder with the one of the last start.
func (a *App) checkMoved() {
	last := a.settings.Get().DataDir
	if last != "" && !pathutil.Same(last, a.layout.Root) {
		log.Printf("the app folder was moved: %s -> %s", last, a.layout.Root)
		a.movedFrom = last
	}
	if last != a.layout.Root {
		if err := a.settings.Update(func(s *settings.Settings) { s.DataDir = a.layout.Root }); err != nil {
			log.Printf("settings: %v", err)
		}
	}
}

// GetMoveSuggestions returns the root folders that are gone at their old path
// but found where they would be if they moved along with the app (another
// drive letter of a portable disk, model folders next to the app folder).
func (a *App) GetMoveSuggestions() ([]RootMove, error) {
	out := []RootMove{}
	if err := a.ready(); err != nil || a.movedFrom == "" {
		return out, err
	}
	roots, err := catalog.ListRoots(a.ctx, a.db)
	if err != nil {
		return out, wrapDB(err)
	}
	for _, r := range roots {
		to, ok := appdir.MovedRoot(a.movedFrom, a.layout.Root, r.Path)
		// The new place first: it is usually local, the old one may be a
		// NAS that takes long to answer.
		if ok && isDir(to) && !isDir(r.Path) {
			out = append(out, RootMove{RootID: r.ID, From: r.Path, To: to})
		}
	}
	return out, nil
}

// ApplyMoveSuggestions changes the paths of the given root folders to their
// suggested new place (like "Change path…": IDs, tags and corrections stay).
func (a *App) ApplyMoveSuggestions(ids []int64) (int, error) {
	list, err := a.GetMoveSuggestions()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range list {
		if !slices.Contains(ids, m.RootID) {
			continue
		}
		if _, err := a.RelocateRoot(m.RootID, m.To); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 || len(list) == 0 {
		a.movedFrom = ""
	}
	return n, nil
}

// DismissMoved hides the note that the app folder was moved.
func (a *App) DismissMoved() {
	a.movedFrom = ""
}
