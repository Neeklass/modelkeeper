# Release test

A test by hand on a real PC before a release. Takes about 2 hours; parts 1–5 and 8 are required for
every release, parts 6–7 when sync or the phone view are used. Tick the boxes as you go; write down
anything odd with the time, and export the diagnostics for it (Settings → Maintenance).

**Golden rule:** never test with the only copy of a real data folder. Model folders are safe (the app
only reads them), the data folder (catalog, backups) is what you protect.

## 0. Get a test build

- [ ] GitHub → Actions → **Release** → **Run workflow** (branch `main`). After about 15 minutes the
      run has two artifacts, `windows` and `linux`. Download `windows` (a zip that contains
      `modelkeeper-…-windows-amd64.zip`).
- [ ] Unzip to a **new** folder, e.g. `C:\mk-test\modelkeeper\` (not `C:\Program Files`, not the NAS).

## 1. Prepare test data

- [ ] **If modelkeeper is already in use:** close it and copy the whole `modelkeeper` folder (with
      `data\`) to a safe place, e.g. `C:\mk-safe\`. That copy is not touched during the test.
- [ ] A small test library, e.g. `D:\mk-library\`, copied from real files (about 10 models):
  - [ ] Creator folders with model folders (`Creator\Model\part.stl`), one with `Supported\` and
        `Unsupported\` or `pre-supported` subfolders, one with size variants (`32mm`, `75mm`)
  - [ ] a model only as `.zip` (and one `.7z` or `.rar` if you have one)
  - [ ] one `.obj`, one `.3mf` with and one without a preview picture
  - [ ] one sliced file: `.ctb` (resin) and/or `.gcode`/`.3mf` from PrusaSlicer, Bambu or Orca
  - [ ] a model folder with a `.jpg` render, and the same STL copied into a second folder (a duplicate)
- [ ] A second drive or USB stick (or a Dropbox folder) for the external backup.

## 2. Fresh start (Windows)

- [ ] Start `modelkeeper.exe`. SmartScreen asks once ("More info" → "Run anyway").
- [ ] The first-run guide appears: language, **Start fresh**, add `D:\mk-library\`, external backup
      folder on the second drive, tour, **Start**.
- [ ] The scan runs (progress at the bottom of the sidebar); the models appear with sensible names
      and creators; supports and size variants are recognized.
- [ ] Thumbnails: from the `.jpg`, from the 3MF preview, rendered from STL, OBJ and the 3MF without
      preview.
- [ ] Settings → General: a daily backup exists; the external folder holds a copy
      (`modelkeeper-backups`), the restore test shows OK.

## 3. Daily cataloging

For each action: it works, it shows up in the history (Settings → Tags), and **Ctrl+Z** undoes it.

- [ ] Open a model, type a tag, Enter → the next model opens ("To catalog" view)
- [ ] Notes, favorite, print status, supports, creator, origin and license on one model
- [ ] Select several (Ctrl/Shift+click): add and remove tags, **Edit fields…**, merge two models
- [ ] Rename a tag; merge two tags; an alias ("elves" → "elf") and typing the alias; a tag group
- [ ] Collections: create, add models, reorder ↑↓, rename, **Export as zip…** (open the zip: files
      are there, the originals unchanged)
- [ ] Save a search; open it from the sidebar
- [ ] Print log: an entry with printer and settings; **+ Photo** with a phone photo (upright?);
      **As cover**
- [ ] Sliced file in the file list → **Show print information** (time, layers, material plausible?)
- [ ] 3D preview; **Open in slicer** opens a copy (the path is in `data\open\`, not the library)
- [ ] Statistics page: sizes plausible, the duplicate is listed

## 4. Safety (the important part)

- [ ] **Crash during a scan:** add a larger folder, kill modelkeeper in the Task Manager while it
      scans, start again → the scan continues, nothing entered is lost.
- [ ] **Folder offline:** disconnect the drive/NAS of a root folder → it shows offline, its models
      stay with all tags; reconnect → online again, nothing lost.
- [ ] **Files moved:** in Explorer, rename a model folder and move another one to a different
      creator → rescan → both keep their tags, notes and print log.
- [ ] **Whole library moved:** Folders → change the path of a root folder to the moved copy → all
      models keep their data.
- [ ] **App folder moved:** close, move `C:\mk-test\modelkeeper` to `D:\mk-test2\`, start → the
      catalog is there; the "folder was moved" notice explains it.
- [ ] **Restore:** Settings → General → Backups → **Create backup**, then change some tags, restore
      that backup, restart → the tags are as before; the state before the restore is a backup too.
- [ ] **Restore from another folder:** **Restore from another folder…** → the external backup folder
      → restore an older backup → restart → works.
- [ ] **Export / import:** Settings → Export → export the cataloging work (JSON). Unzip the build a
      second time to `C:\mk-test3\`, start it, skip the guide, import the JSON, add the same library
      → the same tags, notes, collections, prints and photos as in the first one. Undo the import in
      the history → the data is gone again; import again.
- [ ] **Configuration:** export the configuration and import it into the third copy → language, file
      types, folders the same.
- [ ] **Health check:** Settings → Maintenance → **Check now** → all OK.
- [ ] **Diagnostics:** **Export diagnostics…** → open the zip: log, settings, `system.json`, no
      `catalog.db`, no pictures.

## 5. Update from the version in use (only if modelkeeper is already in use)

This proves the existing catalog survives the update.

- [ ] Copy the **saved** folder from part 1 (`C:\mk-safe\modelkeeper`) to `C:\mk-upgrade\`.
- [ ] Replace only `modelkeeper.exe` there with the new one. Start.
- [ ] Settings → General → Backups: a new backup "before update" exists.
- [ ] Count: number of models, tags, collections and a few notes match the old version (look at
      the same models before and after; Statistics shows totals).
- [ ] The way back: copy `C:\mk-upgrade` to `C:\mk-old` and put the **old** `.exe` there. It refuses
      the newer catalog and offers its backups → restore "before update" → restart → the old version
      works with the old data.

## 6. Two PCs (if sync is used)

- [ ] On both PCs: Settings → Export → **Sync with other PCs** → the same shared folder.
- [ ] PC A: add a tag to a model. PC B: remove a tag from another model and change a note.
- [ ] **Sync now** on A, then on B, then on A → both PCs show all three changes.
- [ ] The history shows "Sync with …"; undoing it on one PC and syncing again → both agree.

## 7. Phone and tablet (if used)

- [ ] Settings → Maintenance → turn on; Windows asks about the firewall → **private networks only**.
- [ ] Open the shown link on the phone (same Wi-Fi): search, tags, collection, a model with photos.
- [ ] The address without `?k=…` in a private browser tab → "open the link" message, no data.
- [ ] Turn it off → the phone gets no answer.

## 8. Linux (short)

- [ ] Unpack `linux` → `modelkeeper-…-linux-amd64.tar.gz`, start `./modelkeeper`.
- [ ] Add a folder, scan, a tag, a backup, a restore.
- [ ] Unpacked into a read-only folder (e.g. owned by root): data goes to
      `~/.local/share/modelkeeper`.

## 9. Release

When all required parts pass and the problems found are fixed:

```
git checkout main && git pull
git tag -a v1.0.0 -m "modelkeeper 1.0.0"
git push origin v1.0.0
```

The Release workflow tests, builds and publishes `modelkeeper-v1.0.0-windows-amd64.zip` and the
Linux package under GitHub → Releases. Do not create the release by hand in the GitHub UI (the
workflow creates it and would fail if it exists).

After the release: download the published zip once and start it (part 2 in short). From then on the
update check in the app finds newer versions.
