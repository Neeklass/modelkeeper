package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"modelkeeper/internal/backup"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/settings"
	"modelkeeper/internal/userexport"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Sync between PCs (J9). Each installation writes its catalog export into a
// shared folder (NAS, Dropbox …) as modelkeeper-sync-<name>.json, and applies
// the files of the others. Nothing is ever shared live: the catalog stays a
// local SQLite file on each PC (a database on a network share is refused,
// J5). Applying uses the other PC's previous file as the base of a three-way
// merge (userexport.ApplyOptions.Base), so what it changed or removed since
// arrives here, and what it did not touch does not overwrite work done here.
// Root folders are never added by a sync; exported roots map to the root of
// the same path here, others are skipped (their models link up by content).

const (
	syncPrefix   = "modelkeeper-sync-"
	syncInterval = 30 * time.Minute
	syncFirst    = 20 * time.Second
)

// EventSyncChanged: a sync ran (the catalog may have changed).
const EventSyncChanged = "sync:changed"

// SyncPeer is another PC's file in the sync folder.
type SyncPeer struct {
	Name      string `json:"name"`      // file name without prefix and .json
	Modified  int64  `json:"modified"`  // unix ms of the file
	AppliedAt int64  `json:"appliedAt"` // unix ms when it was last applied here (0 = never)
	Error     string `json:"error,omitempty"`
}

// SyncStatus is shown in the settings.
type SyncStatus struct {
	Dir      string     `json:"dir"`
	Own      string     `json:"own"` // this PC's file name
	LastRun  int64      `json:"lastRun"`
	Running  bool       `json:"running"`
	Error    *msg.Msg   `json:"error,omitempty"`
	Peers    []SyncPeer `json:"peers"`
	Waiting  bool       `json:"waiting"` // a scan runs; applying waits for it
	Exported int64      `json:"exported"`
}

type syncFileState struct {
	Modified  int64 `json:"modified"`
	Size      int64 `json:"size"`
	AppliedAt int64 `json:"appliedAt"`
}

// syncState is kept in data/sync/state.json.
type syncState struct {
	ExportHash  string                   `json:"exportHash"`
	ExportedAt  int64                    `json:"exportedAt"`
	SnapshotDay string                   `json:"snapshotDay"`
	Files       map[string]syncFileState `json:"files"`
	LastRun     int64                    `json:"lastRun"`
}

type peerSync struct {
	mu      sync.Mutex // one run at a time
	stMu    sync.Mutex
	running bool
	waiting bool
	lastErr *msg.Msg
	peerErr map[string]string
}

func (a *App) syncDir() string { return filepath.Join(a.layout.Root, "sync") }

func (a *App) loadSyncState() syncState {
	st := syncState{Files: map[string]syncFileState{}}
	b, err := os.ReadFile(filepath.Join(a.syncDir(), "state.json"))
	if err == nil {
		json.Unmarshal(b, &st)
	}
	if st.Files == nil {
		st.Files = map[string]syncFileState{}
	}
	return st
}

func (a *App) saveSyncState(st syncState) error {
	if err := os.MkdirAll(a.syncDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(a.syncDir(), "state.json"), b)
}

func writeAtomic(file string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), file)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ownSyncName is this installation's name in the sync folder: the computer
// name and a random id (two installations on one PC stay apart).
func (a *App) ownSyncName() (string, error) {
	st := a.settings.Get()
	id := st.SyncID
	if id == "" {
		b := make([]byte, 4)
		rand.Read(b)
		id = hex.EncodeToString(b)
		if err := a.settings.Update(func(s *settings.Settings) { s.SyncID = id }); err != nil {
			return "", err
		}
	}
	host, _ := os.Hostname()
	host = strings.Trim(unsafeName.ReplaceAllString(host, "-"), "-")
	if host == "" {
		host = "pc"
	}
	return host + "-" + id, nil
}

// SetSyncDir turns the sync on (a folder shared with the other PCs) or off ("").
func (a *App) SetSyncDir(dir string) (SyncStatus, error) {
	if err := a.ready(); err != nil {
		return SyncStatus{}, err
	}
	dir = strings.TrimSpace(dir)
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil || !isDir(abs) {
			return SyncStatus{}, msg.Wrap(err, "sync.notAFolder", "path", dir)
		}
		if err := a.checkExternalDir(abs); err != nil {
			return SyncStatus{}, err // not inside the data folder
		}
		dir = abs
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.SyncDir = dir }); err != nil {
		return SyncStatus{}, msg.Wrap(err, "error.settingsSave")
	}
	if dir != "" {
		go a.peerSyncNow()
	}
	return a.GetSyncStatus(), nil
}

