package scanner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/corrections"
	"modelkeeper/internal/db"
	"modelkeeper/internal/settings"
	"modelkeeper/internal/userexport"
)

// freshCatalog opens an empty catalog with a scan manager (no root folder).
func freshCatalog(t *testing.T) (*sql.DB, *Manager) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	mgr, err := NewManager(conn, settings.Defaults, nil, Options{EmitInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return conn, mgr
}

func scanRoot(t *testing.T, mgr *Manager, id int64) {
	t.Helper()
	if err := mgr.Start(id); err != nil {
		t.Fatal(err)
	}
	mgr.Wait()
	if p := mgr.Progress(); p == nil || p.Status != StatusDone {
		t.Fatalf("scan: %+v", p)
	}
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// canonical returns the export as JSON without what differs by nature
// between two catalogs (times, settings).
func canonical(t *testing.T, e *userexport.Export) string {
	t.Helper()
	c := *e
	c.ExportedAt, c.Settings, c.AppVersion = "", nil, ""
	c.Models = append([]userexport.Model(nil), e.Models...)
	for i := range c.Models {
		c.Models[i].CreatedAt, c.Models[i].UpdatedAt = 0, 0
	}
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func choicesOf(p userexport.Plan) []userexport.RootChoice {
	var out []userexport.RootChoice
	for _, r := range p.Roots {
		out = append(out, r.Suggested)
	}
	return out
}

// userData sets up a catalog with every kind of user data.
func userData(t *testing.T, e *env, thumbDir string) {
	t.Helper()
	ctx := context.Background()
	e.write("Atlas/Orc/orc.stl", stlA)
	e.write("Atlas/Orc/orc_base.stl", stlB)
	e.write("Atlas/Elf/elf.stl", []byte("solid elf"))
	e.write("Atlas/Bundle/Goblin/g.stl", []byte("solid goblin"))
	e.write("Atlas/Bundle/Troll/t.stl", []byte("solid troll"))
	e.write("Atlas/Plain/plain.stl", []byte("solid plain"))
	e.scan()

	orc := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Orc/orc.stl'`))
	elf := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Elf/elf.stl'`))
	if err := catalog.AddTags(ctx, e.db, []int64{orc}, []string{"painted", "green skin"}); err != nil {
		t.Fatal(err)
	}
	name, notes, printed, sup := "Big Orc", "print at 0.05 mm", "printed", "presupported"
	if err := catalog.UpdateModel(ctx, e.db, orc, catalog.ModelUpdate{Name: &name, Notes: &notes, PrintStatus: &printed, Supports: &sup}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SetFavorite(ctx, e.db, []int64{elf}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.AddPrint(ctx, e.db, catalog.Print{ModelID: orc, PrintedAt: 1_700_000_000_000, Result: "ok",
		Printer: "Saturn 4", Settings: "2.4 s", Notes: "perfect"}); err != nil {
		t.Fatal(err)
	}
	col, err := catalog.CreateCollection(ctx, e.db, "Campaign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.AddToCollection(ctx, e.db, col.ID, []int64{elf, orc}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SaveSearch(ctx, e.db, "Orcs", `{"text":"orc"}`); err != nil {
		t.Fatal(err)
	}
	// A correction: the bundle is one model.
	if _, err := corrections.MarkModel(ctx, e.db, e.id, "Atlas/Bundle"); err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.Redetect(e.id); err != nil {
		t.Fatal(err)
	}
	e.mgr.Wait()
	bundle := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Bundle/Goblin/g.stl'`))
	if bundle != int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Bundle/Troll/t.stl'`)) {
		t.Fatal("correction not applied")
	}
	if err := catalog.AddTags(ctx, e.db, []int64{bundle}, []string{"bundle"}); err != nil {
		t.Fatal(err)
	}
	// A manual thumbnail.
	uid := e.str(`SELECT uid FROM model WHERE id = ?`, orc)
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thumbDir, uid+".jpg"), testJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	e.db.Exec(`UPDATE model SET thumb_source = 'manual', thumb_file = ? WHERE id = ?`, uid+".jpg", orc)
}

// Everything is gone except the export: a fresh catalog, the same folders,
// import, scan – and the catalog holds exactly the same user data again.
func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	thumbs1 := filepath.Join(t.TempDir(), "thumbnails")
	userData(t, e, thumbs1)

	exp, err := userexport.Build(ctx, e.db, userexport.Options{ThumbDir: thumbs1, EmbedThumbnails: true})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "exported models", len(exp.Models), 3) // orc, elf, bundle; plain has no user data
	expect(t, "exported overrides", len(exp.Overrides), 1)
	var buf bytes.Buffer
	if err := exp.Write(&buf); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "export.json")
	os.WriteFile(file, buf.Bytes(), 0o644)
	read, err := userexport.Read(file)
	if err != nil {
		t.Fatal(err)
	}

	conn, mgr := freshCatalog(t)
	plan, err := userexport.Preview(ctx, conn, read, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Roots) != 1 || plan.Roots[0].Suggested.Action != userexport.ActionAdd || plan.Match.New != 3 {
		t.Fatalf("plan %+v", plan)
	}
	thumbs2 := filepath.Join(t.TempDir(), "thumbnails")
	res, err := userexport.Apply(ctx, conn, read, choicesOf(plan), userexport.ApplyOptions{ThumbDir: thumbs2})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "roots added", len(res.RootsAdded), 1)
	expect(t, "thumbnails", res.Thumbnails, 1)
	expect(t, "overrides", res.Overrides, 1)

	// Before any scan the data is there and searchable.
	page, err := catalog.SearchModels(ctx, conn, catalog.ModelQuery{Text: "painted"}, 0, 10)
	if err != nil || page.Total != 1 {
		t.Fatalf("search before scan: %+v, %v", page, err)
	}

	scanRoot(t, mgr, res.RootsAdded[0])
	var located int
	conn.QueryRow(`SELECT count(DISTINCT model_id) FROM location WHERE model_id IN (SELECT id FROM model WHERE uid IN (?, ?, ?))`,
		exp.Models[0].UID, exp.Models[1].UID, exp.Models[2].UID).Scan(&located)
	expect(t, "imported models linked to their files", located, 3)

	again, err := userexport.Build(ctx, conn, userexport.Options{ThumbDir: thumbs2, EmbedThumbnails: true})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := canonical(t, exp), canonical(t, again); a != b {
		t.Errorf("export after the round trip differs\nbefore:\n%s\nafter:\n%s", a, b)
	}

	// Importing the same file again changes nothing.
	res, err = userexport.Apply(ctx, conn, read, []userexport.RootChoice{{Key: read.Roots[0].Key,
		Action: userexport.ActionExisting, RootID: res.RootsAdded[0]}}, userexport.ApplyOptions{ThumbDir: thumbs2})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "second import: matched by id", res.Match.ByID, 3)
	expect(t, "second import: tags added", res.Tags, 0)
	expect(t, "second import: notes kept twice", res.Match.NotesKept, 0)
}

