package catalog

import (
	"context"
	"strings"
	"testing"

	"modelkeeper/internal/msg"
)

// Every change of user data can be undone, newest first; a model changed in
// another way since is left alone.
func TestUndo(t *testing.T) {
	ctx := context.Background()
	conn, _ := testCatalog(t, "Creator/Orc/o.stl", "Creator/Elf/e.stl", "Creator/Troll/t.stl")
	orc, elf, troll := modelID(t, conn, "Orc"), modelID(t, conn, "Elf"), modelID(t, conn, "Troll")
	detail := func(id int64) ModelDetail {
		t.Helper()
		d, err := GetModel(ctx, conn, id)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	if _, err := Undo(ctx, conn, nil); msg.From(err).Key != "history.nothingToUndo" {
		t.Fatalf("undo on an empty journal: %v", err)
	}

	// 1. Tags on three models, 2. one removed again by mistake.
	if err := AddTags(ctx, conn, []int64{orc, elf, troll}, []string{"painted", "fantasy"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTag(ctx, conn, []int64{orc, elf, troll}, "painted"); err != nil {
		t.Fatal(err)
	}
	// 3. Notes typed in several saves: one action.
	for _, n := range []string{"p", "pri", "print at 0.05"} {
		v := n
		if err := UpdateModel(ctx, conn, orc, ModelUpdate{Notes: &v}); err != nil {
			t.Fatal(err)
		}
	}
	// 4. Creator, origin and license.
	creator, origin, lic := "Someone Else", "MyMiniFactory", "personal use"
	if err := UpdateModel(ctx, conn, elf, ModelUpdate{Creator: &creator, Origin: &origin, License: &lic}); err != nil {
		t.Fatal(err)
	}
	if d := detail(elf); d.Creator != "Someone Else" || !d.CreatorManual || d.Origin != origin || d.License != lic {
		t.Fatalf("elf %+v", d)
	}
	// 5. Favorite.
	if err := SetFavorite(ctx, conn, []int64{troll}, true); err != nil {
		t.Fatal(err)
	}
	// Nothing changed: not recorded.
	if err := SetFavorite(ctx, conn, []int64{troll}, true); err != nil {
		t.Fatal(err)
	}

	h, err := ListHistory(ctx, conn, 0)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range h {
		actions = append(actions, e.Action)
	}
	if got := strings.Join(actions, " "); got != "history.favoritesAdded history.modelEdited history.modelEdited history.tagsRemoved history.tagsAdded" {
		t.Fatalf("history %s", got)
	}
	if h[3].Models != 3 || h[3].Params["tags"] != "painted" {
		t.Errorf("tags removed entry %+v", h[3])
	}

	undo := func(want string) UndoResult {
		t.Helper()
		r, err := Undo(ctx, conn, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Action != want {
			t.Fatalf("undid %s, want %s", r.Action, want)
		}
		return r
	}
	undo("history.favoritesAdded")
	if detail(troll).Favorite {
		t.Error("favorite not undone")
	}
	undo("history.modelEdited")
	if d := detail(elf); d.Creator != "Creator" || d.CreatorManual || d.Origin != "" || d.License != "" {
		t.Errorf("elf after undo %+v", d)
	}
	undo("history.modelEdited")
	if d := detail(orc); d.Notes != "" {
		t.Errorf("notes after undo %q", d.Notes)
	}

	// Meanwhile the troll is changed outside the journal (as a merge would).
	conn.Exec(`UPDATE model SET notes = 'merged' WHERE id = ?`, troll)
	r := undo("history.tagsRemoved")
	if r.Restored != 2 || r.Skipped != 1 {
		t.Errorf("undo of the removal: %+v", r)
	}
	if d := detail(orc); strings.Join(d.Tags, ",") != "fantasy,painted" {
		t.Errorf("orc tags %v", d.Tags)
	}
	if d := detail(troll); strings.Join(d.Tags, ",") != "fantasy" || d.Notes != "merged" {
		t.Errorf("troll was changed since and must stay: %+v", d)
	}

	undo("history.tagsAdded")
	if d := detail(orc); len(d.Tags) != 0 {
		t.Errorf("orc tags after undoing everything %v", d.Tags)
	}
	if tags, _ := ListTags(ctx, conn); len(tags) != 1 { // the troll keeps "fantasy"
		t.Errorf("tags left %+v", tags)
	}
	if _, err := Undo(ctx, conn, nil); msg.From(err).Key != "history.nothingToUndo" {
		t.Errorf("undo after everything: %v", err)
	}
	// Searchable state follows the undo.
	page, err := SearchModels(ctx, conn, ModelQuery{Text: "painted"}, 0, 10)
	if err != nil || page.Total != 0 {
		t.Errorf("search after undo: %+v, %v", page, err)
	}
}
