# TODO

Working list for the next development rounds. Priorities, in this order (see CONCEPT.md):
model files are never changed or damaged; everything the user does in the catalog is
recoverable; browsing and cataloging are pleasant; real value over a file manager.

Each item names the concept IDs it touches. Tick items off in the PR that finishes them.
Status: `[ ]` open · `[~]` in progress · `[x]` done.

---

## 0. Before anything else

- [ ] **0.1 Look at PR #3 in the running app** (new library UI, pause, slicer copy, external
  backups). It was only built by CI (tsc + vite), never looked at. Fix what is off, then merge.
- [x] **0.2 Build artifact in CI**: every PR attaches a ready `modelkeeper.exe`, so every state
  can be tried without a local build.
- [ ] **0.3 Real folder structures as test data**: names only (no file contents) of the top
  3–4 levels of the NAS and the local folders, anonymized where wanted. They become the test
  corpus for section 1.

## 1. Detection: no silent assumptions (F4, F4c–F4g, L2)

Problem: the detection takes guesses as facts. "The first folder level is the creator",
"Parts/Supported/32mm belong to the model above", size prefixes, supports from names – all of
these are right for some collections and wrong for others, and wrong guesses look exactly like
facts. New principle: **the app shows what it guessed and why, and nothing guessed counts as
known until the user confirms it** (once per folder pattern, not once per model).

- [ ] **1.1 Every detected value carries its source and confidence.** Creator, model boundary,
  variant, supports: stored as `detected` (with the rule that produced it) or `confirmed` /
  `manual`. Detected values are shown as suggestions (dashed, "suggested"), never like user
  data. Filter "unconfirmed".
- [ ] **1.2 Folder template per root folder instead of fixed rules.** A setup step shows real
  sample paths of the folder; the user says what each level means (creator / release or bundle /
  model / ignore / "varies"). A live preview of ~20 random samples shows the resulting models
  before anything is applied. No creator is assumed until a template says so; "first level =
  creator" becomes one option among others, not the default.