// SyncNow runs a sync right away (in the background).
func (a *App) SyncNow() {
	go a.peerSyncNow()
}

// GetSyncStatus describes the sync.
func (a *App) GetSyncStatus() SyncStatus {
	st := a.settings.Get()
	s := SyncStatus{Dir: st.SyncDir, Peers: []SyncPeer{}}
	if st.SyncDir == "" || a.layout.Root == "" {
		return s
	}
	own, _ := a.ownSyncName()
	s.Own = syncPrefix + own + ".json"
	state := a.loadSyncState()
	s.LastRun, s.Exported = state.LastRun, state.ExportedAt
	a.peers.stMu.Lock()
	s.Running, s.Waiting, s.Error = a.peers.running, a.peers.waiting, a.peers.lastErr
	errs := map[string]string{}
	for k, v := range a.peers.peerErr {
		errs[k] = v
	}
	a.peers.stMu.Unlock()
	files, _ := peerFiles(st.SyncDir, s.Own)
	for _, f := range files {
		p := SyncPeer{Name: strings.TrimSuffix(strings.TrimPrefix(f.name, syncPrefix), ".json"), Modified: f.mod,
			AppliedAt: state.Files[f.name].AppliedAt, Error: errs[f.name]}
		s.Peers = append(s.Peers, p)
	}
	return s
}

type peerFile struct {
	name string
	mod  int64
	size int64
}

func peerFiles(dir, own string) ([]peerFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []peerFile
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == own || !strings.HasPrefix(n, syncPrefix) || !strings.HasSuffix(n, ".json") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, peerFile{n, fi.ModTime().UnixMilli(), fi.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func (a *App) setSyncState(running, waiting bool, err *msg.Msg) {
	a.peers.stMu.Lock()
	a.peers.running, a.peers.waiting, a.peers.lastErr = running, waiting, err
	a.peers.stMu.Unlock()
}

// runPeerSync syncs shortly after the start and then every 30 minutes.
func (a *App) runPeerSync(stop <-chan struct{}) {
	t := time.NewTimer(syncFirst)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			a.peerSyncNow()
			t.Reset(syncInterval)
		}
	}
}

// peerSyncNow applies the other PCs' new files, then writes this PC's file.
func (a *App) peerSyncNow() {
	dir := a.settings.Get().SyncDir
	if dir == "" || a.ready() != nil {
		return
	}
	if !a.peers.mu.TryLock() {
		return // already running
	}
	defer a.peers.mu.Unlock()
	a.setSyncState(true, false, nil)
	var errMsg *msg.Msg
	waiting := false
	defer func() {
		a.setSyncState(false, waiting, errMsg)
		runtime.EventsEmit(a.ctx, EventSyncChanged, nil)
	}()
	if !isDir(dir) {
		m := msg.New("sync.unreachable", "path", dir)
		errMsg = &m
		return
	}
	own, err := a.ownSyncName()
	if err != nil {
		m := msg.New("error.settingsSave")
		errMsg = &m
		return
	}
	ownFile := syncPrefix + own + ".json"
	state := a.loadSyncState()
	applied, w, err := a.applyPeers(dir, ownFile, &state)
	waiting = w
	if err != nil {
		m := toMsgErr(err)
		errMsg = &m
	}
	if err := a.exportOwn(dir, ownFile, &state); err != nil {
		m := toMsgErr(err)
		errMsg = &m
	}
	state.LastRun = time.Now().UnixMilli()
	if err := a.saveSyncState(state); err != nil {
		log.Printf("sync state: %v", err)
	}
	if applied > 0 {
		runtime.EventsEmit(a.ctx, EventRootsChanged, nil)
	}
}

func toMsgErr(err error) msg.Msg {
	var me *msg.Error
	if errors.As(err, &me) {
		return me.Msg
	}
	return msg.New("sync.failed", "error", err.Error())
}

// applyPeers applies the files of the other PCs that changed since they were
// last applied. waiting: a scan runs, the files are applied next time.
func (a *App) applyPeers(dir, own string, state *syncState) (applied int, waiting bool, err error) {
	files, err := peerFiles(dir, own)
	if err != nil {
		return 0, false, msg.Wrap(err, "sync.unreachable", "path", dir)
	}
	a.peers.stMu.Lock()
	a.peers.peerErr = map[string]string{}
	a.peers.stMu.Unlock()
	for _, f := range files {
		prev := state.Files[f.name]
		if prev.AppliedAt > 0 && prev.Modified == f.mod && prev.Size == f.size {
			continue // unchanged since it was applied
		}
		if a.scans.Busy() {
			return applied, true, nil
		}
		raw, err := os.ReadFile(filepath.Join(dir, f.name))
		if err == nil {
			err = a.applyPeer(f.name, raw, state)
		}
		if err != nil {
			log.Printf("sync %s: %v", f.name, err)
			a.peers.stMu.Lock()
			a.peers.peerErr[f.name] = err.Error()
			a.peers.stMu.Unlock()
			continue // the others still sync
		}
		state.Files[f.name] = syncFileState{Modified: f.mod, Size: f.size, AppliedAt: time.Now().UnixMilli()}
		applied++
	}
	return applied, false, nil
}

