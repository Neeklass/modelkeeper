package catalog

import (
	"context"
	"testing"
)

func TestFavorites(t *testing.T) {
	ctx := context.Background()
	conn, redetect := testCatalog(t, "Atlas/Orc/orc.stl", "Atlas/Elf/elf.stl", "Atlas/Goblin/goblin.stl")
	orc, elf := modelID(t, conn, "Orc"), modelID(t, conn, "Elf")

	if err := SetFavorite(ctx, conn, []int64{orc, elf}, true); err != nil {
		t.Fatal(err)
	}
	page, err := SearchModels(ctx, conn, ModelQuery{Favorites: true}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(page); len(got) != 2 || got[0] != "Elf" || got[1] != "Orc" {
		t.Fatalf("favorites = %v", got)
	}
	if !page.Items[0].Favorite {
		t.Error("row not marked as favorite")
	}
	all, _ := SearchModels(ctx, conn, ModelQuery{}, 0, 50)
	for _, r := range all.Items {
		if r.Favorite != (r.Name != "Goblin") {
			t.Errorf("%s: favorite = %v", r.Name, r.Favorite)
		}
	}
	d, _ := GetModel(ctx, conn, orc)
	if !d.Favorite {
		t.Error("detail not marked as favorite")
	}

	// Favorites survive rescans, and combine with search text.
	redetect()
	hits, _ := SearchModels(ctx, conn, ModelQuery{Favorites: true, Text: "or"}, 0, 50)
	if got := names(hits); len(got) != 1 || got[0] != "Orc" {
		t.Errorf("favorites + search = %v", got)
	}

	if err := SetFavorite(ctx, conn, []int64{elf}, false); err != nil {
		t.Fatal(err)
	}
	page, _ = SearchModels(ctx, conn, ModelQuery{Favorites: true}, 0, 50)
	if got := names(page); len(got) != 1 || got[0] != "Orc" {
		t.Errorf("after removing Elf: %v", got)
	}

	// A favorite whose files are all gone is kept (user data), like a tagged model.
	conn.Exec(`UPDATE location SET present = 0 WHERE path = 'Atlas/Orc/orc.stl'`)
	redetect()
	if _, err := GetModel(ctx, conn, orc); err != nil {
		t.Errorf("favorite removed after its files went missing: %v", err)
	}
}
