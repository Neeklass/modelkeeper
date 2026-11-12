package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/msg"
)

// E9 with real scans: a root is deactivated and later added again (with a
// different spelling of the path). Everything is linked up again: same root,
// same model IDs, tags, notes and corrections, files present.
func TestDeactivateAndReactivateRoot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("Atlas/Orc/orc.stl", stlA)
	e.write("Atlas/Orc/orc_base.stl", stlB)
	e.write("Atlas/Elf/elf.stl", []byte("solid elf"))
	e.scan()

	orcID := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Orc/orc.stl'`))
	elfID := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Elf/elf.stl'`))
	orcUID := e.str(`SELECT uid FROM model WHERE id = ?`, orcID)
	if err := catalog.AddTags(ctx, e.db, []int64{orcID}, []string{"painted"}); err != nil {
		t.Fatal(err)
	}
	notes := "printed twice"
	if err := catalog.UpdateModel(ctx, e.db, orcID, catalog.ModelUpdate{Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO override (kind, root_id, path, model_id, created_at)
		VALUES ('is_model', ?, 'Atlas/Orc', ?, 0)`, e.id, orcID); err != nil {
		t.Fatal(err)
	}
	locations := e.int(`SELECT count(*) FROM location`)

	if err := catalog.DeactivateRoot(ctx, e.db, e.id); err != nil {
		t.Fatal(err)
	}
	// Nothing is lost while deactivated.
	expect(t, "locations kept", e.int(`SELECT count(*) FROM location`), locations)
	expect(t, "anchors kept", e.int(`SELECT count(*) FROM model_anchor WHERE root_id = ?`, e.id), 2)
	expect(t, "override kept", e.int(`SELECT count(*) FROM override WHERE root_id = ?`, e.id), 1)
	if roots, _ := catalog.ListRoots(ctx, e.db); len(roots) != 0 {
		t.Errorf("deactivated root listed: %+v", roots)
	}
	// Not scanned.
	if err := e.mgr.Start(e.id); msg.From(err).Key != "error.rootRemoved" {
		t.Errorf("Start on a deactivated root = %v, want error.rootRemoved", err)
	}
	if err := e.mgr.Redetect(e.id); msg.From(err).Key != "error.rootRemoved" {
		t.Errorf("Redetect on a deactivated root = %v", err)
	}
	// Still visible, marked inactive, but not "missing" or "single location".
	page, err := catalog.SearchModels(ctx, e.db, catalog.ModelQuery{Text: "orc"}, 0, 10)
	if err != nil || page.Total != 1 || !page.Items[0].Inactive || page.Items[0].Roots != 0 {
		t.Fatalf("search while deactivated: %+v, %v", page, err)
	}
	for name, q := range map[string]catalog.ModelQuery{
		"inactive": {Inactive: true}, "single location": {SingleLocation: true}, "missing": {Missing: true},
	} {
		page, err := catalog.SearchModels(ctx, e.db, q, 0, 10)
		want := map[string]int{"inactive": 2}[name]
		if err != nil || page.Total != want {
			t.Errorf("filter %s while deactivated: %d models, want %d (%v)", name, page.Total, want, err)
		}
	}
	d, err := catalog.GetModel(ctx, e.db, orcID)
	if err != nil || !d.Inactive || len(d.Anchors) != 1 || !d.Anchors[0].RootRemoved {
		t.Fatalf("detail while deactivated: %+v, %v", d, err)
	}

	// Changes meanwhile: one file more.
	e.write("Atlas/Orc/orc_arm.stl", []byte("solid arm"))

	// Add again with another spelling: trailing separator, other case on Windows.
	again := e.root + string(filepath.Separator)
	if os.PathSeparator == '\\' {
		again = strings.ToUpper(again)
	}
	r, err := catalog.AddRoot(ctx, e.db, again, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != e.id || !r.Reactivated || r.RemovedAt != nil {
		t.Fatalf("AddRoot = %+v, want reactivated root %d", r, e.id)
	}
	expect(t, "roots", e.int(`SELECT count(*) FROM root`), 1)
	p := e.scan()
	expect(t, "scan", p.Status, StatusDone)
	expect(t, "new files", p.FilesNew, 1)

	d, err = catalog.GetModel(ctx, e.db, orcID)
	if err != nil {
		t.Fatalf("model gone after reactivation: %v", err)
	}
	expect(t, "uid", d.UID, orcUID)
	expect(t, "tags", strings.Join(d.Tags, ","), "painted")
	expect(t, "notes", d.Notes, notes)
	expect(t, "inactive", d.Inactive, false)
	expect(t, "override", e.int(`SELECT count(*) FROM override WHERE model_id = ? AND root_id = ?`, orcID, e.id), 1)
	expect(t, "orc files", len(d.Files), 3)
	for _, f := range d.Files {
		if !f.Present || f.RootRemoved {
			t.Errorf("file %s: present %v, rootRemoved %v", f.Path, f.Present, f.RootRemoved)
		}
	}
	expect(t, "elf keeps its ID", int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Elf/elf.stl'`)), elfID)
	expect(t, "models", e.int(`SELECT count(*) FROM model`), 2)
}