func (a *App) applyPeer(name string, raw []byte, state *syncState) error {
	exp, err := userexport.Decode(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	baseFile := filepath.Join(a.syncDir(), "base-"+name)
	var base *userexport.Export
	if b, err := os.ReadFile(baseFile); err == nil {
		if x, err := userexport.Decode(bytes.NewReader(b)); err == nil {
			base = x
		}
	}
	var res userexport.Result
	err = a.scans.Exclusive(func(ctx context.Context) error {
		// At most one snapshot a day for syncs (they come every 30 minutes).
		if day := time.Now().Format("2006-01-02"); state.SnapshotDay != day {
			if _, err := a.snapshot(a.db, backup.ReasonSync); err != nil {
				return msg.Wrap(err, "error.backupFailed", "path", a.layout.Backups)
			}
			state.SnapshotDay = day
		}
		plan, err := userexport.Preview(ctx, a.db, exp, nil)
		if err != nil {
			return err
		}
		var choices []userexport.RootChoice
		for _, r := range plan.Roots {
			c := r.Suggested
			if c.Action != userexport.ActionExisting {
				c = userexport.RootChoice{Key: r.Key, Action: userexport.ActionSkip}
			}
			choices = append(choices, c)
		}
		res, err = userexport.Apply(ctx, a.db, exp, choices, userexport.ApplyOptions{
			ThumbDir: a.layout.Thumbnails, Base: base,
			Journal: "history.synced", JournalParams: map[string]any{"peer": peerLabel(name)},
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, id := range res.RootsChanged {
		if r, err := catalog.GetRoot(a.ctx, a.db, id); err == nil && r.RemovedAt == nil {
			a.scans.EnqueueRedetect(id)
		}
	}
	log.Printf("sync: applied %s (%+v)", name, res.Match)
	return writeAtomic(baseFile, raw)
}

// exportOwn writes this PC's file if the catalog changed since the last one.
func (a *App) exportOwn(dir, own string, state *syncState) error {
	// Not a.ctx: this also runs while the app closes.
	exp, err := userexport.Build(context.Background(), a.db, userexport.Options{
		AppVersion: appVersion(), SettingsFile: a.layout.Settings, ThumbDir: a.layout.Thumbnails, EmbedThumbnails: true,
	})
	if err != nil {
		return err
	}
	at := exp.ExportedAt
	exp.ExportedAt, exp.Settings = "", nil // the hash covers the data only; settings stay per PC
	var buf bytes.Buffer
	if err := exp.Write(&buf); err != nil {
		return err
	}
	sum := sha256.Sum256(buf.Bytes())
	hash := hex.EncodeToString(sum[:])
	if hash == state.ExportHash && isFile(filepath.Join(dir, own)) {
		return nil
	}
	exp.ExportedAt = at
	buf.Reset()
	if err := exp.Write(&buf); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, own), buf.Bytes()); err != nil {
		return msg.Wrap(err, "sync.writeFailed", "path", dir)
	}
	state.ExportHash, state.ExportedAt = hash, time.Now().UnixMilli()
	return nil
}

// peerExportOnClose writes this PC's file when the app closes, so the other
// PCs get the newest work.
func (a *App) peerExportOnClose() {
	dir := a.settings.Get().SyncDir
	if dir == "" || a.db == nil || !isDir(dir) {
		return
	}
	if !a.peers.mu.TryLock() {
		return
	}
	defer a.peers.mu.Unlock()
	own, err := a.ownSyncName()
	if err != nil {
		return
	}
	state := a.loadSyncState()
	if err := a.exportOwn(dir, syncPrefix+own+".json", &state); err != nil {
		log.Printf("sync on close: %v", err)
		return
	}
	a.saveSyncState(state)
}

// peerLabel is the name of another PC as shown to the user: its host name
// (the file name without prefix, random id and extension).
func peerLabel(file string) string {
	n := strings.TrimSuffix(strings.TrimPrefix(file, syncPrefix), ".json")
	if i := strings.LastIndex(n, "-"); i > 0 {
		n = n[:i]
	}
	return n
}
