package watch

import (
	"context"
	"log"
	"time"
)

// Scheduled rescans of NAS roots (concept E4): NAS roots are not watched, so
// they are rescanned incrementally when their last completed scan is older
// than the configured interval. The interval counts from the last scan, so a
// manual scan postpones the next scheduled one, and a scan that was due while
// the app was closed runs soon after start.
//
// An unreachable NAS does not complete a scan; it is tried again after
// OfflineRetry instead of on every check (each try can wait for an SMB timeout).

// checkSchedule queues NAS roots whose scheduled rescan is due.
func (w *Watcher) checkSchedule(ctx context.Context) {
	if w.opts.NASInterval == nil {
		return
	}
	interval := w.opts.NASInterval()
	// While a scan runs, last_scan_at of its root is not updated yet: wait,
	// so a long scan does not queue another one right behind it.
	if interval <= 0 || w.scans.Busy() {
		return
	}
	rows, err := w.db.QueryContext(ctx, `SELECT id, last_scan_at FROM root
		WHERE kind = 'nas' AND last_scan_at IS NOT NULL AND removed_at IS NULL`)
	if err != nil {
		log.Printf("schedule: %v", err)
		return
	}
	type due struct{ id, last int64 }
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.last) == nil {
			list = append(list, d)
		}
	}
	rows.Close()

	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	seen := map[int64]bool{}
	for _, d := range list {
		seen[d.id] = true
		if now.Sub(time.UnixMilli(d.last)) < interval {
			continue
		}
		if _, queued := w.pending[d.id]; queued {
			continue
		}
		if at, ok := w.attempted[d.id]; ok && now.Sub(at) < w.opts.OfflineRetry {
			continue
		}
		w.attempted[d.id] = now
		w.pending[d.id] = now
	}
	for id := range w.attempted {
		if !seen[id] {
			delete(w.attempted, id) // root removed
		}
	}
}
