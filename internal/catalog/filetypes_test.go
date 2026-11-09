package catalog

import (
	"context"
	"testing"
)

func TestApplyFileTypes(t *testing.T) {
	ctx := context.Background()
	conn := filterCatalog(t, []loc{
		{root: 1, path: "Atlas/Orc/orc.stl", content: "orc", present: true},
		{root: 1, path: "Atlas/Orc/orc.lys", content: "orc-lys", present: true},
		{root: 1, path: "Atlas/Orc/cover.jpg", content: "cover", present: true},
		{root: 1, path: "Atlas/Pack.zip", content: "pack", status: "ok", present: true},
		{root: 1, path: "pack/elf.stl", content: "elf", archive: "Atlas/Pack.zip", present: true},
		{root: 1, path: "pack/elf.jpg", content: "elf-jpg", archive: "Atlas/Pack.zip", present: true},
		{root: 1, path: "Atlas/Bad.zip", content: "bad", status: "broken", present: true},
	})
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := conn.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	cats := map[string]string{"stl": "model", "lys": "slicer", "zip": "archive", "jpg": "supplementary"}
	apply := func(added bool) FileTypesChange {
		t.Helper()
		ch, err := ApplyFileTypes(ctx, conn, func(ext string) string { return cats[ext] }, added)
		if err != nil {
			t.Fatal(err)
		}
		return ch
	}

	// Unchanged types: nothing happens.
	if ch := apply(false); ch != (FileTypesChange{}) {
		t.Fatalf("no change: %+v", ch)
	}

	// jpg dropped (on disk and in the archive), lys moved to supplementary.
	delete(cats, "jpg")
	cats["lys"] = "supplementary"
	ch := apply(false)
	if ch.Removed != 2 || ch.Recategorized != 1 || ch.ArchivesPending != 0 {
		t.Fatalf("drop jpg: %+v", ch)
	}
	if n := count(`SELECT count(*) FROM location WHERE ext = 'jpg'`); n != 0 {
		t.Errorf("%d jpg locations left", n)
	}
	if n := count(`SELECT count(*) FROM location WHERE ext = 'lys' AND category = 'supplementary'`); n != 1 {
		t.Errorf("lys not recategorized")
	}

	// A type was added: archives (ok and broken) are read again on the next scan.
	cats["pdf"] = "supplementary"
	ch = apply(true)
	if ch.ArchivesPending != 2 {
		t.Fatalf("added type: %+v", ch)
	}
	if n := count(`SELECT count(*) FROM location WHERE category = 'archive' AND archive_status = 'pending'`); n != 2 {
		t.Errorf("%d archives pending, want 2", n)
	}

	// zip no longer an archive but a model type (odd, but allowed): members go, state is cleared.
	cats["zip"] = "model"
	ch = apply(false)
	if ch.Recategorized != 2 {
		t.Fatalf("zip as model: %+v", ch)
	}
	if n := count(`SELECT count(*) FROM location WHERE archive_id IS NOT NULL`); n != 0 {
		t.Errorf("%d archive members left", n)
	}
	if n := count(`SELECT count(*) FROM location WHERE ext = 'zip' AND archive_status IS NULL AND category = 'model'`); n != 2 {
		t.Errorf("zip state not cleared")
	}

	// And back: archives again, pending.
	cats["zip"] = "archive"
	apply(false)
	if n := count(`SELECT count(*) FROM location WHERE ext = 'zip' AND archive_status = 'pending'`); n != 2 {
		t.Errorf("zip not pending after becoming an archive again")
	}
}
