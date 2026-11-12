// Package watch watches local root folders for changes and starts incremental
// scans (concept E4). NAS roots are not watched: change notifications over
// SMB are unreliable and keep the NAS awake.
//
// Changes are collected per root until the folder has been quiet for a while
// (copying a large model produces a stream of events), then an incremental
// scan of that root is queued. Only one scan runs at a time, so queued roots
// wait until the scanner is idle.
//
// Only roots that were scanned at least once are watched: the initial scan
// can take long and stays a deliberate action of the user. When watching
// starts (app start, or after the first scan) a catch-up scan picks up
// changes made while the app was not watching.
//
// NAS roots get scheduled incremental rescans instead (schedule.go).
package watch

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/pathutil"
)

// Scanner starts background scans (scanner.Manager).
type Scanner interface {
	Start(rootID int64) error
	Busy() bool
}

// Options tune the watcher; zero values pick defaults.
type Options struct {
	// Quiet is how long a root must be free of changes before it is scanned.
	Quiet time.Duration
	// Retry is the interval for retrying roots that could not be watched
	// (e.g. a disconnected USB drive).
	Retry time.Duration
	// Exclude is a folder whose changes are ignored (the data folder, in
	// case it lies inside a root).
	Exclude string
	// Relevant reports whether a changed file can matter to the catalog
	// (a recognized extension). Nil means every file is relevant.
	Relevant func(name string) bool

	// NASInterval returns the interval of scheduled NAS rescans; zero or
	// nil disables them. Read on every check, so setting changes apply at once.
	NASInterval func() time.Duration
	// ScheduleCheck is how often scheduled rescans are checked.
	ScheduleCheck time.Duration
	// OfflineRetry is the pause after a scheduled rescan that did not
	// complete (e.g. the NAS was unreachable) before it is tried again.
	OfflineRetry time.Duration

	clock clock // tests: virtual time; nil = real time
}

func (o Options) withDefaults() Options {
	if o.Quiet <= 0 {
		o.Quiet = 3 * time.Second
	}
	if o.Retry <= 0 {
		o.Retry = time.Minute
	}
	if o.ScheduleCheck <= 0 {
		o.ScheduleCheck = time.Minute
	}
	if o.OfflineRetry <= 0 {
		o.OfflineRetry = 30 * time.Minute
	}
	if o.clock == nil {
		o.clock = realClock{}
	}
	return o
}

type root struct {
	path   string // cleaned absolute path
	live   bool   // watches are in place
	warned bool   // the failure to watch was logged
}

// Watcher watches all scanned local roots.
type Watcher struct {
	db    *sql.DB
	scans Scanner
	opts  Options
	fs    *fsnotify.Watcher

	mu      sync.Mutex
	roots   map[int64]*root
	pending map[int64]time.Time // root -> scan when this time has passed
	// attempted: when a scheduled NAS rescan was last queued (schedule.go).
	attempted map[int64]time.Time

	flushReq chan chan struct{} // tests: wait until the loop handled what it received

	syncMu sync.Mutex // serializes Sync
	synced bool       // the first Sync has run
	quit   chan struct{}
	done   chan struct{}
}

// New creates a watcher and starts its event loop. Call Sync to pick up roots.
func New(db *sql.DB, scans Scanner, opts Options) (*Watcher, error) {
	fw, err := fsnotify.NewBufferedWatcher(4096)
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		db: db, scans: scans, opts: opts.withDefaults(), fs: fw,
		roots: map[int64]*root{}, pending: map[int64]time.Time{}, attempted: map[int64]time.Time{},
		quit: make(chan struct{}), done: make(chan struct{}), flushReq: make(chan chan struct{}),
	}
	go w.loop()
	return w, nil
}

// Close stops watching.
func (w *Watcher) Close() {
	select {
	case <-w.quit:
		return
	default:
	}
	close(w.quit)
	<-w.done
	w.fs.Close()
}

// Live reports whether a root is currently watched.
func (w *Watcher) Live(rootID int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.roots[rootID]
	return r != nil && r.live
}

// Sync aligns the watches with the catalog: starts watching new (scanned,
// local) roots and stops watching removed ones. Roots that start being
// watched get a catch-up scan. Safe to call at any time.
func (w *Watcher) Sync(ctx context.Context) error {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	all, err := catalog.ListRoots(ctx, w.db)
	if err != nil {
		return err
	}
	want := map[int64]string{}
	for _, r := range all {
		if r.Kind == "local" && r.LastScanAt != nil {
			want[r.ID] = filepath.Clean(r.Path)
		}
	}

	w.mu.Lock()
	var drop []string
	for id, r := range w.roots {
		if p, ok := want[id]; !ok || p != r.path {
			drop = append(drop, r.path)
			delete(w.roots, id)
			delete(w.pending, id)
		}
	}
	// Catch-up scans: at the first Sync (changes while the app was closed) and
	// for roots that were unreachable. A root that was just scanned for the
	// first time needs none.
	add := map[int64]bool{} // root -> catch up
	for id, p := range want {
		if r, ok := w.roots[id]; !ok {
			w.roots[id] = &root{path: p}
			add[id] = !w.synced
		} else if !r.live {
			add[id] = true // retry
		}
	}
	w.synced = true
	w.mu.Unlock()

	for _, p := range drop {
		w.unwatchTree(p)
	}
	for id, catchUp := range add {
		w.mu.Lock()
		r := w.roots[id]
		w.mu.Unlock()
		if r == nil {
			continue
		}
		if err := w.watchTree(r.path); err != nil {
			w.mu.Lock()
			if !r.warned { // retried every minute: log once
				log.Printf("watch %s: %v", r.path, err)
				r.warned = true
			}
			w.mu.Unlock()
			continue
		}
		w.mu.Lock()
		if w.roots[id] == r {
			r.live = true
			if catchUp {
				w.pending[id] = w.opts.clock.Now()
			}
		}
		w.mu.Unlock()
	}
	return nil
}

