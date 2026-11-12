# modelkeeper – Concept

Desktop app for cataloging, tagging and searching 3D printing models (resin) that are spread
across the local PC, a Fritz!NAS and (as purchase origin) MyMiniFactory.

Date: 2026-11-08 · Status: agreed, ready for implementation

---

## A – Scope

- **A1** Desktop app for a single user on a single PC.
- **A2** Windows first; Linux must be possible later without a rewrite.
- **A3** No server, no cloud, no sync.
- **A4** A public website is **out of scope** for this project.
- **A5** The data model is clean enough for a future website to build on
  (stable IDs, separation of internal and descriptive fields).

## B – Technology

- **B1** Backend in **Go**.
- **B2** UI built with **Wails** (web UI in a native window; Windows: WebView2, preinstalled on Windows 11;
  Linux: WebKitGTK).
- **B3** Shipped as **a single portable `.exe`**, no installer.
- **B4** Data stored in a **SQLite** file, **portable next to the `.exe`** (see section M).
- **B5** SQLite via a pure Go library without CGO (`modernc.org/sqlite`) → easy cross-compiling.
- **B6** Search via **SQLite FTS5**, no external search engine.
- **B7** 3D preview in the frontend using **three.js**.

## C – Scale & Performance

- **C1** Smooth from 10,000 files, scaling to 100,000+.
- **C2** Search results update while typing and feel instant.
- **C3** Grid and list views use virtual scrolling.

## D – Data Model

- **D1** A **model** is the logical unit (e.g. a single miniature).
- **D2** A model consists of **one or more files**. Recognized types (extensible in settings):
  - 3D models: `.stl`, `.obj` (+`.mtl`), `.3mf`, `.ply`, `.amf`, `.step`/`.stp`, `.fbx`, `.blend`, `.ztl`
  - Resin slicers (project & sliced): `.chitubox`, `.ctb`, `.cbddlp`, `.lys`, `.lyt`, `.pwmx`, `.pwma`, `.pws`,
    `.goo`, `.sl1`, `.photon`
  - Archives: `.zip`, `.rar`, `.7z`
  - Supplementary: `.jpg`, `.jpeg`, `.png`, `.webp`, `.pdf`, `.txt`, `.md`
- **D3** A file can exist at multiple **locations** (local, NAS).
- **D4** Identical files are detected by **hash**; duplicates are visible.
- **D5** Models that exist at **only one location** (no backup) are visible.
- **D6** Every model has a **stable ID**, independent of file paths.
- **D7** Fields per model: name, creator/designer, tags, notes, origin, license, thumbnail,
  variants, print status (L1), supports (L2).
- **D8** Archives stay **in their original form** and are never stored extracted.
  The app reads the archive index directly; individual files (e.g. for thumbnails) are only held in memory briefly.

### Entities (sketch)

```
Model ──< ModelFile >── File ──< Location
  │                       └── (in_archive: archive file + path inside archive)
  ├──< ModelTag >── Tag
  ├──< Variant            (e.g. S, M)
  └── Thumbnail           (source: manual | image | embedded | rendered)

Root      (root folder: path, type local/NAS, creator level on/off)
Override  (manual corrections of model detection, survive rescans)
```

## E – Sources

- **E1** Local folders are scanned recursively.
- **E2** Fritz!NAS via SMB path (`\\fritz.nas\…` or mapped drive), treated like a folder.
- **E3** NAS unreachable → entries remain, status "offline".
- **E4** Local folders: live watching (`fsnotify`). NAS: manual or scheduled rescan.
- **E5** MyMiniFactory is an **origin** ("purchased at"), not a storage location.
- **E6** MyMiniFactory purchases are entered manually (link, designer, license). *(post-MVP)*
- **E7** Automatic MyMiniFactory import later, depending on API capabilities. *(post-MVP)*
- **E8** Show purchased models that have not been downloaded yet. *(post-MVP)*
- **E9** Removing a root folder **deactivates** it: it is no longer scanned or watched and not listed with
  the root folders, but locations, detection anchors, corrections and hashes stay. Models whose files are only
  on deactivated roots stay visible and are marked. Adding the same path again (compared case-insensitively on
  Windows, trailing separators ignored) reactivates the root with the same ID, followed by an incremental scan:
  everything is linked up again. Deactivated roots count for search and descriptive filters, not for the
  checks (single location, duplicates, extracted + archive, missing, offline, broken archives).
  **Deleting permanently** from the catalog is a separate action for deactivated roots only: it shows how many
  models with user data would be left without any location (the number must be typed if > 0) and takes a
  snapshot right before (J2); if the snapshot fails, nothing is deleted. The content (SHA-256) of the models that
  lose their last location is kept; when a later scan finds it again anywhere, they are linked up again (F6, D6).

