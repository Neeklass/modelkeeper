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
// A full check (F9) re-hashes every file and archive regardless of size and
// modification time. A file whose content changed although size and
// modification time did not is reported as possible corruption (F8).
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
	"modelkeeper/internal/detect"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/settings"
	"modelkeeper/internal/thumbs"
)

// Phases reported in Progress.Phase.
const (
	PhaseChecking   = "checking"
	PhaseWalking    = "walking"
	PhaseHashing    = "hashing"
	PhaseArchives   = "archives"
	PhaseDetecting  = "detecting"
	PhaseThumbnails = "thumbnails"
	PhaseFinished   = "finished"
)

// Scan modes (Progress.Mode, scan_run.mode).
const (
	ModeIncremental = "incremental"
	ModeFull        = "full"
	ModeDetect      = "detect" // detection only, no scan_run
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
	Mode     string `json:"mode"`
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

	ModelsFound   int `json:"modelsFound"`
	ModelsCreated int `json:"modelsCreated"`

	ThumbsTotal int `json:"thumbsTotal"`
	ThumbsDone  int `json:"thumbsDone"`

	// HashChanged counts files whose content changed at unchanged size and
	// modification time (F8).
	HashChanged int `json:"hashChanged"`

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
	// ThumbDir is the thumbnail folder; empty disables thumbnail generation.
	ThumbDir string
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

// Start begins scanning a root in the background. Model detection runs at the end.
func (m *Manager) Start(rootID int64) error { return m.start(rootID, ModeIncremental) }

// StartFull begins a full check of a root in the background: every file and
// archive is read and hashed again (F9).
func (m *Manager) StartFull(rootID int64) error { return m.start(rootID, ModeFull) }

// Redetect re-runs only model detection for a root in the background
// (e.g. after the creator-level setting changed).
func (m *Manager) Redetect(rootID int64) error { return m.start(rootID, ModeDetect) }

func (m *Manager) start(rootID int64, mode string) error {
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
	phase := PhaseChecking
	if mode == ModeDetect {
		phase = PhaseDetecting
	}
	t := &tracker{p: Progress{RootID: rootID, Mode: mode, Phase: phase, Status: StatusRunning, Running: true, Started: nowMs()}}
	m.cancel, m.done, m.last = cancel, make(chan struct{}), t

	s := &scan{
		db: m.db, root: root, st: m.settings(), opts: m.opts.withDefaults(root.Kind), t: t,
		detectOnly: mode == ModeDetect, full: mode == ModeFull,
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

// Busy reports whether a scan is running.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done == nil {
		return false
	}
	select {
	case <-m.done:
		return false
	default:
		return true
	}
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
	detectOnly      bool
	full            bool // F9: re-hash everything
}

func (s *scan) run(ctx context.Context) {
	if s.detectOnly {
		status, errKey, detail := StatusDone, "", ""
		err := s.detectModels(ctx)
		if err == nil {
			s.t.update(func(p *Progress) { p.Phase = PhaseThumbnails })
			err = s.thumbnails(ctx)
		}
		if err != nil {
			status, errKey, detail = StatusFailed, "scan.error.database", "detecting: "+err.Error()
			if errors.Is(err, context.Canceled) {
				status, errKey, detail = StatusCancelled, "", ""
			}
			log.Printf("detect: %v", err)
		}
		s.finish(status, errKey, detail)
		return
	}
	status, errKey, detail := s.execute(ctx)
	if ctx.Err() != nil && status == StatusDone {
		status = StatusCancelled
	}
	s.finish(status, errKey, detail)
}

// execute runs the phases. detail is the technical error text for failures;
// it is stored with the run and shown to the user.
func (s *scan) execute(ctx context.Context) (status, errKey, detail string) {
	mode := ModeIncremental
	if s.full {
		mode = ModeFull
	}
	res, err := s.db.Exec(`INSERT INTO scan_run (root_id, mode, status, started_at) VALUES (?, ?, ?, ?)`,
		s.root.ID, mode, StatusRunning, s.t.snapshot().Started)
	if err != nil {
		log.Printf("scan: create run: %v", err)
		return StatusFailed, "scan.error.database", err.Error()
	}
	s.runID, _ = res.LastInsertId()
	s.t.update(func(p *Progress) { p.RunID = s.runID })

	// E3: an unreachable root keeps its entries and is shown as offline.
	if !rootReachable(s.root.Path) {
		s.db.Exec(`UPDATE root SET status = 'offline' WHERE id = ?`, s.root.ID)
		return StatusOffline, "scan.error.offline", ""
	}
	s.db.Exec(`UPDATE root SET status = 'online' WHERE id = ?`, s.root.ID)

	steps := []struct {
		phase string
		fn    func(context.Context) error
	}{
		{PhaseWalking, s.walk},
		{PhaseHashing, s.hashFiles},
		{PhaseArchives, s.archives},
		{PhaseDetecting, s.detectModels},
		{PhaseThumbnails, s.thumbnails},
	}
	for _, step := range steps {
		if ctx.Err() != nil {
			return StatusCancelled, "", ""
		}
		s.t.update(func(p *Progress) { p.Phase, p.Current = step.phase, "" })
		if err := step.fn(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return StatusCancelled, "", ""
			}
			var oe *offlineError
			if errors.As(err, &oe) {
				s.db.Exec(`UPDATE root SET status = 'offline' WHERE id = ?`, s.root.ID)
				return StatusOffline, "scan.error.offline", ""
			}
			log.Printf("scan %s: %v", step.phase, err)
			return StatusFailed, "scan.error.database", step.phase + ": " + err.Error()
		}
	}
	return StatusDone, "", ""
}

