# modelkeeper

Desktop app for cataloging, tagging and searching 3D printing models (resin)
spread across the local PC and a NAS. Windows first, one portable `.exe`,
no server, no cloud. The concept and all requirements are in
[CONCEPT.md](CONCEPT.md); the IDs in commit messages (F4, H2 …) refer to it.

## What it does

- Scans root folders (local and NAS) in the background; local folders are
  watched live, NAS folders are rescanned on a schedule.
- Hashes every file (SHA-256), including files inside zip, rar and 7z
  archives, which are read in place and checked for damage. Progress is
  saved continuously; an interrupted scan continues where it stopped.
- Detects models from the folder structure: creator, part folders, size
  variants, supports. Detection can be corrected; corrections survive rescans.
- Duplicates, models without a backup, files both extracted and archived,
  broken archives, silently changed files (full check).
- Tags with suggestions, favorites, notes, print status, bulk editing.
- Search and filters, list and grid, detail view with a rotatable 3D preview.
- Hashes of a folder can be exported and imported by another user of the
  same folder (e.g. the same NAS), so it is not hashed twice.
- German and English.

The app only ever reads model files: it never moves, changes or deletes them.

## Use

Put `modelkeeper.exe` into a writable folder (not `C:\Program Files`) and
start it. Everything it stores lives next to it in `data\`:

```
modelkeeper\
  modelkeeper.exe
  data\
    catalog.db        catalog
    thumbnails\       thumbnails (incl. manually set ones)
    settings.json     settings
    backups\          automatic snapshots of the above
    modelkeeper.log   log
```

Keep it on the local PC, not on the NAS. Requires WebView2 (included in
Windows 11). The `.exe` is not code-signed, so Windows SmartScreen asks once
on first start.

### Backup

The app takes snapshots of the catalog, the settings and the manually set
thumbnails into `data\backups\`: once a day at start, before every catalog
update, before permanently deleting a root folder or removing file types,
and when you click "Create backup now" in the settings. It keeps the 14
newest daily snapshots and all others from the last 90 days (at least the 3
newest). These snapshots are on the same drive, so also copy the whole
`modelkeeper\` folder to another drive or a cloud folder from time to time,
while the app is closed.

At every start the catalog is checked. If it is damaged, the app stops and
shows the path of the newest snapshot. If `catalog.db` is missing but
snapshots exist, it asks before starting with an empty catalog.

To restore a snapshot:

1. Close modelkeeper.
2. Copy `catalog.db`, `settings.json` and `thumbnails\` from the snapshot
   folder (e.g. `data\backups\2026-11-12_083000_daily\`) into `data\`,
   replacing the files there. `thumbnails\` in a snapshot only holds the
   manually set thumbnails: copy its files into the existing
   `data\thumbnails\` folder, do not replace the folder.
3. Delete `data\catalog.db-wal` and `data\catalog.db-shm` if they exist.
4. Start modelkeeper.

## Build

Requires Go (see `go.mod`) and Node.js.

```
go run github.com/wailsapp/wails/v2/cmd/wails@v2.17.1 build -platform windows/amd64 -trimpath -ldflags "-X main.version=1.0.0"
```

The version is written into each backup's `manifest.json` ("dev" without it).

The result is `build/bin/modelkeeper.exe`. Development with live reload:
`wails dev` (Wails CLI v2.17).

## Test

```
go test ./...
```

`internal/scanner` includes crash tests (the scan process is killed and
resumed) and, if 7-Zip is installed, 7z fixtures. Opt-in performance tests:
`MODELKEEPER_SCALE=10000 go test -run TestScale -v ./internal/scanner`.

## Layout

- `app.go`, `main.go` – Wails bindings (thin) and asset handler
- `internal/scanner` – walking, hashing, archives, resume
- `internal/detect`, `internal/corrections` – model detection and corrections
- `internal/catalog` – queries, tags, favorites, filters
- `internal/watch` – live watching and scheduled NAS rescans
- `internal/thumbs`, `internal/preview` – thumbnails, files for the 3D preview
- `internal/hashshare` – hash export/import
- `internal/backup` – snapshots in data\backups and their rotation
- `internal/db/migrations` – SQLite schema
- `frontend/` – React UI, translations in `src/i18n/locales`
