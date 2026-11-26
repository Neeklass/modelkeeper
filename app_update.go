package main

import (
	"context"
	"log"
	"sync"
	"time"

	"modelkeeper/internal/msg"
	"modelkeeper/internal/settings"
	"modelkeeper/internal/update"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// UpdateStatus is the result of the update check.
type UpdateStatus struct {
	Enabled   bool            `json:"enabled"`
	Current   string          `json:"current"`
	CheckedAt int64           `json:"checkedAt"` // unix ms, 0 = not yet
	Latest    *update.Release `json:"latest,omitempty"`
	Newer     bool            `json:"newer"`
	Error     *msg.Msg        `json:"error,omitempty"`
}

// EventUpdateFound: a newer release was found.
const EventUpdateFound = "update:found"

var updMu sync.Mutex
var updStatus UpdateStatus

// checkForUpdate asks GitHub for the newest release (only reading).
func (a *App) checkForUpdate(ctx context.Context) UpdateStatus {
	s := UpdateStatus{Enabled: a.settings.Get().UpdateCheck != "off", Current: appVersion(), CheckedAt: time.Now().UnixMilli()}
	r, err := update.Latest(ctx, nil)
	if err != nil {
		m := msg.New("update.unavailable")
		s.Error = &m
		log.Printf("update check: %v", err)
	} else {
		s.Latest = &r
		s.Newer = update.Newer(s.Current, r.Version)
	}
	updMu.Lock()
	updStatus = s
	updMu.Unlock()
	return s
}

// runUpdateCheck checks once a minute after the start (if not turned off).
func (a *App) runUpdateCheck(stop <-chan struct{}) {
	select {
	case <-stop:
		return
	case <-time.After(time.Minute):
	}
	if a.settings.Get().UpdateCheck == "off" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s := a.checkForUpdate(ctx); s.Newer {
		runtime.EventsEmit(a.ctx, EventUpdateFound, s)
	}
}

// GetUpdateStatus returns the last update check.
func (a *App) GetUpdateStatus() UpdateStatus {
	updMu.Lock()
	s := updStatus
	updMu.Unlock()
	s.Current = appVersion()
	if a.settings != nil {
		s.Enabled = a.settings.Get().UpdateCheck != "off"
	}
	return s
}

// CheckForUpdateNow checks for a new release now.
func (a *App) CheckForUpdateNow() (UpdateStatus, error) {
	if err := a.ready(); err != nil {
		return UpdateStatus{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return a.checkForUpdate(ctx), nil
}

// SetUpdateCheck turns the check at the start on or off.
func (a *App) SetUpdateCheck(on bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	v := "off"
	if on {
		v = ""
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.UpdateCheck = v }); err != nil {
		return msg.Wrap(err, "error.settingsSave")
	}
	return nil
}

// OpenReleasePage opens the page of the newest release in the browser.
func (a *App) OpenReleasePage() {
	s := a.GetUpdateStatus()
	if s.Latest != nil {
		runtime.BrowserOpenURL(a.ctx, s.Latest.URL)
	}
}
