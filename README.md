# modelkeeper

Desktop app for cataloging, tagging and searching 3D printing models (resin)
spread across the local PC and a NAS. Windows first, one portable `.exe`,
no server, no cloud. The concept and all requirements are in
[CONCEPT.md](CONCEPT.md); the IDs in commit messages (F4, H2 …) refer to it.

## What it does

- Scans root folders (mainly on the NAS, also local) in the background;
  local folders are watched live, NAS folders are rescanned on a schedule.
  Scans can be paused and resumed; a scan the app was closed in continues
  at the next start. Removing a root
  folder only deactivates it; adding it again links tags and notes up again.
  Deleting it permanently from the catalog is a separate, confirmed step.
- Hashes every file (SHA-256), including files inside zip, rar and 7z
  archives, which are read in place and checked for damage. Progress is
  saved continuously; an interrupted scan continues where it stopped.
- Detects models from the folder structure: creator, part folders, size
  variants, supports. Detection can be corrected; corrections survive rescans.
- Duplicates, models without a backup, files both extracted and archived,
  broken archives, silently changed files (full check).
- Tags with suggestions, favorites, notes, print status, bulk editing.
- Sidebar with views (all, to catalog, favorites, recently added), creators,
  tags and locations; search, filter chips, grid with three tile sizes or
  list, keyboard navigation; detail view with picture gallery, rotatable 3D
  preview and a cataloging flow (tag, Enter, next model).
- "Open in slicer" opens a read-only copy, never the original.
- Hashes of a folder can be exported and imported by another user of the
  same folder (e.g. the same NAS), so it is not hashed twice.
- German and English, with a built-in user manual (Help tab).
  The manual lives in the translation files (`manual` key), so a new
  language brings its own manual.

The app only ever reads model files: it never moves, changes or deletes them,
and no other program gets the originals from it.

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
    backups\          automatic snapshots of the above (each with catalog-export.json)
    modelkeeper.log   log
```

It must be on the local PC, not on the NAS (the app refuses to start from a
network share). Requires WebView2 (included in
Windows 11). The `.exe` is not code-signed, so Windows SmartScreen asks once
on first start.

### Export, import, undo

**Settings → Export and import → Export…** writes one JSON file with
everything you entered: tags, notes, favorites, print status, edited names,
creators, origin and license, corrections of the detection, manually set
thumbnails, the root folders and the detection settings. Every snapshot also
contains it as `catalog-export.json`. It is plain JSON and does not depend on
`catalog.db`: if the catalog and all snapshots were lost, this one file
restores the cataloging work.

**Import…** shows a preview first: which root folder of the file belongs to
which folder here (or add it, or skip it), and how many models are found by
their ID, by their place or by their files' fingerprints (moved or renamed
models too). Models not found yet keep their data and are linked up when a
scan finds their files. The import only adds – nothing in the catalog is
removed or overwritten – and takes a `pre-import` snapshot first. The same
way, a catalog prepared on one PC can be handed to someone else.

Changes of tags, fields and favorites can be undone: **Undo** in the message
at the bottom, **Ctrl+Z**, or Settings → Recent changes.

### Moving the model folders

If the models move as a whole (a new NAS, another drive letter), use
**Folders → Change path…** at the folder instead of adding the new place as
a new folder. modelkeeper looks for a sample of the files at the new place,
takes a snapshot, changes the path and scans; the folder keeps its tags,
notes and corrections.

### Installing and moving modelkeeper

Unzip the release into a permanent folder (e.g. `C:\Users\<you>\modelkeeper`)
and start `modelkeeper.exe` there. modelkeeper refuses to start from inside a
zip file (Windows' temporary folder) and warns in Downloads or in a OneDrive /
Dropbox folder. Program and `data\` belong together: move or copy the whole
folder and everything goes along. At the next start modelkeeper notices the
move and offers to update the paths of model folders that moved along (a
portable disk with another drive letter, model folders next to the app).

**Settings → Configuration** exports the settings and the folder list as a JSON
file and imports it on another PC (choose which parts to take over).

### Backup

The app takes snapshots of the catalog, the settings and the manually set
thumbnails into `data\backups\`: once a day at start (skipped if nothing
changed since the newest snapshot), before every catalog update, before
permanently deleting a root folder, before removing file types or moving
them to another category, and when you click "Create backup now" in the
settings. The settings show how many snapshots there are and their size. It keeps the 14
newest daily snapshots and all others from the last 90 days (at least the 3
newest). Snapshots are also taken once a day while the app keeps running. They are on
the same drive, so choose a second backup folder in the settings (another
drive, Dropbox, a separate NAS share): every snapshot is copied there together
with `modelkeeper.exe` and a `README.txt` for restoring on a new PC. After
every new snapshot and at least daily, the newest snapshot (preferably the
external copy) is restore-tested: content hash, full `integrity_check` of a
temporary copy, counts against the manifest. The result is shown in the
settings.

At every start the catalog is checked (about one second for 100,000
models). If it is damaged, the app stops and
shows the path of the newest snapshot. If `catalog.db` is missing but
snapshots exist, it asks before starting with an empty catalog.

To restore a snapshot, open "Show and restore backups" in the settings (or
on the error screen after a failed start), choose one and restart
modelkeeper. At the next start, before anything is opened, the snapshot is
checked and put in place; the state it replaces is kept as a
`pre-restore` snapshot, so a restore can be undone the same way.

By hand:

1. Close modelkeeper.
2. Copy `catalog.db`, `settings.json` and `thumbnails\` from the snapshot
   folder (e.g. `data\backups\2026-11-12_083000_daily\`) into `data\`,
   replacing the files there. `thumbnails\` in a snapshot only holds the
   manually set thumbnails: copy its files into the existing
   `data\thumbnails\` folder (create it if it is gone).
3. Delete `data\catalog.db-wal` and `data\catalog.db-shm` if they exist.
4. Start modelkeeper. Generated thumbnails that are missing are generated
   again in the background.

## Build

Requires Go (see `go.mod`) and Node.js.

```
go run github.com/wailsapp/wails/v2/cmd/wails@v2.17.1 build -platform windows/amd64 -trimpath -ldflags "-X main.version=$(git describe --tags --always --dirty)"
```

Each backup's `manifest.json` records the app version. Wails builds
without git information, so the command above passes it (the tag, or the
commit with `-dirty` for uncommitted changes). Without `-ldflags`, plain
`go build` builds take it from git; Wails builds record "dev".

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
- `internal/backup` – snapshots in data\backups, rotation, external copies, restore tests
- `internal/userexport` – readable export (catalog-export.json) and import of all user data
- `internal/opencopy` – read-only copies for "Open in slicer"
- `internal/db/migrations` – SQLite schema
- `frontend/` – React UI, translations in `src/i18n/locales`
