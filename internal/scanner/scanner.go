// Package scanner walks a root folder, records files in the catalog, hashes
// new/changed files and verifies archives (concept F1–F3, F7, F11).
//
// A scan runs in three phases:
//
//  1. walk:     list all recognized files; compare path+size+mtime with the
//     catalog to find new, changed and missing files (F2).
//  2. hash:     SHA-256 of every file that has no hash yet (F3).
//  3. archives: hash each new/changed archive and read every member fully,
//     which hashes the members and verifies their checksums (F7, F11).
//
// Everything committed before a cancel stays committed, so an interrupted
// initial scan over the NAS continues where it stopped.
package scanner

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/settings"
)

// Phases reported in Progress.Phase.
const (
	PhaseChecking = "checking"
	PhaseWalking  = "walking"
	PhaseHashing  = "hashing"
	PhaseArchives = "archives"
	PhaseFinished = "finished"
)

// Run states (scan_run.status).
const (
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusCancelled = "cancelled"
	StatusFailed    = "failed"
	StatusOffline   = "offline"
)

// Progress is sent to the frontend while a scan runs.
type Progress struct {
	RootID   int64  `json:"rootId"`
	RunID    int64  `json:"runId"`
	Phase    string `json:"phase"`
	Status   string `json:"status"`
	Current  string `json:"current"` // path currently being processed
	Running  bool   `json:"running"`
	Started  int64  `json:"startedAt"`
	Finished int64  `json:"finishedAt,omitempty"`

	FilesSeen    int `json:"filesSeen"`
	FilesNew     int `json:"filesNew"`
	FilesChanged int `json:"filesChanged"`
	FilesMissing int `json:"filesMissing"`

	HashTotal  int   `json:"hashTotal"`
	HashDone   int   `json:"hashDone"`
	BytesTotal int64 `json:"bytesTotal"`
	BytesDone  int64 `json:"bytesDone"`

	ArchivesTotal     int `json:"archivesTotal"`
	ArchivesDone      int `json:"archivesDone"`
	ArchivesBroken    int `json:"archivesBroken"`
	ArchivesEncrypted int `json:"archivesEncrypted"`

	Issues int      `json:"issues"`
	Error  *msg.Msg `json:"error,omitempty"`
}

// Options tune a scan; zero values pick defaults.
type Options struct {
	// HashWorkers is the number of parallel hashing goroutines.
	HashWorkers int
	// ArchiveMemLimit: archives up to this size are read into memory once and
	// both hashed and opened from memory; larger ones are read twice.
	ArchiveMemLimit int64
	// EmitInterval throttles progress events.
	EmitInterval time.Duration
}

func (o Options) withDefaults(rootKind string) Options {
	if o.HashWorkers <= 0 {
		// Parallel reads help on SSDs but make HDDs and the Fritz!NAS seek.
		o.HashWorkers = 2
		if rootKind == "nas" {
			o.HashWorkers = 1
		}
	}
	if o.ArchiveMemLimit <= 0 {
		o.ArchiveMemLimit = 256 << 20
	}
	if o.EmitInterval <= 0 {
		o.EmitInterval = 250 * time.Millisecond
	}
	return o
}

// Manager runs at most one scan at a time in the background (F1).
type Manager struct {
	db       *sql.DB
	settings func() settings.Settings
	emit     func(Progress)
	opts     Options

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	last   *tracker
}

// NewManager creates a manager. Runs left "running" by a crash are marked failed.
func NewManager(db *sql.DB, settingsFn func() settings.Settings, emit func(Progress), opts Options) (*Manager, error) {
	if emit == nil {
		emit = func(Progress) {}
	}
	_, err := db.Exec(`UPDATE scan_run SET status = ?, finished_at = ?, error_key = 'scan.error.interrupted'
		WHERE status = ?`, StatusFailed, nowMs(), StatusRunning)
	if err != nil {
		return nil, err
	}
	return &Manager{db: db, settings: settingsFn, emit: emit, opts: opts}, nil
}

// Start begins scanning a root in the background.
func (m *Manager) Start(rootID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done != nil {
		select {
		case <-m.done:
		default:
			return msg.Wrap(nil, "scan.error.alreadyRunning")
		}
	}
	root, err := catalog.GetRoot(context.Background(), m.db, rootID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &tracker{p: Progress{RootID: rootID, Phase: PhaseChecking, Status: StatusRunning, Running: true, Started: nowMs()}}
	m.cancel, m.done, m.last = cancel, make(chan struct{}), t

	s := &scan{
		db: m.db, root: root, st: m.settings(), opts: m.opts.withDefaults(root.Kind), t: t,
	}
	done := m.done
	go func() {
		defer close(done)
		defer cancel()
		stopEmit := t.emitEvery(s.opts.EmitInterval, m.emit)
		s.run(ctx)
		stopEmit()
		m.emit(t.snapshot())
	}()
	return nil
}

// Cancel stops the running scan, if any. Work done so far is kept.
func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

// Wait blocks until the current scan (if any) has finished.
func (m *Manager) Wait() {
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Close cancels a running scan and waits for it to finish.
func (m *Manager) Close() {
	m.Cancel()
	m.Wait()
}

// Progress returns the state of the current or last scan (nil if none ran).
func (m *Manager) Progress() *Progress {
	m.mu.Lock()
	t := m.last
	m.mu.Unlock()
	if t == nil {
		return nil
	}
	p := t.snapshot()
	return &p
}

// tracker holds the live progress of one scan.
type tracker struct {
	mu        sync.Mutex
	p         Progress
	bytesDone atomic.Int64
}

func (t *tracker) update(fn func(p *Progress)) {
	t.mu.Lock()
	fn(&t.p)
	t.mu.Unlock()
}

func (t *tracker) addBytes(n int64) { t.bytesDone.Add(n) }

func (t *tracker) snapshot() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.p
	p.BytesDone = t.bytesDone.Load()
	return p
}

// emitEvery sends snapshots periodically until the returned stop func is called.
func (t *tracker) emitEvery(d time.Duration, emit func(Progress)) (stop func()) {
	quit := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(d)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
				emit(t.snapshot())
			}
		}
	}()
	return func() { close(quit); wg.Wait() }
}