- [ ] **1.3 Part folders and size variants are opt-in suggestions.** The lists stay, but before
  a rule is used the app shows where it would apply ("`Supported` found in 1,240 folders – treat
  these as parts of the model above?"). Rules apply per root and can be switched off per root.
- [ ] **1.4 Supports only from explicit evidence.** Only clear tokens (`presupported`,
  `supported`, `unsupported`, `pre-supported`, `_sup`) count; otherwise "unknown", never a
  default. Show the token that decided it.
- [ ] **1.5 Review queue "Check detection".** Uncertain cases are collected and grouped: folder
  with many loose model files and no subfolders (one model or many?), archive containing several
  model folders, very deep nesting, conflicting support tokens, creator differs from the template.
  Each group: accept all, correct all, or decide one by one – with preview and undo.
- [ ] **1.6 Corrections as rules, not only per folder.** "Treat all folders like this one" (same
  name pattern / same level / same parent) with a count of what it changes before applying.
- [ ] **1.7 "Why?" in the detail view.** Shows which rule made this a model, where the creator,
  variant and supports came from, and offers the matching correction right there.
- [ ] **1.8 Corrections are undoable and listed** (a page of all corrections and rules per root,
  each removable; depends on 2.1).
- [ ] **1.9 Regression tests from the real structures (0.3)**: expected models per sample path,
  so detection changes cannot silently make things worse.
- [ ] **1.10 Models during the first scan.** A provisional detection right after listing (no
  hashes yet), so the library is not empty for a day; after hashing the normal detection
  reconciles provisional models by content, so "moved from PC to NAS" still re-links tags.

## 2. Recoverability of catalog work (J2, G1, G2, F4d)

- [~] **2.1 Undo for every catalog change.** *Done: journal for tags, fields, favorites,
  suggestions; toast with Undo, Ctrl+Z, history list (J8). Done too: corrections
  (merge, split, mark, detach, reset), with detection in between. Open: thumbnail, root settings;
  replacing confirm dialogs.* A journal stores the inverse of each action (tags,
  fields, favorite, merge/split, corrections, thumbnail, root settings). Toast "… · Undo" after
  each action, Ctrl+Z, and a **History** page with the last ~500 actions, each undoable. Then
  replace confirm dialogs by undo wherever the action is reversible.
- [x] **2.2 Tag management with undo** (Settings → Tags; rename = merge when the name exists): page **Tags** with counts; rename, merge, delete for
  all models at once, with preview ("affects 412 models").
- [x] **2.3 Readable export of user data with every snapshot** (J6, J7): `catalog-export.json` (models,
  paths, hashes, tags, notes, favorites, corrections). Plus an import that applies it to a new
  catalog by hash – the plan for "everything is gone".
- [ ] **2.4 Restore single models or tags from a snapshot**: "Earlier states" in the detail view
  (snapshots opened read-only), take over only that model's data; same for "which models had tag
  X on date Y".
- [ ] **2.5 Old program next to the pre-migration snapshot**: on a schema update, copy the old
  exe into the snapshot, so going back to the old version is one double-click.

## 3. Robust operation on the NAS (E2, E3, F1, J3, J4)

- [ ] **3.1 Auto-resume after a NAS outage**: offline roots with unfinished work are checked
  quietly every few minutes and continue when the NAS is back (unless paused).
- [ ] **3.2 Backup warnings that are seen**: warning when the second backup folder got no new
  copy for 7 days (USB drive not plugged in); a notice at start when the last restore test failed.
- [ ] **3.3 Activity view**: click on the status at the bottom left → everything running in the
  background (scans per folder, thumbnails, snapshot, external copy, restore test) with state,
  last run and errors; a simple log viewer.
- [x] **3.5 Change a root folder's path** after a move to a new NAS or drive letter (E10), keeping its
  ID, tags and corrections; with a sample check before.
- [x] **3.7 Import and path change during a scan**: the scan pauses for it and continues
  (`Manager.Interrupting`); corrections during a scan are stored and applied by the scan's detection.
- [x] **3.8 "Waiting for files" view**: imported models not linked yet are visible as such and can be
  removed (with a snapshot) when their files are gone for good.
- [x] **3.9 Linux in CI**: tests and a Linux build on Ubuntu next to Windows (A2).
- [x] **3.10 Moving the installation**: detected at start, root folders that moved along are offered
  for a path update (M).
- [x] **3.12 Release packages**: tag `v*` → Windows zip and Linux tar.gz with the right folder layout.
- [x] **3.11 Configuration as JSON**: export/import of settings and folder list (M).
- [x] **3.6 Data folder in a bad place**: refused in a temporary folder (exe started from a zip), warning
  in Downloads and synced folders (J5).
- [ ] **3.4 Large-catalog test**: generator for ~200,000 models; grid, search and sidebar must
  stay smooth (C1, C2).

## 4. Usability (H1–H5, G4, I4)

- [ ] **4.1 First-start assistant**: add the NAS folder (UNC path, read-only user), folder
  template (1.2), second backup folder, start the scan with a realistic time estimate.
- [ ] **4.2 Faster cataloging**: recently used tags as one-click chips; "tags of the previous
  model"; progress "412 of 3,180 cataloged".
- [ ] **4.3 Space bar quick look** in the grid, Shift+arrows for multi-select, shortcut overview
  on `?`.
- [ ] **4.4 Every error offers the next step** ("NAS unreachable · Try again", "No program for
  .stl · Choose slicer").
- [ ] **4.5 Thumbnails**: view/filter "without thumbnail", "generate again" for failed ones.
- [x] **4.6 Origin, purchase link, license in the UI** (also the creator is editable) (fields exist, E6).

## Not planned

- Print history, materials, slicer profiles.
- Distinguishing printed / not printed for favorites.
- Nested archives (zip in zip): edge case, would cost too much memory.
- Hashing on the NAS (helper on the NAS): hashing stays in the app.
- Pre-scanning a folder for someone else: every user scans their own folders.
