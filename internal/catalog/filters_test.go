package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"modelkeeper/internal/db"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/settings"
)

type loc struct {
	root    int64
	path    string
	content string // identical content = identical file
	archive string // path of the containing archive (same root), "" on disk
	status  string // archive_status for archives
	present bool
}

func filterCatalog(t *testing.T, locs []loc) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	conn.Exec(`INSERT INTO root (id, path, kind, created_at) VALUES (1, 'C:\local', 'local', 0)`)
	conn.Exec(`INSERT INTO root (id, path, kind, status, created_at) VALUES (2, '\nas\m', 'nas', 'online', 0)`)
	ids := map[string]int64{}
	for _, l := range locs {
		sum := sha256.Sum256([]byte(l.content))
		var fid int64
		if err := conn.QueryRow(`INSERT INTO file (sha256, size) VALUES (?, 1)
			ON CONFLICT (sha256) DO UPDATE SET size = 1 RETURNING id`, sum[:]).Scan(&fid); err != nil {
			t.Fatal(err)
		}
		ext := path.Ext(l.path)[1:]
		cat := map[string]string{"stl": "model", "lys": "slicer", "zip": "archive", "jpg": "supplementary"}[ext]
		var archiveID, status any
		if l.archive != "" {
			archiveID = ids[l.archive]
		}
		if l.status != "" {
			status = l.status
		}
		r, err := conn.Exec(`INSERT INTO location (root_id, archive_id, path, ext, category, size, mtime, file_id,
			archive_status, present, first_seen_at) VALUES (?, ?, ?, ?, ?, 1, 0, ?, ?, ?, 0)`,
			l.root, archiveID, l.path, ext, cat, fid, status, l.present)
		if err != nil {
			t.Fatal(err)
		}
		ids[l.path], _ = r.LastInsertId()
	}
	d := settings.Defaults()
	for _, root := range []int64{1, 2} {
		if _, err := detect.Apply(context.Background(), conn, root, detect.Rules{PartNames: d.PartNames, SizeTokens: d.SizeTokens}); err != nil {
			t.Fatal(err)
		}
	}
	return conn
}

func TestFilters(t *testing.T) {
	ctx := context.Background()
	conn := filterCatalog(t, []loc{
		{root: 1, path: "Atlas/Orc/Supported/orc.stl", content: "orc", present: true},
		{root: 1, path: "Atlas/Orc/orc.lys", content: "orc-lys", present: true},
		{root: 1, path: "Atlas/Elf/elf.stl", content: "elf", present: true},
		{root: 1, path: "Atlas/Elf/elf_base.stl", content: "elf-base", present: true},
		{root: 2, path: "Atlas/Elf/elf.stl", content: "elf", present: true}, // same model on the NAS
		{root: 2, path: "Atlas/Elf/elf_base.stl", content: "elf-base", present: true},
		{root: 1, path: "Bastet/Goblin/goblin.stl", content: "goblin", present: true},
		{root: 1, path: "Bastet/Goblin.zip", content: "goblin-zip", status: "ok", present: true},
		{root: 1, path: "goblin.stl", content: "goblin", archive: "Bastet/Goblin.zip", present: true},
		{root: 1, path: "Bastet/Broken.zip", content: "broken", status: "broken", present: true},
		{root: 1, path: "Bastet/Gone/gone.stl", content: "gone", present: true}, // disappears below
		{root: 1, path: "Bastet/Gone/still.stl", content: "still", present: true},
	})
	// A later scan finds gone.stl missing; detection runs again.
	conn.Exec(`UPDATE location SET present = 0 WHERE path = 'Bastet/Gone/gone.stl'`)
	d := settings.Defaults()
	if _, err := detect.Apply(ctx, conn, 1, detect.Rules{PartNames: d.PartNames, SizeTokens: d.SizeTokens}); err != nil {
		t.Fatal(err)
	}
	orc, elf := modelID(t, conn, "Orc"), modelID(t, conn, "Elf")
	AddTags(ctx, conn, []int64{orc}, []string{"greenskin"})
	s := func(v string) *string { return &v }
	UpdateModel(ctx, conn, elf, ModelUpdate{PrintStatus: s("printed")})
	conn.Exec(`UPDATE root SET status = 'offline' WHERE id = 2`)

	var tagID, atlasID int64
	conn.QueryRow(`SELECT id FROM tag WHERE name = 'greenskin'`).Scan(&tagID)
	conn.QueryRow(`SELECT id FROM creator WHERE name = 'Atlas'`).Scan(&atlasID)

	for _, c := range []struct {
		name string
		q    ModelQuery
		want []string
	}{
		{"all", ModelQuery{}, []string{"Broken", "Elf", "Goblin", "Goblin", "Gone", "Orc"}},
		{"tag", ModelQuery{TagID: tagID}, []string{"Orc"}},
		{"creator", ModelQuery{CreatorID: atlasID}, []string{"Elf", "Orc"}},
		{"root nas", ModelQuery{RootID: 2}, []string{"Elf"}},
		{"ext lys", ModelQuery{Ext: "LYS"}, []string{"Orc"}},
		{"supports", ModelQuery{Supports: "presupported"}, []string{"Orc"}},
		{"print status", ModelQuery{PrintStatus: "printed"}, []string{"Elf"}},
		{"duplicates", ModelQuery{Duplicates: true}, []string{"Elf", "Goblin", "Goblin"}},
		{"single location", ModelQuery{SingleLocation: true}, []string{"Broken", "Goblin", "Goblin", "Gone", "Orc"}},
		{"offline", ModelQuery{Offline: true}, []string{"Elf"}},
		{"broken archives", ModelQuery{BrokenArchives: true}, []string{"Broken"}},
		{"extracted and archive", ModelQuery{ExtractedAndArchive: true}, []string{"Goblin"}},
		{"missing", ModelQuery{Missing: true}, []string{"Gone"}},
		{"combined", ModelQuery{CreatorID: atlasID, Text: "elf", Offline: true}, []string{"Elf"}},
		{"combined empty", ModelQuery{TagID: tagID, Offline: true}, nil},
	} {
		page, err := SearchModels(ctx, conn, c.q, 0, 50)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := names(page)
		sort.Strings(got)
		if !reflect.DeepEqual(got, c.want) || page.Total != len(c.want) {
			t.Errorf("%s: got %v (total %d), want %v", c.name, got, page.Total, c.want)
		}
	}

	opts, err := GetFilterOptions(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Creators) != 2 || opts.Creators[0].Name != "Atlas" || opts.Creators[0].Models != 2 {
		t.Errorf("creators = %+v", opts.Creators)
	}
	var exts []string
	for _, e := range opts.Exts {
		exts = append(exts, e.Name)
	}
	if !reflect.DeepEqual(exts, []string{"lys", "stl", "zip"}) {
		t.Errorf("exts = %v", exts)
	}
}
