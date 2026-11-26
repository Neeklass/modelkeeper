package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"modelkeeper/internal/health"
	"modelkeeper/internal/msg"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// healthEvery: the maintenance check runs on its own this often (when no
// scan runs), and can be started by hand in the settings.
const healthEvery = 7 * 24 * time.Hour

// EventHealthChanged: a new health report is there.
const EventHealthChanged = "health:changed"

var healthMu sync.Mutex

func (a *App) healthFile() string { return filepath.Join(a.layout.Root, "health.json") }

// GetHealth returns the last health report (nil: none yet).
func (a *App) GetHealth() *health.Report {
	if !a.started() || a.layout.Root == "" {
		return nil
	}
	b, err := os.ReadFile(a.healthFile())
	if err != nil {
		return nil
	}
	var r health.Report
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// CheckHealthNow runs the maintenance check: catalog integrity, compacting,
// unused thumbnails, free disk space. Fails while a scan runs.
func (a *App) CheckHealthNow() (*health.Report, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	healthMu.Lock()
	defer healthMu.Unlock()
	var r health.Report
	err := a.scans.Exclusive(func(ctx context.Context) error {
		var err error
		r, err = health.Check(ctx, a.db, a.layout.Catalog, health.Options{
			DataDir: a.layout.Root, ThumbDir: a.layout.Thumbnails})
		return err
	})
	if err != nil {
		var me *msg.Error
		if errors.As(err, &me) {
			return nil, err
		}
		return nil, msg.Wrap(err, "health.failed")
	}
	log.Printf("health check: %+v", r)
	if b, err := json.MarshalIndent(r, "", "  "); err == nil {
		tmp := a.healthFile() + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, a.healthFile())
		}
	}
	runtime.EventsEmit(a.ctx, EventHealthChanged, nil)
	return &r, nil
}

// runHealthTicker runs the check weekly, when no scan runs.
func (a *App) runHealthTicker(stop <-chan struct{}) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	first := time.After(5 * time.Minute)
	for {
		select {
		case <-stop:
			return
		case <-first:
		case <-t.C:
		}
		if last := a.GetHealth(); last != nil && time.Since(time.UnixMilli(last.Time)) < healthEvery {
			continue
		}
		if a.scans.Busy() {
			continue
		}
		if _, err := a.CheckHealthNow(); err != nil {
			log.Printf("health check: %v", err)
		}
	}
}
