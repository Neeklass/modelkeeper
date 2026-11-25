package catalog

import (
	"context"
	"strings"
	"testing"
)

// Renaming, merging and deleting a tag affects all its models at once and
// is undoable.
func TestTagManagement(t *testing.T) {
	ctx := context.Background()
	conn, _ := testCatalog(t, "C/Orc/o.stl", "C/Elf/e.stl", "C/Troll/t.stl")
	orc, elf, troll := modelID(t, conn, "Orc"), modelID(t, conn, "Elf"), modelID(t, conn, "Troll")
	AddTags(ctx, conn, []int64{orc, elf}, []string{"pianted"})
	AddTags(ctx, conn, []int64{troll}, []string{"painted"})
	tags := func(id int64) string {
		t.Helper()
		d, err := GetModel(ctx, conn, id)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(d.Tags, ",")
	}

	// Typo fixed: merged into the existing tag.
	n, err := RenameTag(ctx, conn, "pianted", "painted")
	if err != nil || n != 2 {
		t.Fatalf("rename: %d, %v", n, err)
	}
	if list, _ := ListTags(ctx, conn); len(list) != 1 || list[0].Models != 3 {
		t.Fatalf("tags after merge %+v", list)
	}
	// Plain rename, then only the spelling.
	if _, err := RenameTag(ctx, conn, "painted", "Painted"); err != nil {
		t.Fatal(err)
	}
	if got := tags(orc); got != "Painted" {
		t.Errorf("orc tags %q", got)
	}
	if _, err := RenameTag(ctx, conn, "nope", "x"); err == nil {
		t.Error("renaming an unknown tag")
	}

	// Delete, then undo everything step by step.
	if n, err := DeleteTag(ctx, conn, "painted"); err != nil || n != 3 {
		t.Fatalf("delete: %d, %v", n, err)
	}
	if got := tags(troll); got != "" {
		t.Errorf("troll after delete %q", got)
	}
	for _, want := range []string{"history.tagDeleted", "history.tagRenamed", "history.tagMerged"} {
		r, err := Undo(ctx, conn, nil)
		if err != nil || r.Action != want {
			t.Fatalf("undo: %+v, %v, want %s", r, err, want)
		}
	}
	if a, b, c := tags(orc), tags(elf), tags(troll); a != "pianted" || b != "pianted" || c != "painted" {
		t.Errorf("after undo: %q %q %q", a, b, c)
	}
}

// Models without any files (imported, waiting for a scan) can be listed and
// removed; models with files are never removed that way.
func TestForgetWaiting(t *testing.T) {
	ctx := context.Background()
	conn, _ := testCatalog(t, "C/Orc/o.stl")
	orc := modelID(t, conn, "Orc")
	var ghost int64
	if err := conn.QueryRow(`INSERT INTO model (uid, name, notes, created_at, updated_at)
		VALUES ('g', 'Ghost', 'from an import', 0, 0) RETURNING id`).Scan(&ghost); err != nil {
		t.Fatal(err)
	}
	AddTags(ctx, conn, []int64{ghost}, []string{"only-ghost"})

	page, err := SearchModels(ctx, conn, ModelQuery{Waiting: true}, 0, 10)
	if err != nil || page.Total != 1 || page.Items[0].ID != ghost || !page.Items[0].Waiting {
		t.Fatalf("waiting: %+v, %v", page, err)
	}
	if d, _ := GetModel(ctx, conn, ghost); !d.Waiting {
		t.Error("detail not waiting")
	}
	n, err := ForgetWaiting(ctx, conn, []int64{ghost, orc})
	if err != nil || n != 1 {
		t.Fatalf("forget: %d, %v", n, err)
	}
	if _, err := GetModel(ctx, conn, orc); err != nil {
		t.Errorf("model with files removed: %v", err)
	}
	if list, _ := ListTags(ctx, conn); len(list) != 0 {
		t.Errorf("unused tag left %+v", list)
	}
}
