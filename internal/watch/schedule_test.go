package watch

import (
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"modelkeeper/internal/db"
)

func TestScheduledNASRescan(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	scan := &fakeScanner{}
	clk := newFakeClock(t)
	var hours atomic.Int64
	hours.Store(24)
	const retry = 1200 * time.Millisecond
	w, err := New(conn, scan, Options{
		Quiet: 50 * time.Millisecond, ScheduleCheck: 40 * time.Millisecond, OfflineRetry: retry,
		NASInterval: func() time.Duration { return time.Duration(hours.Load()) * time.Hour },
		clock:       clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	day := time.Hour.Milliseconds() * 24
	now := clk.Now().UnixMilli()
	add := func(path, kind string, last any) int64 {
		res, err := conn.Exec(`INSERT INTO root (path, kind, last_scan_at, created_at) VALUES (?, ?, ?, 0)`,
			filepath.Join(dir, path), kind, last)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	overdue := add("nas1", "nas", now-day-60_000)
	recent := add("nas2", "nas", now-day/2)
	add("nas3", "nas", nil)          // never scanned: the initial scan stays manual
	add("local", "local", now-2*day) // local roots are watched, not scheduled
	expect := func(what string, want ...int64) {
		t.Helper()
		clk.Advance(250 * time.Millisecond)
		w.flush()
		got := scan.take()
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("%s: started %v, want %v", what, got, want)
		}
	}

	expect("due", overdue)
	// The fake scan did not complete (like an offline NAS): no retry right away...
	expect("no immediate retry")
	// ...but after OfflineRetry (counted from the first try, ~500 ms ago).
	clk.Advance(retry - 400*time.Millisecond)
	expect("retry after pause", overdue)

	// A completed scan resets the timer.
	conn.Exec(`UPDATE root SET last_scan_at = ? WHERE id = ?`, clk.Now().UnixMilli(), overdue)
	clk.Advance(retry)
	expect("after completed scan")

	// A shorter interval makes the other root due; while the scanner is busy, nothing is queued.
	scan.setBusy(true)
	hours.Store(6)
	expect("busy")
	scan.setBusy(false)
	expect("shorter interval", recent)

	// Disabled.
	hours.Store(0)
	conn.Exec(`UPDATE root SET last_scan_at = 1`)
	clk.Advance(retry)
	expect("disabled")
}