// Overlap with a deactivated root: a folder inside it (or around it) is not
// added silently next to the kept entries.
func TestAddRootOverlappingDeactivated(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("Atlas/Orc/orc.stl", stlA)
	e.scan()
	if err := catalog.DeactivateRoot(ctx, e.db, e.id); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(e.root, "Atlas"), filepath.Dir(e.root)} {
		if _, err := catalog.AddRoot(ctx, e.db, p, ""); msg.From(err).Key != "error.rootOverlapRemoved" {
			t.Errorf("AddRoot(%s) = %v, want error.rootOverlapRemoved", p, err)
		}
	}
	// The same path of an active root is still an overlap, not a reactivation.
	other := filepath.Join(t.TempDir(), "other")
	os.MkdirAll(other, 0o755)
	if _, err := catalog.AddRoot(ctx, e.db, other, ""); err != nil {
		t.Fatal(err)
	}
	if r, err := catalog.AddRoot(ctx, e.db, other, ""); msg.From(err).Key != "error.rootOverlap" || r.Reactivated {
		t.Errorf("adding an active root again = %+v, %v", r, err)
	}
}

// After a root was deleted permanently, models with user data have no
// location left. When their files show up again (the same folder added
// anew, or a copy elsewhere), they are linked up: same ID, tags and notes.
func TestPurgedModelLinkedUpAgain(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("Atlas/Orc/orc.stl", stlA)
	e.write("Atlas/Orc/orc_base.stl", stlB)
	e.write("Atlas/Elf/elf.stl", []byte("solid elf")) // no user data: removed for good
	e.scan()
	orcID := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Orc/orc.stl'`))
	orcUID := e.str(`SELECT uid FROM model WHERE id = ?`, orcID)
	catalog.AddTags(ctx, e.db, []int64{orcID}, []string{"painted"})

	if err := catalog.DeactivateRoot(ctx, e.db, e.id); err != nil {
		t.Fatal(err)
	}
	if err := catalog.PurgeRoot(ctx, e.db, e.id, 1, nil); err != nil {
		t.Fatal(err)
	}
	expect(t, "fingerprint rows", e.int(`SELECT count(*) FROM model_fingerprint WHERE model_id = ?`, orcID), 2)
	expect(t, "models after purge", e.int(`SELECT count(*) FROM model`), 1)

	// The same files, now in another folder.
	other := filepath.Join(t.TempDir(), "nas")
	for rel, data := range map[string][]byte{"Sorted/Orc/orc.stl": stlA, "Sorted/Orc/orc_base.stl": stlB} {
		p := filepath.Join(other, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o644)
	}
	r, err := catalog.AddRoot(ctx, e.db, other, "")
	if err != nil {
		t.Fatal(err)
	}
	e.id = r.ID
	e.scan()

	d, err := catalog.GetModel(ctx, e.db, orcID)
	if err != nil {
		t.Fatalf("model not linked up: %v", err)
	}
	expect(t, "uid", d.UID, orcUID)
	expect(t, "tags", strings.Join(d.Tags, ","), "painted")
	expect(t, "files", len(d.Files), 2)
	expect(t, "anchor", len(d.Anchors), 1)
	expect(t, "one Orc", e.int(`SELECT count(*) FROM model WHERE name = 'Orc'`), 1)
	expect(t, "fingerprint consumed", e.int(`SELECT count(*) FROM model_fingerprint`), 0)
	page, _ := catalog.SearchModels(ctx, e.db, catalog.ModelQuery{Text: "sorted"}, 0, 10)
	if page.Total != 1 || page.Items[0].ID != orcID {
		t.Errorf("search index not updated: %+v", page)
	}
}
