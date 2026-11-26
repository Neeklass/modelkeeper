package main

import (
	"bytes"
	"context"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"modelkeeper/internal/backup"
	"modelkeeper/internal/catalog"
	"modelkeeper/internal/diag"
	"modelkeeper/internal/msg"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// diagRoot is a root folder as the diagnostics show it.
type diagRoot struct {
	ID     int64  `json:"id"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// ExportDiagnostics writes a zip with the log, the settings, the status files
// and system information (no catalog, no model data) to a file the user
// chooses, to send it along when something goes wrong. Works on the startup
// error screen too. Returns "" if the dialog was cancelled.
func (a *App) ExportDiagnostics() (string, error) {
	if !a.started() || a.layout.Root == "" {
		return "", msg.Wrap(nil, "error.notReady")
	}
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: "modelkeeper-diagnostics-" + time.Now().Format("2006-01-02") + ".zip",
		Filters:         []runtime.FileFilter{{DisplayName: "Zip", Pattern: "*.zip"}},
	})
	if err != nil || file == "" {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(file), ".zip") {
		file += ".zip"
	}
	if a.logFile != nil {
		a.logFile.Sync()
	}
	var buf bytes.Buffer
	if _, err := diag.Write(&buf, diag.Input{DataDir: a.layout.Root, Log: a.layout.Log, System: a.systemInfo(), Now: time.Now()}); err != nil {
		return "", msg.Wrap(err, "diag.failed")
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return "", msg.Wrap(err, "diag.failed")
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return "", msg.Wrap(err, "diag.failed")
	}
	return file, nil
}

func (a *App) systemInfo() map[string]any {
	host, _ := os.Hostname()
	exe, _ := os.Executable()
	info := map[string]any{
		"appVersion": appVersion(),
		"os":         goruntime.GOOS,
		"arch":       goruntime.GOARCH,
		"cpus":       goruntime.NumCPU(),
		"go":         goruntime.Version(),
		"host":       host,
		"exe":        exe,
		"dataDir":    a.layout.Root,
		"time":       time.Now().Format(time.RFC3339),
	}
	if fi, err := os.Stat(a.layout.Catalog); err == nil {
		info["catalogBytes"] = fi.Size()
	}
	if list, err := backup.List(a.layout.Backups); err == nil {
		var names []string
		for _, s := range list {
			names = append(names, s.Name)
		}
		info["backups"] = names
		if n, err := backup.Size(a.layout.Backups); err == nil {
			info["backupBytes"] = n
		}
	}
	if entries, err := os.ReadDir(a.layout.Thumbnails); err == nil {
		info["thumbnailFiles"] = len(entries)
	}
	if a.startupErr != nil {
		info["startupError"] = a.startupErr
	}
	if a.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if roots, err := catalog.ListRoots(ctx, a.db); err == nil {
			var rs []diagRoot
			for _, r := range roots {
				rs = append(rs, diagRoot{ID: r.ID, Path: r.Path, Kind: r.Kind, Status: r.Status})
			}
			info["roots"] = rs
		}
		counts := map[string]int64{}
		for name, q := range map[string]string{
			"models": `SELECT count(*) FROM model`, "locations": `SELECT count(*) FROM location`,
			"tags": `SELECT count(*) FROM tag`, "journal": `SELECT count(*) FROM journal`,
		} {
			var n int64
			if a.db.QueryRowContext(ctx, q).Scan(&n) == nil {
				counts[name] = n
			}
		}
		info["counts"] = counts
		var v int64
		if a.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v) == nil {
			info["schema"] = v
		}
	}
	return info
}
