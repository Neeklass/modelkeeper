package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg" // decoders for SetThumbnailFromView
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"modelkeeper/internal/appdir"
	"modelkeeper/internal/archive"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/corrections"
	"modelkeeper/internal/db"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/osopen"
	"modelkeeper/internal/preview"
	"modelkeeper/internal/scanner"
	"modelkeeper/internal/settings"
	"modelkeeper/internal/thumbs"
	"modelkeeper/internal/watch"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// EventScanProgress carries scanner.Progress to the frontend.
const EventScanProgress = "scan:progress"

// EventRootsChanged tells the frontend to reload the root folders (e.g. the
// live-watching state changed).
const EventRootsChanged = "roots:changed"

// App is bound to the frontend. Bound methods stay thin and delegate to
// internal packages. Errors returned to the frontend are *msg.Error.
type App struct {
	ctx      context.Context
	layout   appdir.Layout
	settings *settings.Store
	db       *sql.DB
	scans    *scanner.Manager
	watch    *watch.Watcher // live watching of local roots (E4); nil if unavailable

	redetectMu sync.Mutex // serializes redetectAll
	logFile    *os.File

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
	if f, err := layout.OpenLog(); err == nil {
		// A GUI app has no console: errors go to data/modelkeeper.log.
		log.SetOutput(io.MultiWriter(f, os.Stderr)) // file first: GUI builds have no valid stderr
		a.logFile = f
		log.Printf("modelkeeper started, data folder %s", layout.Root)
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
		if !p.Running {
			// A root's first scan makes it eligible for watching.
			go a.syncWatch()
		}
	}, scanner.Options{ThumbDir: layout.Thumbnails})
	if err != nil {
		return msg.Wrap(err, "error.catalogOpen", "path", layout.Catalog)
	}
	a.scans = scans
	go a.detectExisting()

	w, err := watch.New(conn, scans, watch.Options{
		Exclude: layout.Root,
		Relevant: func(name string) bool {
			return a.settings.Get().ExtCategory(filepath.Ext(name)) != ""
		},
		NASInterval: func() time.Duration {
			return time.Duration(a.settings.Get().NASRescanHours) * time.Hour
		},
	})
	if err != nil {
		log.Printf("live watching unavailable: %v", err) // scans still work manually
	} else {
		a.watch = w
		go a.syncWatch()
	}
	return nil
}

// syncWatch aligns the watched folders with the root folders in the catalog.
func (a *App) syncWatch() {
	if a.watch == nil {
		return
	}
	if err := a.watch.Sync(context.Background()); err != nil {
		log.Printf("watch: %v", err)
		return
	}
	runtime.EventsEmit(a.ctx, EventRootsChanged)
}

func (a *App) shutdown(ctx context.Context) {
	if a.watch != nil {
		a.watch.Close() // before the scanner, so no new scan starts
	}
	if a.scans != nil {
		a.scans.Close() // committed scan work is kept
	}
	if a.db != nil {
		if err := db.Close(a.db); err != nil {
			log.Printf("closing catalog: %v", err)
		}
	}
	if a.logFile != nil {
		log.Printf("modelkeeper stopped")
		a.logFile.Close()
	}
}

// AppStatus is what the frontend needs to render its shell.
type AppStatus struct {
	DataDir  string `json:"dataDir"`
	Language string `json:"language"`
	// NASRescanHours: interval of scheduled NAS rescans, 0 = off (E4).
	NASRescanHours int      `json:"nasRescanHours"`
	StartupError   *msg.Msg `json:"startupError,omitempty"`
}