// The files moved to another place and a model folder was renamed: the
// import maps the root folder, the renamed model is found by its content.
func TestImportAfterMoveAndRename(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	userData(t, e, filepath.Join(t.TempDir(), "thumbnails"))
	orcUID := e.str(`SELECT m.uid FROM model m JOIN location l ON l.model_id = m.id WHERE l.path = 'Atlas/Orc/orc.stl'`)
	exp, err := userexport.Build(ctx, e.db, userexport.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// The new NAS: same files, other place, one folder renamed.
	nas := filepath.Join(t.TempDir(), "nas")
	copyTree(t, e.root, nas)
	if err := os.Rename(filepath.Join(nas, "Atlas", "Orc"), filepath.Join(nas, "Atlas", "Orc (v2)")); err != nil {
		t.Fatal(err)
	}

	conn, mgr := freshCatalog(t)
	choice := []userexport.RootChoice{{Key: exp.Roots[0].Key, Action: userexport.ActionAdd, Path: nas}}
	res, err := userexport.Apply(ctx, conn, exp, choice, userexport.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	scanRoot(t, mgr, res.RootsAdded[0])

	var id int64
	if err := conn.QueryRow(`SELECT id FROM model WHERE uid = ?`, orcUID).Scan(&id); err != nil {
		t.Fatalf("orc: %v", err)
	}
	d, err := catalog.GetModel(ctx, conn, id)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "name", d.Name, "Big Orc")
	expect(t, "tags", strings.Join(d.Tags, ","), "green skin,painted")
	if len(d.Anchors) != 1 || d.Anchors[0].Path != "Atlas/Orc (v2)" {
		t.Errorf("anchors %+v", d.Anchors)
	}
	expect(t, "models named Orc*", int(countRows(t, conn, `SELECT count(*) FROM model WHERE name LIKE 'Orc%' OR name LIKE 'Big Orc%'`)), 1)
	// The bundle correction applies at the new place too.
	expect(t, "bundle is one model", countRows(t, conn,
		`SELECT count(DISTINCT model_id) FROM location WHERE path LIKE 'Atlas/Bundle/%'`), 1)
}

func countRows(t *testing.T, conn *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// A mistake in the same catalog (tag removed, notes overwritten) is repaired
// by importing an earlier export; nothing the user entered since is lost.
func TestImportRepairsMistakes(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	userData(t, e, filepath.Join(t.TempDir(), "thumbnails"))
	exp, err := userexport.Build(ctx, e.db, userexport.Options{})
	if err != nil {
		t.Fatal(err)
	}
	orc := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Orc/orc.stl'`))
	if err := catalog.RemoveTag(ctx, e.db, []int64{orc}, "painted"); err != nil {
		t.Fatal(err)
	}
	notes := "new notes"
	catalog.UpdateModel(ctx, e.db, orc, catalog.ModelUpdate{Notes: &notes})
	catalog.AddTags(ctx, e.db, []int64{orc}, []string{"added later"})

	plan, err := userexport.Preview(ctx, e.db, exp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Roots[0].Suggested.Action != userexport.ActionExisting || plan.Match.ByID != 3 || plan.Match.NotesKept != 1 {
		t.Fatalf("plan %+v", plan)
	}
	res, err := userexport.Apply(ctx, e.db, exp, choicesOf(plan), userexport.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "tags added", res.Tags, 1)
	d, _ := catalog.GetModel(ctx, e.db, orc)
	expect(t, "tags", strings.Join(d.Tags, ","), "added later,green skin,painted")
	expect(t, "notes", d.Notes, "new notes\n\nprint at 0.05 mm")
}

// Broken or foreign files are rejected before anything is applied.
func TestImportRejectsBadFiles(t *testing.T) {
	for name, content := range map[string]string{
		"not json":     "hello",
		"other json":   `{"a": 1}`,
		"too new":      `{"format": "modelkeeper-export", "formatVersion": 99}`,
		"bad root":     `{"format": "modelkeeper-export", "formatVersion": 1, "roots": [], "models": [{"uid": "u", "name": "x", "anchors": [{"root": "r9", "path": "a", "kind": "folder"}]}]}`,
		"bad hash":     `{"format": "modelkeeper-export", "formatVersion": 1, "models": [{"uid": "u", "name": "x", "content": ["zz"]}]}`,
		"double uid":   `{"format": "modelkeeper-export", "formatVersion": 1, "models": [{"uid": "u", "name": "x"}, {"uid": "u", "name": "y"}]}`,
		"bad status":   `{"format": "modelkeeper-export", "formatVersion": 1, "models": [{"uid": "u", "name": "x", "printStatus": "maybe"}]}`,
		"bad override": `{"format": "modelkeeper-export", "formatVersion": 1, "roots": [{"key": "r1", "path": "x"}], "overrides": [{"kind": "x", "root": "r1", "path": "a"}]}`,
	} {
		if _, err := userexport.Decode(strings.NewReader(content)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := userexport.Decode(strings.NewReader(`{"format": "modelkeeper-export", "formatVersion": 1}`)); err != nil {
		t.Errorf("empty export rejected: %v", err)
	}
}

// The files moved to a new NAS: the root folder gets the new path and keeps
// its ID; after a scan every model, tag and correction is where it was.
func TestRelocateRoot(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	userData(t, e, filepath.Join(t.TempDir(), "thumbnails"))
	models := e.int(`SELECT count(*) FROM model`)
	orc := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Orc/orc.stl'`))

	nas := filepath.Join(t.TempDir(), "nas")
	copyTree(t, e.root, nas)
	os.Remove(filepath.Join(nas, "Atlas", "Plain", "plain.stl")) // one file did not make it

	c, err := catalog.CheckRelocate(ctx, e.db, e.id, nas, "")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "sampled", c.Sampled, c.Total)
	expect(t, "found", c.Found, c.Total-1)
	if len(c.Missing) != 1 || c.Missing[0] != "Atlas/Plain/plain.stl" {
		t.Errorf("missing %v", c.Missing)
	}
	if _, err := catalog.CheckRelocate(ctx, e.db, e.id, filepath.Join(nas, "Atlas", "Orc", "orc.stl"), ""); err == nil {
		t.Error("a file accepted as new path")
	}
	os.WriteFile(filepath.Join(nas, "Atlas", "Plain", "plain.stl"), []byte("solid plain"), 0o644) // copied later

	snapshots := 0
	r, err := catalog.RelocateRoot(ctx, e.db, e.id, nas, "", func() error { snapshots++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "snapshot before", snapshots, 1)
	expect(t, "same id", r.ID, e.id)
	expect(t, "new path", r.Path, filepath.Clean(nas))
	os.RemoveAll(e.root) // the old place is gone
	e.scan()

	expect(t, "models", e.int(`SELECT count(*) FROM model`), models)
	d, err := catalog.GetModel(ctx, e.db, orc)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "tags kept", strings.Join(d.Tags, ","), "green skin,painted")
	expect(t, "files present", e.int(`SELECT count(*) FROM location WHERE model_id = ? AND present = 0`, orc), 0)
	expect(t, "bundle still one model", e.int(`SELECT count(DISTINCT model_id) FROM location WHERE path LIKE 'Atlas/Bundle/%'`), 1)
}
