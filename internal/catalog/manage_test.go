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

// Fields of many models at once, undoable; prints, collections, saved searches.
func TestBulkPrintsCollections(t *testing.T) {
	ctx := context.Background()
	conn, _ := testCatalog(t, "C/Orc/o.stl", "C/Elf/e.stl", "C/Troll/t.stl")
	orc, elf, troll := modelID(t, conn, "Orc"), modelID(t, conn, "Elf"), modelID(t, conn, "Troll")

	status, lic := "printed", "personal use"
	if err := UpdateModels(ctx, conn, []int64{orc, elf}, ModelUpdate{PrintStatus: &status, License: &lic}); err != nil {
		t.Fatal(err)
	}
	if d, _ := GetModel(ctx, conn, elf); d.PrintStatus != "printed" || d.License != lic {
		t.Errorf("elf %+v", d)
	}
	name := "x"
	if err := UpdateModels(ctx, conn, []int64{orc}, ModelUpdate{Name: &name}); err == nil {
		t.Error("bulk rename accepted")
	}
	if r, err := Undo(ctx, conn, nil); err != nil || r.Action != "history.modelsEdited" || r.Restored != 2 {
		t.Errorf("undo bulk: %+v %v", r, err)
	}

	// Print log: the newest entry sets the status.
	day := int64(1_700_000_000_000)
	if _, err := AddPrint(ctx, conn, Print{ModelID: troll, PrintedAt: day, Result: "failed", Printer: "Saturn", Settings: "2.5 s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddPrint(ctx, conn, Print{ModelID: troll, PrintedAt: day - 86_400_000, Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	d, _ := GetModel(ctx, conn, troll)
	if d.PrintStatus != "failed" || len(d.Prints) != 2 || d.Prints[0].Printer != "Saturn" {
		t.Errorf("troll prints %+v status %s", d.Prints, d.PrintStatus)
	}
	if _, err := AddPrint(ctx, conn, Print{ModelID: troll, Result: "maybe"}); err == nil {
		t.Error("bad result accepted")
	}

	// Collections keep their order.
	c, err := CreateCollection(ctx, conn, "  D&D   campaign ")
	if err != nil || c.Name != "D&D campaign" {
		t.Fatalf("create %+v %v", c, err)
	}
	if n, err := AddToCollection(ctx, conn, c.ID, []int64{troll, orc, troll}); err != nil || n != 2 {
		t.Fatalf("add: %d %v", n, err)
	}
	AddToCollection(ctx, conn, c.ID, []int64{elf})
	order := func() string {
		page, err := SearchModels(ctx, conn, ModelQuery{CollectionID: c.ID}, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, r := range page.Items {
			names = append(names, r.Name)
		}
		return strings.Join(names, ",")
	}
	if got := order(); got != "Troll,Orc,Elf" {
		t.Errorf("order %s", got)
	}
	MoveInCollection(ctx, conn, c.ID, elf, -1)
	if got := order(); got != "Troll,Elf,Orc" {
		t.Errorf("after move %s", got)
	}
	if d, _ := GetModel(ctx, conn, elf); len(d.Collections) != 1 || d.Collections[0].Name != "D&D campaign" {
		t.Errorf("elf collections %+v", d.Collections)
	}
	RemoveFromCollection(ctx, conn, c.ID, []int64{orc})
	if list, _ := ListCollections(ctx, conn); len(list) != 1 || list[0].Models != 2 {
		t.Errorf("collections %+v", list)
	}

	s, err := SaveSearch(ctx, conn, "Dragons", `{"text":"dragon"}`)
	if err != nil {
		t.Fatal(err)
	}
	SaveSearch(ctx, conn, "dragons", `{"text":"dragon","printStatus":"not_printed"}`) // same name: replaced
	if list, _ := ListSavedSearches(ctx, conn); len(list) != 1 || list[0].ID != s.ID || !strings.Contains(list[0].Query, "not_printed") {
		t.Errorf("saved %+v", list)
	}
	if _, err := SaveSearch(ctx, conn, "bad", `{`); err == nil {
		t.Error("invalid query accepted")
	}
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	conn, _ := testCatalog(t, "C/Orc/o.stl", "C/Elf/e.stl", "C/Troll/t.zip")
	// A copy of the orc elsewhere: same content.
	conn.Exec(`INSERT INTO location (root_id, path, ext, category, size, mtime, file_id, first_seen_at)
		SELECT root_id, 'D/Copy/o.stl', ext, category, size, mtime, file_id, 0 FROM location WHERE path = 'C/Orc/o.stl'`)
	st, err := GetStats(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 4 || st.Bytes != 4 || len(st.Roots) != 1 || st.Roots[0].Files != 4 {
		t.Errorf("totals %+v", st)
	}
	if st.DupFiles != 1 || st.DupExtra != 1 || len(st.Duplicates) != 1 || st.Duplicates[0].Copies != 2 {
		t.Errorf("duplicates %+v %+v", st.DupFiles, st.Duplicates)
	}
	cats := map[string]int64{}
	for _, c := range st.Categories {
		cats[c.Key] = c.Files
	}
	if cats["model"] != 3 || cats["archive"] != 1 {
		t.Errorf("categories %+v", st.Categories)
	}
	if len(st.Creators) == 0 || st.Creators[0].Key != "C" {
		t.Errorf("creators %+v", st.Creators)
	}
}