// GetStatus returns the data folder, the configured language and any startup error.
func (a *App) GetStatus() AppStatus {
	s := AppStatus{DataDir: a.layout.Root, StartupError: a.startupErr}
	if a.settings != nil {
		st := a.settings.Get()
		s.Language, s.NASRescanHours = st.Language, st.NASRescanHours
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

// NASRescanChoices are the intervals offered for scheduled NAS rescans (hours, 0 = off).
var NASRescanChoices = []int{0, 1, 6, 12, 24, 168}

// SetNASRescanHours sets the interval of scheduled NAS rescans (E4).
func (a *App) SetNASRescanHours(hours int) error {
	if a.settings == nil {
		return msg.Wrap(nil, "error.notReady")
	}
	if !slices.Contains(NASRescanChoices, hours) {
		return msg.Wrap(nil, "error.invalidValue")
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.NASRescanHours = hours }); err != nil {
		return msg.Wrap(err, "error.settingsSave")
	}
	return nil
}

// DetectionSettings are the editable lists of the settings screen (F10).
type DetectionSettings struct {
	FileTypes  settings.FileTypes `json:"fileTypes"`
	PartNames  []string           `json:"partNames"`
	SizeTokens []string           `json:"sizeTokens"`
}

// DetectionSettingsView carries the current values and the built-in defaults.
type DetectionSettingsView struct {
	Current  DetectionSettings `json:"current"`
	Defaults DetectionSettings `json:"defaults"`
}

func detectionOf(st settings.Settings) DetectionSettings {
	return DetectionSettings{FileTypes: st.FileTypes, PartNames: st.PartNames, SizeTokens: st.SizeTokens}
}

// GetDetectionSettings returns file types, part names and size tokens (F10).
func (a *App) GetDetectionSettings() (DetectionSettingsView, error) {
	if a.settings == nil {
		return DetectionSettingsView{}, msg.Wrap(nil, "error.notReady")
	}
	return DetectionSettingsView{Current: detectionOf(a.settings.Get()), Defaults: detectionOf(settings.Defaults())}, nil
}

// settingsError maps validation errors to their message.
func settingsError(err error) error {
	var ve *settings.ValidationError
	if errors.As(err, &ve) {
		return msg.Wrap(nil, ve.Key, "value", ve.Value)
	}
	return msg.Wrap(err, "error.settingsSave")
}

// SaveFileTypes stores the recognized file types (F10, D2) and adapts the
// catalog: entries of removed types are dropped, categories updated, and
// after adding types, archives are read again on the next scan. Fails while
// a scan runs. Model detection runs again in the background.
func (a *App) SaveFileTypes(ft settings.FileTypes) (catalog.FileTypesChange, error) {
	var ch catalog.FileTypesChange
	if err := a.ready(); err != nil {
		return ch, err
	}
	ft, err := settings.NormalizeFileTypes(ft)
	if err != nil {
		return ch, settingsError(err)
	}
	prev := a.settings.Get()
	added := len(ft.Added(prev.FileTypes)) > 0
	err = a.scans.Exclusive(func(ctx context.Context) error {
		if err := a.settings.Update(func(s *settings.Settings) { s.FileTypes = ft }); err != nil {
			return msg.Wrap(err, "error.settingsSave")
		}
		st := a.settings.Get()
		ch, err = catalog.ApplyFileTypes(ctx, a.db, st.ExtCategory, added)
		if err != nil {
			a.settings.Update(func(s *settings.Settings) { s.FileTypes = prev.FileTypes })
			return wrapDB(err)
		}
		return nil
	})
	if err != nil {
		return ch, err
	}
	log.Printf("file types changed: %+v", ch)
	if ch.Removed+ch.Recategorized > 0 {
		go a.redetectAll()
	}
	runtime.EventsEmit(a.ctx, EventRootsChanged)
	return ch, nil
}

// SaveDetectionRules stores part names and size tokens (F10, F4c, F4f) and
// re-runs model detection for all roots in the background. Manual
// corrections are kept (F4e).
func (a *App) SaveDetectionRules(partNames, sizeTokens []string) error {
	if err := a.ready(); err != nil {
		return err
	}
	parts, err := settings.NormalizeList(partNames)
	if err != nil {
		return settingsError(err)
	}
	sizes, err := settings.NormalizeList(sizeTokens)
	if err != nil {
		return settingsError(err)
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.PartNames, s.SizeTokens = parts, sizes }); err != nil {
		return msg.Wrap(err, "error.settingsSave")
	}
	go a.redetectAll()
	return nil
}