## F – Scanning & Model Detection

- **F1** Scanning runs in the background with a progress indicator; the app stays usable.
- **F2** Rescans only process new/changed files (criteria: path + size + modification time).
- **F3** Only new/changed files are hashed.
- **F4** Model detection: **the app suggests, the user decides.**
  - **F4a** First level below a root = suggested creator (can be disabled per root).
  - **F4b** An archive is a single model by default.
  - **F4c** Bottom-up from the files: folders with typical part names (`Supported`, `Unsupported`,
    `Presupported`, `STL`, `Parts`, `Base`, `32mm`, `75mm` …) or single subfolders are merged into
    the model above.
  - **F4d** The user can mark any folder as "this is a model", merge models or split them.
  - **F4e** Manual corrections survive rescans.
  - **F4f** Subfolders with a size prefix (`S_`, `M_`, `L_`, `XL_` …) are **size variants** of the same model.
  - **F4g** Intermediate levels between creator and model are **suggested as tags**.
- **F5** Tag suggestions from folder/file names, never applied automatically.
- **F6** Moved files are recognized by hash; tags & notes are kept.
- **F7** Archives are checked for readability during scanning (CRC); broken archives are flagged.
- **F8** Changed hashes at an unchanged path are reported (possible corruption).
- **F9** "Full check" button: re-hashes all files regardless of size/modification time.
- **F10** The lists of part names (F4c) and size prefixes/suffixes (F4f, e.g. `S_`, `32mm`)
  are extensible in settings.
- **F11** The initial scan also hashes files inside archives (read fully, in memory only).
  Over the NAS, the initial scan of 10,000+ files can take hours; afterwards it is incremental.

### Reference example

```
\\fritz.nas\FRITZ.NAS\<disk>\<user>\3D Models\     ← root
  <Creator>\                                        ← creator
    <Collection>\                                   ← intermediate level → tag suggestion
      <Model>\                                      ← MODEL
        S_<PART>\                                   ← variant S
        M_<PART>\                                   ← variant M
          M_<PART>_BASE.stl
```

## G – Tags

- **G1** Free-form, **flat** tags.
- **G2** Bulk editing (tag multiple models at once).
- **G4** "Untagged" view for working through the backlog.

## H – Search & Views

- **H1** Full-text search over name, tags, notes, creator, path.
- **H2** Filters: tag, location, creator, file type, online/offline, duplicates, single-location only,
  print status, supports, broken archives, "exists both extracted **and** as archive" (space-saving candidates),
  only in deactivated root folders (E9).
- **H3** Grid view (thumbnails) and list view.
- **H4** Detail view: rotatable 3D preview, variants, all files, all locations.
- **H5** "Show in Explorer" action. No slicer integration.

## I – Thumbnails

- **I1** The app generates thumbnails itself. Source priority:
  1. set manually by the user
  2. image in the model folder/archive
  3. embedded preview (`.3mf`, `.ctb`, `.goo` …)
  4. rendered from the STL (three.js)
- **I3** Generated once, then cached.
- **I4** The user can change a thumbnail: pick an image, choose from candidates,
  or use the current view of the 3D preview as the thumbnail.

## J – Data Safety

