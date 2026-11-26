package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/db"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/settings"
)

// TestLoad builds a large synthetic catalog (folders of creators and models,
// several files each, some duplicates) and measures the queries the UI runs
// (TODO 6.5). By default 5,000 models with 50,000 files; with
// MODELKEEPER_LOAD=full 20,000 models with 200,000 files (the size of a
// 10-20 TB collection). Each query must stay below a generous limit, so a
// change that makes one much slower fails here first.
func TestLoad(t *testing.T) {
	models, filesPer := 5000, 10
	limit := 5 * time.Second
	if os.Getenv("MODELKEEPER_LOAD") == "full" {
		models = 20000
		limit = 15 * time.Second
	}
	if testing.Short() {
		models = 500
	}
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "load.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(conn)

	start := time.Now()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tx.Exec(`INSERT INTO root (id, path, kind, created_at) VALUES (1, 'C:\lib', 'local', 0), (2, '\\nas\lib', 'nas', 0)`)
	fileStmt, _ := tx.Prepare(`INSERT INTO file (sha256, size) VALUES (?, ?) RETURNING id`)
	locStmt, _ := tx.Prepare(`INSERT INTO location (root_id, path, ext, category, size, mtime, file_id, first_seen_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, 0)`)
	n := 0
	var dupID int64
	for m := 0; m < models; m++ {
		creator := fmt.Sprintf("Creator %03d", m%250)
		for f := 0; f < filesPer; f++ {
			n++
			var sum [32]byte
			binary.LittleEndian.PutUint64(sum[:], uint64(n))
			sum = sha256.Sum256(sum[:])
			ext, cat := "stl", "model"
			switch f {
			case 7:
				ext, cat = "jpg", "supplementary"
			case 8:
				ext, cat = "ctb", "slicer"
			case 9:
				ext, cat = "zip", "archive"
			}
			size := int64(1_000_000 + n%5_000_000)
			var fid int64
			if f == 3 && m%20 == 0 && dupID != 0 {
				fid = dupID // every 20th model shares a file: duplicates
			} else if err := fileStmt.QueryRow(sum[:], size).Scan(&fid); err != nil {
				t.Fatal(err)
			}
			if f == 3 && dupID == 0 {
				dupID = fid
			}
			root := 1 + m%2
			p := fmt.Sprintf("%s/Model %05d/part %02d.%s", creator, m, f, ext)
			if _, err := locStmt.Exec(root, p, ext, cat, size, fid); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d models, %d files inserted in %v", models, n, time.Since(start))

	measureWithin := func(what string, max time.Duration, fn func() error) {
		t.Helper()
		s := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		d := time.Since(s)
		t.Logf("%-28s %v", what, d.Round(time.Millisecond))
		if d > max {
			t.Errorf("%s took %v (limit %v)", what, d, max)
		}
	}
	measure := func(what string, fn func() error) { t.Helper(); measureWithin(what, limit, fn) }
	// Detection runs in the background after a scan; it may take longer than
	// a query the UI waits for (about 0.3 ms per file on a CI runner).
	rules := detect.Rules{PartNames: settings.Defaults().PartNames, SizeTokens: settings.Defaults().SizeTokens}
	perRoot := time.Duration(n/2) * time.Millisecond
	measureWithin("detection root 1", perRoot, func() error { _, err := detect.Apply(ctx, conn, 1, rules); return err })
	measureWithin("detection root 2", perRoot, func() error { _, err := detect.Apply(ctx, conn, 2, rules); return err })
	var count int
	conn.QueryRow(`SELECT count(*) FROM model`).Scan(&count)
	if count < models*9/10 {
		t.Fatalf("only %d models detected", count)
	}

	var ids []int64
	rows, _ := conn.Query(`SELECT id FROM model ORDER BY id LIMIT 1000`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	measure("add tag to 1000 models", func() error { return AddTags(ctx, conn, ids, []string{"bulk", "orc"}) })
	measure("undo it", func() error { _, err := Undo(ctx, conn, nil); return err })
	AddTags(ctx, conn, ids[:500], []string{"orc"})

	page := func(q ModelQuery, offset int) func() error {
		return func() error {
			p, err := SearchModels(ctx, conn, q, offset, 100)
			if err == nil && p.Total == 0 && q.Text == "" && !q.Duplicates {
				return fmt.Errorf("no results")
			}
			return err
		}
	}
	measure("library, first page", page(ModelQuery{}, 0))
	measure("library, deep page", page(ModelQuery{}, count-200))
	measure("library, newest", page(ModelQuery{Sort: "newest"}, 0))
	measure("text search", page(ModelQuery{Text: "model 01"}, 0))
	tags, _ := ListTags(ctx, conn)
	measure("by tag", page(ModelQuery{TagID: tags[0].ID}, 0))
	measure("untagged", page(ModelQuery{Untagged: true}, 0))
	measure("duplicates", page(ModelQuery{Duplicates: true}, 0))
	measure("no backup", page(ModelQuery{SingleLocation: true}, 0))
	measure("by file type", page(ModelQuery{Ext: "ctb"}, 0))
	measure("filter options", func() error { _, err := GetFilterOptions(ctx, conn); return err })
	measure("tag list", func() error { _, err := ListTags(ctx, conn); return err })
	measure("storage statistics", func() error { _, err := GetStats(ctx, conn); return err })
	measure("folder statistics", func() error { _, err := GetRootStats(ctx, conn, 1); return err })
	measure("model detail", func() error { _, err := GetModel(ctx, conn, ids[10]); return err })
}
