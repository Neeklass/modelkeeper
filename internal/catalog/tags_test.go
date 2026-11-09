package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path"
	"path/filepath"
	"testing"

	"modelkeeper/internal/db"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/settings"
)

// testCatalog creates a catalog with one root and runs detection over paths.
func testCatalog(t *testing.T, paths ...string) (*sql.DB, func()) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	conn.Exec(`INSERT INTO root (id, path, kind, created_at) VALUES (1, 'C:\m', 'local', 0)`)
	for _, p := range paths {
		sum := sha256.Sum256([]byte(p))
		var fid int64
		conn.QueryRow(`INSERT INTO file (sha256, size) VALUES (?, 1) RETURNING id`, sum[:]).Scan(&fid)
		cat := "model"
		if path.Ext(p) == ".zip" {
			cat = "archive"
		}
		if _, err := conn.Exec(`INSERT INTO location (root_id, path, ext, category, size, mtime, file_id, first_seen_at)
			VALUES (1, ?, ?, ?, 1, 0, ?, 0)`, p, path.Ext(p)[1:], cat, fid); err != nil {
			t.Fatal(err)
		}
	}
	redetect := func() {
		d := settings.Defaults()
		if _, err := detect.Apply(context.Background(), conn, 1, detect.Rules{PartNames: d.PartNames, SizeTokens: d.SizeTokens}); err != nil {
			t.Fatal(err)
		}
	}
	redetect()
	return conn, redetect
}

func modelID(t *testing.T, conn *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(`SELECT id FROM model WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("model %q: %v", name, err)
	}
	return id
}

func names(p ModelPage) []string {
	var out []string
	for _, r := range p.Items {
		out = append(out, r.Name)
	}
	return out
}

func TestTags(t *testing.T) {
	ctx := context.Background()
	conn, redetect := testCatalog(t,
		"Creator/Undead/Skeleton/s.stl", "Creator/Undead/Zombie/z.stl", "Creator/Elf/e.stl")
	skel, zombie, elf := modelID(t, conn, "Skeleton"), modelID(t, conn, "Zombie"), modelID(t, conn, "Elf")

	// Bulk tagging (G2); whitespace is normalized.
	if err := AddTags(ctx, conn, []int64{skel, zombie}, []string{"  undead ", "Horror  Night"}); err != nil {
		t.Fatal(err)
	}
	tags, _ := ListTags(ctx, conn)
	if len(tags) != 2 || tags[0].Name != "Horror Night" || tags[1].Models != 2 {
		t.Fatalf("tags = %+v", tags)
	}
	// Accepting "Undead" (suggested from the folder) dismissed the suggestion.
	var state string
	conn.QueryRow(`SELECT state FROM tag_suggestion WHERE model_id = ? AND name = 'Undead'`, skel).Scan(&state)
	if state != "dismissed" {
		t.Errorf("suggestion state = %q", state)
	}

	// Tags are searchable (H1) and the untagged view shows the rest (G4).
	page, _ := SearchModels(ctx, conn, ModelQuery{Text: "horr"}, 0, 10)
	if page.Total != 2 {
		t.Errorf("search by tag: %v", names(page))
	}
	page, _ = SearchModels(ctx, conn, ModelQuery{Untagged: true}, 0, 10)
	if page.Total != 1 || page.Items[0].ID != elf {
		t.Errorf("untagged = %v", names(page))
	}
	page, _ = SearchModels(ctx, conn, ModelQuery{Text: "skeleton"}, 0, 10)
	if len(page.Items) != 1 || len(page.Items[0].Tags) != 2 {
		t.Errorf("row tags = %+v", page.Items)
	}

	// Removing the last use deletes the tag.
	if err := RemoveTag(ctx, conn, []int64{skel, zombie}, "Horror Night"); err != nil {
		t.Fatal(err)
	}
	if tags, _ := ListTags(ctx, conn); len(tags) != 1 {
		t.Errorf("tags after remove = %+v", tags)
	}
	if page, _ := SearchModels(ctx, conn, ModelQuery{Text: "horr"}, 0, 10); page.Total != 0 {
		t.Errorf("removed tag still found")
	}

	// Tags survive re-detection; invalid names are rejected.
	redetect()
	if page, _ := SearchModels(ctx, conn, ModelQuery{Text: "undead"}, 0, 10); page.Total < 2 {
		t.Errorf("tags lost after redetect")
	}
	if err := AddTags(ctx, conn, []int64{elf}, []string{"   "}); err == nil {
		t.Errorf("empty tag accepted")
	}
}

func TestUpdateModel(t *testing.T) {
	ctx := context.Background()
	conn, redetect := testCatalog(t, "Creator/Orc/Supported/orc.stl")
	orc := modelID(t, conn, "Orc")
	s := func(v string) *string { return &v }

	if err := UpdateModel(ctx, conn, orc, ModelUpdate{Name: s("Orc Warlord"), Notes: s("print at 50µm"),
		PrintStatus: s("printed"), Supports: s("unsupported")}); err != nil {
		t.Fatal(err)
	}
	redetect()
	d, err := GetModel(ctx, conn, orc)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Orc Warlord" || d.Notes != "print at 50µm" || d.PrintStatus != "printed" ||
		d.Supports != "unsupported" || !d.SupportsManual {
		t.Errorf("after redetect: %+v", d)
	}
	if page, _ := SearchModels(ctx, conn, ModelQuery{Text: "warlord 50"}, 0, 10); page.Total != 1 {
		t.Errorf("renamed model / notes not searchable")
	}

	// "auto" hands supports back to detection.
	if err := UpdateModel(ctx, conn, orc, ModelUpdate{Supports: s("auto")}); err != nil {
		t.Fatal(err)
	}
	redetect()
	d, _ = GetModel(ctx, conn, orc)
	if d.Supports != "presupported" || d.SupportsManual {
		t.Errorf("auto supports: %q manual=%v", d.Supports, d.SupportsManual)
	}

	for _, bad := range []ModelUpdate{{Name: s(" ")}, {PrintStatus: s("maybe")}, {Supports: s("x")}} {
		if err := UpdateModel(ctx, conn, orc, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
