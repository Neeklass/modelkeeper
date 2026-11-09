package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"modelkeeper/internal/catalog"
)

// F6 with real scans: a model folder copied to another place, scanned, then
// the original deleted and scanned again. Tags and notes stay with the model.
func TestCopyThenDeleteKeepsTags(t *testing.T) {
	e := newEnv(t)
	e.write("Atlas/Orc/orc.stl", stlA)
	e.write("Atlas/Orc/orc_base.stl", stlB)
	e.write("Atlas/Elf/elf.stl", []byte("solid elf"))
	e.scan()
	ctx := context.Background()
	orcID := int64(e.int(`SELECT model_id FROM location WHERE path = 'Atlas/Orc/orc.stl'`))
	if err := catalog.AddTags(ctx, e.db, []int64{orcID}, []string{"painted"}); err != nil {
		t.Fatal(err)
	}
	e.db.Exec(`UPDATE model SET notes = 'printed twice' WHERE id = ?`, orcID)

	e.write("Sorted/Orc/orc.stl", stlA)
	e.write("Sorted/Orc/orc_base.stl", stlB)
	e.scan()
	os.RemoveAll(filepath.Join(e.root, "Atlas", "Orc"))
	p := e.scan()
	expect(t, "status", p.Status, StatusDone)

	d, err := catalog.GetModel(ctx, e.db, orcID)
	if err != nil {
		t.Fatalf("original model gone: %v", err)
	}
	expect(t, "tags", len(d.Tags), 1)
	expect(t, "notes", d.Notes, "printed twice")
	expect(t, "anchor", d.Anchors[0].Path, "Sorted/Orc")
	expect(t, "anchors", len(d.Anchors), 1)
	missing := 0
	for _, f := range d.Files {
		if !f.Present {
			missing++
		}
	}
	expect(t, "no missing files listed", missing, 0)
	expect(t, "files", len(d.Files), 2)
	expect(t, "one Orc model", e.int(`SELECT count(*) FROM model WHERE name = 'Orc'`), 1)
}