- **J1** The app is **read-only** towards model files: it never moves, modifies or deletes files, including duplicates.
- **J2** Automatic local snapshots in `data\backups\` (see M): daily at start (only if something changed
  since the newest snapshot), always before a catalog migration, before risky catalog changes (permanently
  deleting a root folder, removing file types or moving them to another category) and on demand ("Create
  backup now"). Every snapshot is checked with `quick_check` after writing. A damaged catalog (integrity
  check at start) stops the app before anything is written, with the path to the newest snapshot. A snapshot
  can be restored from the app (settings or the startup error screen): it is applied at the next start,
  before anything is opened, and the replaced state is kept as a `pre-restore` snapshot. An external backup
  (another drive, Dropbox …) stays manual.

## L – Additional Fields

- **L1** Print status: not printed / printed successfully / failed.
- **L2** Supports: presupported / unsupported / both — detected automatically from names
  (`supported`, `presupported`, `unsupported`), editable.
- All status fields are filterable.

## M – Data Storage (portable)

Everything lives next to the `.exe`:

```
modelkeeper\
  modelkeeper.exe
  data\
    catalog.db          ← database
    catalog.db-wal      ← only while the app is running
    catalog.db-shm      ← only while the app is running
    thumbnails\         ← cache + manually set thumbnails
    settings.json       ← settings (root folders etc.)
    backups\            ← automatic snapshots (J2)
      2026-11-12_083000_daily\
        catalog.db      ← consistent copy (VACUUM INTO)
        settings.json
        thumbnails\     ← only the manually set thumbnails (generated ones are reproducible)
        manifest.json   ← app/schema version, time, reason, number of models/tags
```

- Snapshots are written to a temporary folder and renamed when complete. Retention: the 14 newest daily
  snapshots and all event snapshots (`pre-migration-NNNN`, `pre-remove-root`, `pre-filetypes`, `pre-restore`, `manual`) of
  the last 90 days; the 3 newest are never deleted. Rotation only deletes folders it created (name scheme
  and manifest).
- Restore: from the app (applied at the next start; the replaced state becomes a `pre-restore` snapshot), or
  by hand: close the app, copy `catalog.db`, `settings.json` and `thumbnails\` from a snapshot into
  `data\`, delete `catalog.db-wal`/`-shm` if present, start the app.
- The data folder is never catalogued, even if it lies inside a root; a root that contains it or lies inside
  it is rejected.
- External backup: **copy the whole `modelkeeper\` folder while the app is closed.**
  On exit, the app checkpoints everything into `catalog.db`.
- `thumbnails\` must be included in backups (contains manually set thumbnails).
- The folder must be writable (i.e. **not** under `C:\Program Files`).
- Recommendation: keep the folder on the **local PC**, not on the NAS (SQLite over network shares is unreliable).
- Settings include an "Open data folder" action.

## N – Localization

- **N1** The app UI is **multi-language**; German and English are included from the MVP onwards.
- **N2** Default language follows the system locale; it can be switched in settings.
- **N3** All UI strings live in translation files (one JSON file per language); no hard-coded UI text.
- **N4** The backend returns message keys + parameters instead of finished sentences;
  the frontend translates them.
- **N5** Adding a language = adding one translation file, no code changes.
- **N6** Code, comments, documentation and the data model are in English.

## K – MVP

**Included:**
- Manage root folders (local + NAS), background scan, hashing, reading & checking archives
- Model detection incl. variants, corrections (mark/merge/split)
- Tags incl. suggestions & bulk editing
- Search + filters, grid/list, detail view with 3D preview
- Thumbnails (automatic + editable)
- Print status, supports
- "Show in Explorer"
- UI in German and English

**Not included (later):**
- MyMiniFactory (manual entry, import, not-yet-downloaded purchases)
- Export for a website

## Prerequisites

- Windows 10/11 with WebView2 (preinstalled on Windows 11).
- NAS: SMB enabled on the Fritz!Box, Windows has access to `\\fritz.nas\…` (credentials saved).
- Linux (later): WebKitGTK system package required.

## Open Points

- MyMiniFactory API: unclear whether the user's own purchase library can be retrieved
  (see https://www.myminifactory.com/pages/api-guidelines). Only relevant after the MVP.