// redetectAll re-runs model detection for every root, one after another,
// waiting for scans that run meanwhile (e.g. started by the watcher).
func (a *App) redetectAll() {
	a.redetectMu.Lock() // a second call waits and then runs with the newest settings
	defer a.redetectMu.Unlock()
	roots, err := catalog.ListRoots(context.Background(), a.db)
	if err != nil {
		log.Printf("redetect all: %v", err)
		return
	}
	for _, r := range roots {
		for attempt := 0; ; attempt++ {
			a.scans.Wait()
			err := a.scans.Redetect(r.ID)
			if err == nil {
				break
			}
			if attempt >= 20 {
				log.Printf("redetect root %d: %v", r.ID, err)
				break
			}
			time.Sleep(500 * time.Millisecond) // another scan started in between
		}
	}
	a.scans.Wait()
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
	if a.watch != nil {
		for i := range roots {
			roots[i].Live = a.watch.Live(roots[i].ID)
		}
	}
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
	if err := catalog.RemoveRoot(a.ctx, a.db, id); err != nil {
		return wrapDB(err)
	}
	go a.syncWatch()
	return nil
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

// StartFullCheck re-reads and re-hashes every file of a root in the
// background, regardless of size and modification time (F9). Files whose
// content changed silently are reported as scan issues (F8).
func (a *App) StartFullCheck(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(a.scans.StartFull(id))
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

// SearchModels returns a page of the model list for a query (H1, H2).
func (a *App) SearchModels(query catalog.ModelQuery, offset, limit int) (catalog.ModelPage, error) {
	if err := a.ready(); err != nil {
		return catalog.ModelPage{}, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	p, err := catalog.SearchModels(a.ctx, a.db, query, offset, limit)
	return p, wrapDB(err)
}

// GetModel returns the detail view of a model (H4).
func (a *App) GetModel(id int64) (catalog.ModelDetail, error) {
	if err := a.ready(); err != nil {
		return catalog.ModelDetail{}, err
	}
	d, err := catalog.GetModel(a.ctx, a.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return d, msg.Wrap(nil, "error.modelNotFound")
	}
	if err == nil {
		d.HasCorrections, err = corrections.HasCorrections(a.ctx, a.db, id)
	}
	return d, wrapDB(err)
}

// ShowInExplorer reveals a file (or the archive containing it) in the file manager (H5).
func (a *App) ShowInExplorer(locationID int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	p, err := catalog.LocationPath(a.ctx, a.db, locationID)
	if err != nil {
		return wrapDB(err)
	}
	if err := osopen.Reveal(p); err != nil {
		return msg.Wrap(err, "error.openFolder", "path", p)
	}
	return nil
}

// RedetectRoot re-runs model detection for a root in the background.
func (a *App) RedetectRoot(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(a.scans.Redetect(id))
}

// SetRootCreatorLevel turns "first level = creator" (F4a) on/off and re-detects.
func (a *App) SetRootCreatorLevel(id int64, on bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	if p := a.scans.Progress(); p != nil && p.Running {
		return msg.Wrap(nil, "scan.error.alreadyRunning")
	}
	if err := catalog.SetCreatorLevel(a.ctx, a.db, id, on); err != nil {
		return wrapDB(err)
	}
	return wrapDB(a.scans.Redetect(id))
}

// detectExisting runs detection (and thumbnail generation) once at startup
// for roots that have files but no models yet, or models without a thumbnail
// that was never attempted (e.g. catalogs from before these features).
func (a *App) detectExisting() {
	rows, err := a.db.Query(`SELECT r.id FROM root r
		WHERE r.status <> 'offline'
		  AND EXISTS (SELECT 1 FROM location l WHERE l.root_id = r.id AND l.present = 1)
		  AND (NOT EXISTS (SELECT 1 FROM model_anchor m WHERE m.root_id = r.id)
		    OR EXISTS (SELECT 1 FROM model m JOIN model_anchor a ON a.model_id = m.id
		      WHERE a.root_id = r.id AND m.thumb_source IS NULL AND m.thumb_tried_at IS NULL))`)
	if err != nil {
		log.Printf("detect existing: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		a.scans.Wait()
		if err := a.scans.Redetect(id); err != nil {
			log.Printf("detect existing root %d: %v", id, err)
			continue
		}
		a.scans.Wait()
	}
}

// correct runs a manual correction and re-detects the affected roots, while
// no scan runs (F4d). Corrections are stored as overrides and survive rescans (F4e).
func (a *App) correct(op func(ctx context.Context) ([]int64, error)) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(a.scans.Exclusive(func(ctx context.Context) error {
		roots, err := op(ctx)
		if err != nil {
			return err
		}
		for _, r := range roots {
			if _, err := detect.Apply(ctx, a.db, r, a.scans.DetectRules()); err != nil {
				return err
			}
		}
		return nil
	}))
}

// MarkFolderModel marks a folder as a model: everything below belongs to it.
func (a *App) MarkFolderModel(rootID int64, path string) error {
	return a.correct(func(ctx context.Context) ([]int64, error) {
		return corrections.MarkModel(ctx, a.db, rootID, path)
	})
}

// MarkFolderNotModel merges a detected model folder into the folder above.
func (a *App) MarkFolderNotModel(rootID int64, path string) error {
	return a.correct(func(ctx context.Context) ([]int64, error) {
		return corrections.MarkNotModel(ctx, a.db, rootID, path)
	})
}

// SplitModel makes every subfolder of the model a model of its own.
func (a *App) SplitModel(id int64) error {
	return a.correct(func(ctx context.Context) ([]int64, error) { return corrections.Split(ctx, a.db, id) })
}

// DetachAnchor splits one location off a model that has several.
func (a *App) DetachAnchor(id, rootID int64, path string) error {
	return a.correct(func(ctx context.Context) ([]int64, error) {
		return corrections.Detach(ctx, a.db, id, rootID, path)
	})
}

// MergeModels merges the source models into the target.
func (a *App) MergeModels(targetID int64, sourceIDs []int64) error {
	return a.correct(func(ctx context.Context) ([]int64, error) {
		return corrections.Merge(ctx, a.db, targetID, sourceIDs)
	})
}

// ResetCorrections removes all manual corrections at a model's folders.
func (a *App) ResetCorrections(id int64) error {
	return a.correct(func(ctx context.Context) ([]int64, error) { return corrections.Reset(ctx, a.db, id) })
}

// ListTags returns all tags with usage counts.
func (a *App) ListTags() ([]catalog.TagCount, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	t, err := catalog.ListTags(a.ctx, a.db)
	return t, wrapDB(err)
}

// AddTags adds tags to one or more models (G2).
func (a *App) AddTags(modelIDs []int64, names []string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.AddTags(a.ctx, a.db, modelIDs, names))
}

// RemoveTag removes a tag from one or more models.
func (a *App) RemoveTag(modelIDs []int64, name string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.RemoveTag(a.ctx, a.db, modelIDs, name))
}

// DismissSuggestion hides a tag suggestion (F5).
func (a *App) DismissSuggestion(modelID int64, name string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.DismissSuggestion(a.ctx, a.db, modelID, name))
}

