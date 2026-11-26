package main

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"modelkeeper/internal/backup"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/osopen"
	"modelkeeper/internal/pathutil"
	"modelkeeper/internal/settings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// restoreCheckEvery: without a new snapshot, the restore test is repeated
// after this time (and whenever the last one failed).
const restoreCheckEvery = 24 * time.Hour

// dailyEvery: while the app runs, it looks this often whether the daily
// snapshot is due (a PC that is never shut down still gets one per day).
const dailyEvery = time.Hour

// runDailyTicker takes the daily snapshot while the app keeps running.
func (a *App) runDailyTicker(stop <-chan struct{}) {
	t := time.NewTicker(dailyEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			a.dailyBackup()
		}
	}
}

// afterBackup copies the snapshots to the external backup folder, if one is
// set, and tests that the newest snapshot can be restored, preferably its
// external copy: that is the one needed when the PC breaks (J2). The result
// is stored and shown in the settings. Caller holds backupMu.
func (a *App) afterBackup(force bool) {
	if a.layout.Root == "" || a.settings == nil {
		return
	}
	now := time.Now()
	last := backup.LastRestoreCheck(a.layout.Root)
	if !force && last != nil && last.OK && now.Sub(time.UnixMilli(last.Time)) < restoreCheckEvery {
		return
	}
	newest, ok := backup.Latest(a.layout.Backups)
	external := false
	if dir := a.settings.Get().ExternalBackupDir; dir != "" {
		target, copied, err := backup.Mirror(a.layout.Backups, dir, now)
		if err != nil {
			log.Printf("external backup %s: %v", dir, err)
			a.saveRestoreCheck(backup.RestoreCheck{Time: now.UnixMilli(), Path: target, External: true,
				Problem: "external backup folder: " + err.Error()})
			return
		}
		if len(copied) > 0 {
			log.Printf("external backup: copied %v to %s", copied, target)
		}
		a.copyProgram(target)
		if s, found := backup.Latest(target); found {
			newest, ok, external = s, true, true
		}
	}
	if !ok {
		return
	}
	work := filepath.Join(a.layout.Root, "tmp")
	os.RemoveAll(work) // leftovers of a test the app was closed in
	r := backup.CheckRestore(context.Background(), newest, work, now)
	r.External = external
	if !r.OK {
		log.Printf("restore test of %s failed: %s", newest.Path, r.Problem)
	}
	a.saveRestoreCheck(r)
}

func (a *App) saveRestoreCheck(r backup.RestoreCheck) {
	if err := backup.SaveRestoreCheck(a.layout.Root, r); err != nil {
		log.Printf("restore test result: %v", err)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, EventBackupsChanged, nil)
	}
}

// EventBackupsChanged tells the frontend to reload the backup state.
const EventBackupsChanged = "backups:changed"

// afterBackupAsync runs afterBackup in the background.
func (a *App) afterBackupAsync(force bool) {
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		a.backupMu.Lock()
		defer a.backupMu.Unlock()
		a.afterBackup(force)
	}()
}

// copyProgram puts a copy of modelkeeper.exe next to the external snapshots,
// so a restore on a new PC needs nothing else. Skipped if it is up to date.
func (a *App) copyProgram(target string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	src, err := os.Stat(exe)
	if err != nil {
		return
	}
	dst := filepath.Join(target, filepath.Base(exe))
	if d, err := os.Stat(dst); err == nil && d.Size() == src.Size() && !d.ModTime().Before(src.ModTime()) {
		return
	}
	in, err := os.Open(exe)
	if err != nil {
		return
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		log.Printf("external backup: copy program: %v", err)
		return
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		log.Printf("external backup: copy program: %v", err)
	}
}

// ChooseExternalBackupDir lets the user pick a second backup folder (another
// drive, a cloud folder such as Dropbox, a separate NAS share). Every snapshot
// is copied there, and the copies are tested. Returns "" if the dialog was
// cancelled. The folder must not be the data folder or inside it, and not
// inside a model folder: the app never writes into the library (J1).
func (a *App) ChooseExternalBackupDir() (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{})
	if err != nil || dir == "" {
		return "", err
	}
	if err := a.checkExternalDir(dir); err != nil {
		return "", err
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.ExternalBackupDir = dir }); err != nil {
		return "", msg.Wrap(err, "error.settingsSave")
	}
	a.afterBackupAsync(true)
	return dir, nil
}

