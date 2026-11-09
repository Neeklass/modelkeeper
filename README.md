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
    modelkeeper.log   log
```

Backup: copy the whole `modelkeeper\` folder while the app is closed.
Keep it on the local PC, not on the NAS. Requires WebView2 (included in
Windows 11). The `.exe` is not code-signed, so Windows SmartScreen asks once
on first start.

## Build

Requires Go (see `go.mod`) and Node.js.

```
go run github.com/wailsapp/wails/v2/cmd/wails@v2.17.1 build -platform windows/amd64 -trimpath
```

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
- `internal/db/migrations` – SQLite schema
- `frontend/` – React UI, translations in `src/i18n/locales`