// watchTree adds watches for a folder and all folders below it.
// An unreachable top folder is an error; unreadable subfolders are skipped.
func (w *Watcher) watchTree(top string) error {
	if fi, err := os.Stat(top); err != nil {
		return err
	} else if !fi.IsDir() {
		return errors.New("not a folder")
	}
	var firstErr error
	filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == top {
				firstErr = err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if w.excluded(p) {
			return fs.SkipDir
		}
		if err := w.fs.Add(p); err != nil {
			if p == top {
				firstErr = err
				return fs.SkipAll
			}
			// e.g. the inotify watch limit on Linux; the scan still catches
			// changes there on the next catch-up.
			log.Printf("watch %s: %v", p, err)
		}
		return nil
	})
	return firstErr
}

// unwatchTree removes the watches of a folder and all folders below it.
func (w *Watcher) unwatchTree(top string) {
	for _, p := range w.fs.WatchList() {
		if within(p, top) {
			w.fs.Remove(p)
		}
	}
}

func (w *Watcher) excluded(p string) bool {
	return w.opts.Exclude != "" && within(p, filepath.Clean(w.opts.Exclude))
}

// within reports whether p is dir or lies below it.
func within(p, dir string) bool { return pathutil.Within(p, dir) }

func (w *Watcher) loop() {
	defer close(w.done)
	tick, stopTick := w.opts.clock.NewTicker(w.opts.Quiet / 4)
	defer stopTick()
	retry, stopRetry := w.opts.clock.NewTicker(w.opts.Retry)
	defer stopRetry()
	schedule, stopSchedule := w.opts.clock.NewTicker(w.opts.ScheduleCheck)
	defer stopSchedule()
	for {
		select {
		case <-w.quit:
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// An overflow loses events: rescan every watched root.
			log.Printf("watch: %v", err)
			w.markAll()
		case <-tick:
			w.startDue()
		case done := <-w.flushReq:
			close(done)
		case <-retry:
			if w.hasDead() {
				go w.Sync(context.Background())
			}
		case <-schedule:
			w.checkSchedule(context.Background())
		}
	}
}

// handle records a change and keeps watches in step with new folders.
func (w *Watcher) handle(ev fsnotify.Event) {
	if ev.Op == fsnotify.Chmod || w.excluded(ev.Name) {
		return
	}
	id, ok := w.rootOf(ev.Name)
	if !ok {
		return
	}
	isDir := false
	if ev.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			isDir = true
			// Files created inside before the watch was added are found by the scan.
			if err := w.watchTree(ev.Name); err != nil {
				log.Printf("watch %s: %v", ev.Name, err)
			}
		}
	}
	// A removed or renamed path may have been a folder full of models;
	// a changed file only matters if the catalog knows its type.
	gone := ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)
	if !isDir && !gone && w.opts.Relevant != nil && !w.opts.Relevant(ev.Name) {
		return
	}
	if gone && filepath.Ext(ev.Name) != "" && w.opts.Relevant != nil && !w.opts.Relevant(ev.Name) {
		return // an unrecognized file, not a folder
	}
	w.mu.Lock()
	w.pending[id] = w.opts.clock.Now().Add(w.opts.Quiet)
	w.mu.Unlock()
}

func (w *Watcher) rootOf(p string) (int64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, r := range w.roots {
		if r.live && within(p, r.path) {
			return id, true
		}
	}
	return 0, false
}

func (w *Watcher) markAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	due := w.opts.clock.Now().Add(w.opts.Quiet)
	for id, r := range w.roots {
		if r.live {
			w.pending[id] = due
		}
	}
}

func (w *Watcher) hasDead() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.roots {
		if !r.live {
			return true
		}
	}
	return false
}

// startDue starts the scan of one root whose quiet period has passed, if
// the scanner is idle. Further roots follow on later ticks.
func (w *Watcher) startDue() {
	if w.scans.Busy() {
		return
	}
	now := w.opts.clock.Now()
	w.mu.Lock()
	var next int64
	var nextAt time.Time
	for id, at := range w.pending {
		if !at.After(now) && (next == 0 || at.Before(nextAt)) {
			next, nextAt = id, at
		}
	}
	w.mu.Unlock()
	if next == 0 {
		return
	}
	if err := w.scans.Start(next); err != nil {
		// Most likely a scan was started in between; try again later.
		if !w.scans.Busy() {
			log.Printf("watch: scan root %d: %v", next, err)
			w.mu.Lock()
			delete(w.pending, next)
			w.mu.Unlock()
		}
		return
	}
	w.mu.Lock()
	// Changes that arrived after nextAt need another scan; keep them.
	if at, ok := w.pending[next]; ok && !at.After(nextAt) {
		delete(w.pending, next)
	}
	w.mu.Unlock()
}
