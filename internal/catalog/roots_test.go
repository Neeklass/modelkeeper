package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"modelkeeper/internal/db"
	"modelkeeper/internal/msg"
)

func openCatalog(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	return conn
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// msgKey returns the message key of an error ("" for nil).
func msgKey(err error) string {
	if err == nil {
		return ""
	}
	return msg.From(err).Key
}

func exec(t *testing.T, conn *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func count(t *testing.T, conn *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// purgeCatalog: root 1 is deactivated, root 2 active. Models (by name):
//
//	tagged     files only on root 1, a tag          -> loses its last location
//	copied     files on root 1 and 2, notes         -> keeps root 2
//	plain      files only on root 1, no user data   -> not counted (removed silently, as before)
//	anchored   only an anchor on root 1, favorite   -> loses its last location
//	elsewhere  files only on root 2, notes          -> not affected
//	orphan     no location at all, notes            -> not affected (nothing on root 1)
//	corrected  files only on root 1, an override    -> loses its last location
func purgeCatalog(t *testing.T) *sql.DB {
	conn := openCatalog(t)
	exec(t, conn, `INSERT INTO root (id, path, kind, created_at, removed_at) VALUES (1, 'C:\old', 'local', 0, 1)`)
	exec(t, conn, `INSERT INTO root (id, path, kind, created_at) VALUES (2, 'C:\new', 'local', 0)`)
	models := []struct {
		id    int64
		name  string
		roots []int64
	}{
		{1, "tagged", []int64{1}}, {2, "copied", []int64{1, 2}}, {3, "plain", []int64{1}}, {4, "anchored", nil},
		{5, "elsewhere", []int64{2}}, {6, "orphan", nil}, {7, "corrected", []int64{1}},
	}
	for _, m := range models {
		exec(t, conn, `INSERT INTO model (id, uid, name, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`, m.id, m.name, m.name)
		for _, r := range m.roots {
			exec(t, conn, `INSERT INTO location (root_id, path, ext, category, size, mtime, present, first_seen_at, model_id)
				VALUES (?, ?, 'stl', 'model', 1, 0, 1, 0, ?)`, r, m.name+"/a.stl", m.id)
			exec(t, conn, `INSERT INTO model_anchor (model_id, root_id, path, kind) VALUES (?, ?, ?, 'folder')`, m.id, r, m.name)
		}
	}
	exec(t, conn, `INSERT INTO tag (id, name) VALUES (1, 'orc')`)
	exec(t, conn, `INSERT INTO model_tag (model_id, tag_id) VALUES (1, 1)`)
	exec(t, conn, `UPDATE model SET notes = 'n' WHERE id IN (2, 5, 6)`)
	exec(t, conn, `INSERT INTO model_anchor (model_id, root_id, path, kind) VALUES (4, 1, 'anchored', 'folder')`)
	exec(t, conn, `UPDATE model SET favorite = 1 WHERE id = 4`)
	exec(t, conn, `INSERT INTO override (kind, root_id, path, model_id, created_at) VALUES ('is_model', 1, 'corrected', 7, 0)`)
	return conn
}

func TestPurgeImpact(t *testing.T) {
	conn := purgeCatalog(t)
	n, err := PurgeImpact(context.Background(), conn, 1)
	if err != nil || n != 3 {
		t.Fatalf("PurgeImpact = %d, %v, want 3 (tagged, anchored, corrected)", n, err)
	}
	if n, _ := PurgeImpact(context.Background(), conn, 2); n != 1 {
		t.Errorf("PurgeImpact(root 2) = %d, want 1 (elsewhere; copied keeps root 1)", n)
	}
	removed, err := ListRemovedRootsWithImpact(context.Background(), conn)
	if err != nil || len(removed) != 1 || removed[0].ID != 1 || removed[0].Orphaned != 3 || removed[0].Files != 4 {
		t.Errorf("ListRemovedRootsWithImpact = %+v, %v", removed, err)
	}
}

func TestPurgeRootGuards(t *testing.T) {
	conn := purgeCatalog(t)
	ctx := context.Background()
	intact := func(what string) {
		t.Helper()
		if count(t, conn, `SELECT count(*) FROM root WHERE id = 1`) != 1 || count(t, conn, `SELECT count(*) FROM location WHERE root_id = 1`) != 4 {
			t.Fatalf("%s: root 1 was changed", what)
		}
	}
	never := func() error { t.Error("snapshot taken although the purge is refused"); return nil }

	if err := PurgeRoot(ctx, conn, 2, 2, never); msgKey(err) != "error.rootNotRemoved" {
		t.Errorf("purging an active root: %v", err)
	}
	if err := PurgeRoot(ctx, conn, 1, 2, never); msgKey(err) != "error.purgeCountChanged" {
		t.Errorf("wrong confirmation: %v", err)
	}
	intact("wrong confirmation")

	// The snapshot right before fails: nothing is deleted.
	snapshotErr := msg.Wrap(nil, "error.backupFailed")
	if err := PurgeRoot(ctx, conn, 1, 3, func() error { return snapshotErr }); err != snapshotErr {
		t.Errorf("failed snapshot: %v", err)
	}
	intact("failed snapshot")
}

func TestPurgeRoot(t *testing.T) {
	conn := purgeCatalog(t)
	snapshots := 0
	if err := PurgeRoot(context.Background(), conn, 1, 3, func() error { snapshots++; return nil }); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 {
		t.Errorf("%d snapshots", snapshots)
	}
	if n := count(t, conn, `SELECT count(*) FROM root WHERE id = 1`) + count(t, conn, `SELECT count(*) FROM location WHERE root_id = 1`) +
		count(t, conn, `SELECT count(*) FROM model_anchor WHERE root_id = 1`) + count(t, conn, `SELECT count(*) FROM override`); n != 0 {
		t.Errorf("%d rows of root 1 left", n)
	}
	var left []string
	rows, _ := conn.Query(`SELECT name FROM model ORDER BY id`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		left = append(left, s)
	}
	rows.Close()
	// plain had no user data; corrected only had its correction on root 1.
	if got := strings.Join(left, ","); got != "tagged,copied,anchored,elsewhere,orphan" {
		t.Errorf("models left: %s", got)
	}
	if count(t, conn, `SELECT count(*) FROM model_tag`) != 1 {
		t.Error("tag of tagged lost")
	}
}

// A root must neither contain the data folder nor lie inside it (J1, M).
func TestAddRootRejectsDataFolder(t *testing.T) {
	conn := openCatalog(t)
	ctx := context.Background()
	base := t.TempDir()
	data := mkdir(t, filepath.Join(base, "modelkeeper", "data"))
	mkdir(t, filepath.Join(data, "thumbnails"))

	for _, p := range []string{base, filepath.Join(base, "modelkeeper"), data, filepath.Join(data, "thumbnails")} {
		if _, err := AddRoot(ctx, conn, p, data); msgKey(err) != "error.rootContainsData" {
			t.Errorf("AddRoot(%s): %v, want error.rootContainsData", p, err)
		}
	}
	other := mkdir(t, filepath.Join(base, "models"))
	if _, err := AddRoot(ctx, conn, other, data); err != nil {
		t.Errorf("AddRoot(%s): %v", other, err)
	}
}

// The models PurgeImpact counts can be listed (to look at them first).
func TestOrphanedByFilter(t *testing.T) {
	conn := purgeCatalog(t)
	page, err := SearchModels(context.Background(), conn, ModelQuery{OrphanedBy: 1}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	got := names(page)
	sort.Strings(got)
	if strings.Join(got, ",") != "anchored,corrected,tagged" {
		t.Errorf("orphanedBy: %v", got)
	}
	if n, _ := PurgeImpact(context.Background(), conn, 1); n != page.Total {
		t.Errorf("filter total %d, PurgeImpact %d", page.Total, n)
	}
}
