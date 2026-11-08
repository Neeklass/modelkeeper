package scanner

import (
	"context"
	"testing"

	"modelkeeper/internal/catalog"
)

func TestScanDetectsAndSearchesModels(t *testing.T) {
	e := newEnv(t)
	e.write("Atlas/Stella/Stella Bust/Supported/bust_SUP.stl", []byte("bust sup"))
	e.write("Atlas/Stella/Stella Bust/Unsupported/bust.stl", []byte("bust"))
	e.write("Atlas/Stella/Stella Bust/render.jpg", []byte("img"))
	e.write("Atlas/Stella/Stella Full/S_Stella/full.stl", []byte("full s"))
	e.write("Atlas/Stella/Stella Full/M_Stella/full.stl", []byte("full m"))
	e.write("Atlas/Orc Warband.zip", makeZip(t, zipEntry{name: "orc.stl", data: stlA}))

	p := e.scan()
	expect(t, "status", p.Status, StatusDone)
	expect(t, "models found", p.ModelsFound, 3)
	expect(t, "models created", p.ModelsCreated, 3)

	ctx := context.Background()
	all, err := catalog.SearchModels(ctx, e.db, catalog.ModelQuery{Text: ""}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "total", all.Total, 3)
	expect(t, "first by name", all.Items[0].Name, "Orc Warband")

	hits, err := catalog.SearchModels(ctx, e.db, catalog.ModelQuery{Text: "stel bu"}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Total != 1 || hits.Items[0].Name != "Stella Bust" {
		t.Fatalf("search = %+v", hits)
	}
	bust := hits.Items[0]
	expect(t, "creator", bust.Creator, "Atlas")
	expect(t, "supports", bust.Supports, "both")
	expect(t, "files", bust.Files, 2)
	expect(t, "tag suggestions", bust.Suggestion, 1)

	full, _ := catalog.SearchModels(ctx, e.db, catalog.ModelQuery{Text: "full"}, 0, 50)
	if full.Total != 1 || len(full.Items[0].Variants) != 2 || full.Items[0].Variants[0] != "S" {
		t.Fatalf("full = %+v", full)
	}

	d, err := catalog.GetModel(ctx, e.db, bust.ID)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "detail files incl. image", len(d.Files), 3)
	expect(t, "anchor", d.Anchors[0].Path, "Atlas/Stella/Stella Bust")
	expect(t, "suggestion", d.TagSuggested[0], "Stella")
	expect(t, "hash", len(d.Files[0].Hash), 12)

	// Unchanged rescan keeps model IDs.
	e.scan()
	again, _ := catalog.SearchModels(ctx, e.db, catalog.ModelQuery{Text: "stella bust"}, 0, 50)
	expect(t, "stable id", again.Items[0].ID, bust.ID)

	// Redetect alone works too.
	if err := e.mgr.Redetect(e.id); err != nil {
		t.Fatal(err)
	}
	e.mgr.Wait()
	expect(t, "redetect status", e.mgr.Progress().Status, StatusDone)
	expect(t, "redetect models", e.mgr.Progress().ModelsFound, 3)
}