// UpdateModel edits name, notes, print status (L1) or supports (L2).
func (a *App) UpdateModel(id int64, u catalog.ModelUpdate) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.UpdateModel(a.ctx, a.db, id, u))
}

// GetFilterOptions returns the creators and file types for the filter dropdowns.
func (a *App) GetFilterOptions() (catalog.FilterOptions, error) {
	if err := a.ready(); err != nil {
		return catalog.FilterOptions{}, err
	}
	o, err := catalog.GetFilterOptions(a.ctx, a.db)
	return o, wrapDB(err)
}

// thumbName: thumbnail files are named "<uuid>.jpg".
var thumbName = regexp.MustCompile(`^[0-9a-f-]{36}\.jpg$`)

// assetHandler serves files to the frontend (Wails asset server fallback):
//   - /thumbs/<file>: thumbnails from the data folder
//   - /files/<location id>: model files for the 3D preview (H4)
//
// It is a separate type so that ServeHTTP is not exposed as a JavaScript
// binding of App.
type assetHandler struct{ a *App }

func (h assetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if name, ok := strings.CutPrefix(r.URL.Path, "/thumbs/"); ok {
		h.serveThumb(w, r, name)
		return
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/files/"); ok {
		h.serveFile(w, r, id)
		return
	}
	http.NotFound(w, r)
}

