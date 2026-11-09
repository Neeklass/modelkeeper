package watch

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"modelkeeper/internal/db"
)

type fakeScanner struct {
	mu     sync.Mutex
	busy   bool
	starts []int64
}

func (f *fakeScanner) Start(id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, id)
	return nil
}

func (f *fakeScanner) Busy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy
}

func (f *fakeScanner) setBusy(b bool) {
	f.mu.Lock()
	f.busy = b
	f.mu.Unlock()
}

// take returns and clears the recorded starts.
func (f *fakeScanner) take() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.starts
	f.starts = nil
	return s
}

const quiet = 150 * time.Millisecond

type env struct {
	t    *testing.T
	dir  string
	db   *sql.DB
	scan *fakeScanner
	w    *Watcher
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	e := &env{t: t, dir: dir, db: conn, scan: &fakeScanner{}}
	w, err := New(conn, e.scan, Options{
		Quiet: quiet, Retry: 200 * time.Millisecond,
		Exclude:  filepath.Join(dir, "local", "data"),
		Relevant: func(name string) bool { return strings.HasSuffix(name, ".stl") || strings.HasSuffix(name, ".zip") },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	e.w = w
	return e
}

func (e *env) addRoot(name, kind string, scanned bool) (int64, string) {
	e.t.Helper()
	p := filepath.Join(e.dir, name)
	os.MkdirAll(p, 0o755)
	var last any
	if scanned {
		last = 1
	}
	res, err := e.db.Exec(`INSERT INTO root (path, kind, last_scan_at, created_at) VALUES (?, ?, ?, 0)`, p, kind, last)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id, p
}

func (e *env) sync() {
	e.t.Helper()
	if err := e.w.Sync(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

// expectStarts waits for the scans to settle and checks which roots were started (in any order).
func (e *env) expectStarts(what string, want ...int64) {
	e.t.Helper()
	time.Sleep(quiet * 3)
	got := e.scan.take()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		e.t.Fatalf("%s: started %v, want %v", what, got, want)
	}
}

func write(t *testing.T, p string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatchLocalRoots(t *testing.T) {
	e := setup(t)
	local, lp := e.addRoot("local", "local", true)
	nas, _ := e.addRoot("nas", "nas", true)
	fresh, fp := e.addRoot("fresh", "local", false)
	os.MkdirAll(filepath.Join(lp, "Creator", "Orc"), 0o755)

	e.sync()
	if !e.w.Live(local) || e.w.Live(nas) || e.w.Live(fresh) {
		t.Fatalf("live: local %v nas %v fresh %v", e.w.Live(local), e.w.Live(nas), e.w.Live(fresh))
	}
	e.expectStarts("catch-up at startup", local)

	// A burst of changes in an existing subfolder: one scan after the quiet period.
	for i := 0; i < 5; i++ {
		write(t, filepath.Join(lp, "Creator", "Orc", "part"+string(rune('a'+i))+".stl"))
		time.Sleep(quiet / 3)
	}
	e.expectStarts("burst", local)

	// A new folder (watched on the fly), then a file inside it.
	os.MkdirAll(filepath.Join(lp, "Creator", "Elf"), 0o755)
	e.expectStarts("new folder", local)
	write(t, filepath.Join(lp, "Creator", "Elf", "elf.stl"))
	e.expectStarts("file in new folder", local)

	// Irrelevant files and the data folder are ignored.
	write(t, filepath.Join(lp, "Creator", "notes.docx"))
	write(t, filepath.Join(lp, "data", "thumbnails", "x.stl"))
	e.expectStarts("ignored")

	// Removing a folder counts.
	os.RemoveAll(filepath.Join(lp, "Creator", "Elf"))
	e.expectStarts("removed folder", local)

	// A root that was never scanned is not watched; after its first scan it is,
	// without a catch-up scan.
	write(t, filepath.Join(fp, "a.stl"))
	e.expectStarts("unscanned root")
	e.db.Exec(`UPDATE root SET last_scan_at = 1 WHERE id = ?`, fresh)
	e.sync()
	if !e.w.Live(fresh) {
		t.Fatal("fresh root not live after first scan")
	}
	e.expectStarts("no catch-up after first scan")
	write(t, filepath.Join(fp, "b.stl"))
	e.expectStarts("fresh root change", fresh)

	// Removed roots are no longer watched.
	e.db.Exec(`DELETE FROM root WHERE id = ?`, local)
	e.sync()
	write(t, filepath.Join(lp, "Creator", "Orc", "late.stl"))
	e.expectStarts("removed root")
	for _, p := range e.w.fs.WatchList() {
		if within(p, lp) {
			t.Errorf("still watching %s", p)
		}
	}
}

func TestWaitsWhileScannerBusy(t *testing.T) {
	e := setup(t)
	a, ap := e.addRoot("a", "local", true)
	b, bp := e.addRoot("b", "local", true)
	e.sync()
	e.expectStarts("catch-up", a, b)

	e.scan.setBusy(true)
	write(t, filepath.Join(ap, "x.stl"))
	write(t, filepath.Join(bp, "y.stl"))
	e.expectStarts("busy")

	e.scan.setBusy(false)
	time.Sleep(quiet)
	got := e.scan.take()
	if len(got) != 2 {
		t.Fatalf("after busy: started %v, want both roots", got)
	}
}

func TestRetryUnreachableRoot(t *testing.T) {
	e := setup(t)
	id, p := e.addRoot("usb", "local", true)
	os.RemoveAll(p)
	e.sync()
	if e.w.Live(id) {
		t.Fatal("missing root is live")
	}
	e.expectStarts("unreachable")

	os.MkdirAll(p, 0o755) // drive plugged in again
	deadline := time.Now().Add(3 * time.Second)
	for !e.w.Live(id) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !e.w.Live(id) {
		t.Fatal("root not live after it came back")
	}
	e.expectStarts("catch-up after reconnect", id)
}

func TestWithin(t *testing.T) {
	sep := string(filepath.Separator)
	root := sep + "m" + sep + "3D"
	for p, want := range map[string]bool{
		root:                    true,
		root + sep + "a":        true,
		root + sep + "a" + sep:  true,
		sep + "m" + sep + "3DX": false,
		sep + "m":               false,
	} {
		if got := within(p, root); got != want {
			t.Errorf("within(%q) = %v, want %v", p, got, want)
		}
	}
}