func (a *App) checkExternalDir(dir string) error {
	if pathutil.Overlap(dir, a.layout.Root) {
		return msg.Wrap(nil, "backup.externalInDataDir", "path", dir)
	}
	active, err := catalog.ListRoots(a.ctx, a.db)
	if err != nil {
		return wrapDB(err)
	}
	removed, err := catalog.ListRemovedRoots(a.ctx, a.db)
	if err != nil {
		return wrapDB(err)
	}
	for _, r := range append(active, removed...) {
		if pathutil.Within(dir, r.Path) {
			return msg.Wrap(nil, "backup.externalInRoot", "path", dir, "root", r.Path)
		}
	}
	return nil
}

// ClearExternalBackupDir stops copying snapshots to the external folder. The
// copies already there are left alone.
func (a *App) ClearExternalBackupDir() error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.ExternalBackupDir = "" }); err != nil {
		return msg.Wrap(err, "error.settingsSave")
	}
	return nil
}

// CheckRestoreNow copies the snapshots to the external folder (if set) and
// tests the newest one now (button in the settings).
func (a *App) CheckRestoreNow() (*backup.RestoreCheck, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	a.backupMu.Lock()
	a.afterBackup(true)
	a.backupMu.Unlock()
	r := backup.LastRestoreCheck(a.layout.Root)
	if r == nil {
		return nil, msg.Wrap(nil, "backup.nothingToCheck")
	}
	return r, nil
}

// OpenExternalBackupDir opens the external backup folder in the file manager.
func (a *App) OpenExternalBackupDir() error {
	if err := a.ready(); err != nil {
		return err
	}
	dir := a.settings.Get().ExternalBackupDir
	if dir == "" {
		return nil
	}
	p := filepath.Join(dir, backup.ExternalSubdir)
	if err := osopen.Folder(p); err != nil {
		return msg.Wrap(err, "error.openFolder", "path", p)
	}
	return nil
}

// backupFolder resolves a folder the user named for restoring: the external
// backup folder ("" = the one in the settings), or a folder picked by hand
// (its modelkeeper-backups folder if it has one, e.g. the external folder of
// the old PC on a new one).
func (a *App) backupFolder(dir string) (string, error) {
	if dir == "" && a.settings != nil {
		dir = a.settings.Get().ExternalBackupDir
	}
	if dir == "" {
		return "", msg.Wrap(nil, "backup.noExternal")
	}
	if fi, err := os.Stat(backup.ExternalDir(dir)); err == nil && fi.IsDir() {
		return backup.ExternalDir(dir), nil
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", msg.Wrap(err, "backup.externalUnreachable", "path", dir)
	}
	return dir, nil
}

// ListBackupsIn lists the snapshots in another backup folder ("" = the
// external one), newest first. Works on the startup error screen too.
func (a *App) ListBackupsIn(dir string) ([]BackupInfo, error) {
	if !a.started() || a.layout.Backups == "" {
		return nil, msg.Wrap(nil, "error.notReady")
	}
	from, err := a.backupFolder(dir)
	if err != nil {
		return nil, err
	}
	list, err := backup.List(from)
	if err != nil {
		return nil, msg.Wrap(err, "backup.externalUnreachable", "path", from)
	}
	out := []BackupInfo{}
	for _, s := range list {
		b := backupInfo(s)
		b.Bytes = backup.SnapshotSize(s)
		out = append(out, *b)
	}
	return out, nil
}

// PickBackupFolder lets the user choose a folder with snapshots (e.g. the
// external backup folder of the old PC); "" if cancelled.
func (a *App) PickBackupFolder() (string, error) {
	if !a.started() {
		return "", msg.Wrap(nil, "error.notReady")
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{})
}

// RestoreFrom copies snapshot name from another backup folder ("" = the
// external one) into the local backups, checked, and restores it at the
// next start like any snapshot (the current state is kept as a snapshot).
func (a *App) RestoreFrom(dir, name string) error {
	if !a.started() || a.layout.Backups == "" {
		return msg.Wrap(nil, "error.notReady")
	}
	from, err := a.backupFolder(dir)
	if err != nil {
		return err
	}
	if _, err := backup.Fetch(context.Background(), from, a.layout.Backups, name); err != nil {
		return msg.Wrap(err, "backup.fetchFailed", "name", name, "path", from)
	}
	log.Printf("snapshot %s fetched from %s", name, from)
	return a.RequestRestore(name)
}