func (h assetHandler) serveThumb(w http.ResponseWriter, r *http.Request, name string) {
	a := h.a
	if !thumbName.MatchString(name) || a.layout.Thumbnails == "" {
		http.NotFound(w, r)
		return
	}
	// The URL carries a version parameter, so the file can be cached for good.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, filepath.Join(a.layout.Thumbnails, name))
}

// serveFile streams a model file (or an archive member, read in memory only)
// for the 3D preview. Only previewable file types of catalogued locations are
// served. Errors are reported as status codes the frontend translates.
func (h assetHandler) serveFile(w http.ResponseWriter, r *http.Request, idText string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || h.a.db == nil {
		http.NotFound(w, r)
		return
	}
	src, err := preview.Lookup(r.Context(), h.a.db, id)
	var f io.ReadSeekCloser
	var mod time.Time
	if err == nil {
		f, mod, err = src.Open()
	}
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, preview.ErrNotFound), errors.Is(err, preview.ErrUnsupported):
			code = http.StatusNotFound
		case errors.Is(err, preview.ErrTooLarge):
			code = http.StatusRequestEntityTooLarge
		case errors.Is(err, preview.ErrOffline):
			code = http.StatusServiceUnavailable
		case errors.Is(err, archive.ErrEncrypted):
			code = http.StatusForbidden
		default:
			log.Printf("preview %d: %v", id, err)
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, src.Name, mod, f)
}

func (a *App) thumbStore() *thumbs.Store {
	return &thumbs.Store{DB: a.db, Dir: a.layout.Thumbnails}
}

// ListThumbnailCandidates returns the files a thumbnail can be made from (I4).
func (a *App) ListThumbnailCandidates(modelID int64) ([]thumbs.Candidate, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	c, err := thumbs.Candidates(a.ctx, a.db, modelID)
	if c == nil {
		c = []thumbs.Candidate{}
	}
	return c, wrapDB(err)
}

// SetThumbnail makes one of the model's files its thumbnail (manual, I4).
func (a *App) SetThumbnail(modelID, locationID int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.thumbStore().SetFromCandidate(a.ctx, modelID, locationID); err != nil {
		return msg.Wrap(err, "thumbs.failed")
	}
	return nil
}

// PickThumbnailFile lets the user choose any image file as thumbnail (manual, I4).
// Returns false if the dialog was cancelled.
func (a *App) PickThumbnailFile(modelID int64) (bool, error) {
	if err := a.ready(); err != nil {
		return false, err
	}
	file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Filters: []runtime.FileFilter{{DisplayName: "Images (*.jpg;*.png;*.webp)", Pattern: "*.jpg;*.jpeg;*.png;*.webp"}},
	})
	if err != nil || file == "" {
		return false, err
	}
	if err := a.thumbStore().SetFromFile(a.ctx, modelID, file); err != nil {
		return false, msg.Wrap(err, "thumbs.failed")
	}
	return true, nil
}

// SetThumbnailFromView stores the current view of the 3D preview as manual
// thumbnail (I4). image is a data URL (PNG or JPEG) from the preview canvas.
func (a *App) SetThumbnailFromView(modelID int64, dataURL string) error {
	if err := a.ready(); err != nil {
		return err
	}
	_, b64, ok := strings.Cut(dataURL, ";base64,")
	if !ok || !strings.HasPrefix(dataURL, "data:image/") {
		return msg.Wrap(errors.New("not an image data URL"), "thumbs.failed")
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return msg.Wrap(err, "thumbs.failed")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return msg.Wrap(err, "thumbs.failed")
	}
	if err := a.thumbStore().SetFromImage(a.ctx, modelID, img); err != nil {
		return msg.Wrap(err, "thumbs.failed")
	}
	return nil
}

// ResetThumbnail drops a manual thumbnail and generates an automatic one again.
func (a *App) ResetThumbnail(modelID int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.thumbStore().Reset(a.ctx, modelID); err != nil {
		return msg.Wrap(err, "thumbs.failed")
	}
	return nil
}