// scan is the state of one run.
type scan struct {
	db    *sql.DB
	root  catalog.Root
	st    settings.Settings
	opts  Options
	t     *tracker
	runID int64

	pendingArchives []hashJob // loaded in phase 2, processed in phase 3
}

func (s *scan) run(ctx context.Context) {
	status, errKey := s.execute(ctx)
	if ctx.Err() != nil && status == StatusDone {
		status = StatusCancelled
	}
	s.finish(status, errKey)
}

func (s *scan) execute(ctx context.Context) (status, errKey string) {
	res, err := s.db.Exec(`INSERT INTO scan_run (root_id, mode, status, started_at) VALUES (?, 'incremental', ?, ?)`,
		s.root.ID, StatusRunning, s.t.snapshot().Started)
	if err != nil {
		log.Printf("scan: create run: %v", err)
		return StatusFailed, "scan.error.database"
	}
	s.runID, _ = res.LastInsertId()
	s.t.update(func(p *Progress) { p.RunID = s.runID })

	// E3: an unreachable root keeps its entries and is shown as offline.
	if !rootReachable(s.root.Path) {
		s.db.Exec(`UPDATE root SET status = 'offline' WHERE id = ?`, s.root.ID)
		return StatusOffline, "scan.error.offline"
	}
	s.db.Exec(`UPDATE root SET status = 'online' WHERE id = ?`, s.root.ID)

	steps := []struct {
		phase string
		fn    func(context.Context) error
	}{
		{PhaseWalking, s.walk},
		{PhaseHashing, s.hashFiles},
		{PhaseArchives, s.archives},
	}
	for _, step := range steps {
		if ctx.Err() != nil {
			return StatusCancelled, ""
		}
		s.t.update(func(p *Progress) { p.Phase, p.Current = step.phase, "" })
		if err := step.fn(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return StatusCancelled, ""
			}
			var oe *offlineError
			if errors.As(err, &oe) {
				s.db.Exec(`UPDATE root SET status = 'offline' WHERE id = ?`, s.root.ID)
				return StatusOffline, "scan.error.offline"
			}
			log.Printf("scan %s: %v", step.phase, err)
			return StatusFailed, "scan.error.database"
		}
	}
	return StatusDone, ""
}

func (s *scan) finish(status, errKey string) {
	now := nowMs()
	s.t.update(func(p *Progress) {
		p.Phase, p.Status, p.Running, p.Finished, p.Current = PhaseFinished, status, false, now, ""
		if errKey != "" {
			e := msg.New(errKey, "path", s.root.Path)
			p.Error = &e
		}
	})
	if s.runID == 0 {
		return
	}
	p := s.t.snapshot()
	var ek any
	if errKey != "" {
		ek = errKey
	}
	_, err := s.db.Exec(`UPDATE scan_run SET status = ?, finished_at = ?, files_seen = ?, files_new = ?,
		files_changed = ?, files_missing = ?, files_hashed = ?, bytes_hashed = ?, error_key = ? WHERE id = ?`,
		status, now, p.FilesSeen, p.FilesNew, p.FilesChanged, p.FilesMissing,
		p.HashDone, p.BytesDone, ek, s.runID)
	if err != nil {
		log.Printf("scan: finish run: %v", err)
	}
	if status == StatusDone || status == StatusCancelled {
		s.db.Exec(`UPDATE root SET last_scan_at = ? WHERE id = ?`, now, s.root.ID)
	}
}

// issue records a scan problem (written immediately; issues are rare).
func (s *scan) issue(locationID int64, kind, key string, kv ...any) {
	m := msg.New(key, kv...)
	var params any
	if len(m.Params) > 0 {
		b, _ := jsonMarshal(m.Params)
		params = string(b)
	}
	var loc any
	if locationID != 0 {
		loc = locationID
	}
	if _, err := s.db.Exec(`INSERT INTO scan_issue (scan_run_id, location_id, kind, msg_key, params, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, s.runID, loc, kind, key, params, nowMs()); err != nil {
		log.Printf("scan: record issue: %v", err)
	}
	s.t.update(func(p *Progress) { p.Issues++ })
}

func nowMs() int64 { return time.Now().UnixMilli() }