func (s *scan) finish(status, errKey, detail string) {
	now := nowMs()
	s.t.update(func(p *Progress) {
		p.Phase, p.Status, p.Running, p.Finished, p.Current = PhaseFinished, status, false, now, ""
		if errKey != "" {
			e := msg.New(errKey, "path", s.root.Path, "detail", detail)
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

// detectModels is phase 4: suggest models from the files (F4).
func (s *scan) detectModels(ctx context.Context) error {
	rules := detect.Rules{PartNames: s.st.PartNames, SizeTokens: s.st.SizeTokens}
	var res detect.Result
	err := withRetry(ctx, func(ctx context.Context) error {
		var err error
		res, err = detect.Apply(ctx, s.db, s.root.ID, rules)
		return err
	})
	if err != nil {
		return err
	}
	s.t.update(func(p *Progress) { p.ModelsFound, p.ModelsCreated = res.Models, res.Created })
	log.Printf("detect root %d: %+v", s.root.ID, res)
	return nil
}

// Exclusive runs fn while no scan or detection may start, and fails if one
// is running. Used for manual corrections, which re-run detection themselves.
func (m *Manager) Exclusive(fn func(ctx context.Context) error) error {
	m.mu.Lock()
	if m.done != nil {
		select {
		case <-m.done:
		default:
			m.mu.Unlock()
			return msg.Wrap(nil, "scan.error.alreadyRunning")
		}
	}
	done := make(chan struct{})
	m.done = done
	m.cancel = func() {}
	m.mu.Unlock()
	defer close(done)
	return fn(context.Background())
}

// DetectRules returns the detection rules from the current settings.
func (m *Manager) DetectRules() detect.Rules {
	st := m.settings()
	return detect.Rules{PartNames: st.PartNames, SizeTokens: st.SizeTokens}
}

// thumbnails is phase 5: create thumbnails for models that have none yet (I1, I3).
// Failures of single models are logged; the model is not retried automatically.
func (s *scan) thumbnails(ctx context.Context) error {
	if s.opts.ThumbDir == "" {
		return nil
	}
	ids, err := thumbs.Pending(ctx, s.db, s.root.ID)
	if err != nil {
		return err
	}
	s.t.update(func(p *Progress) { p.ThumbsTotal, p.ThumbsDone = len(ids), 0 })
	store := &thumbs.Store{DB: s.db, Dir: s.opts.ThumbDir}
	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := store.Generate(ctx, id); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			if !rootReachable(s.root.Path) {
				return &offlineError{err}
			}
			log.Printf("thumbnail for model %d: %v", id, err)
		}
		s.t.update(func(p *Progress) { p.ThumbsDone = i + 1 })
	}
	return nil
}
