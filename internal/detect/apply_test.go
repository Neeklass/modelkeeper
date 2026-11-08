package detect

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path"
	"path/filepath"
	"testing"

	"modelkeeper/internal/db"
)

type fixture struct {
	t  *testing.T
	db *sql.DB
}

func newFixture(t *testing.T) *fixture {
	conn, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	return &fixture{t: t, db: conn}
}

func (f *fixture) exec(q string, args ...any) sql.Result {
	f.t.Helper()
	r, err := f.db.Exec(q, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	return r
}

func (f *fixture) root(p string) int64 {
	id, _ := f.exec(`INSERT INTO root (path, kind, created_at) VALUES (?, 'local', 0)`, p).LastInsertId()
	return id
}

// file adds a present disk location whose content is identified by content.
func (f *fixture) file(root int64, p, content string) int64 {
	sum := sha256.Sum256([]byte(content))
	var fid int64
	if err := f.db.QueryRow(`INSERT INTO file (sha256, size) VALUES (?, 1)
		ON CONFLICT (sha256) DO UPDATE SET size = 1 RETURNING id`, sum[:]).Scan(&fid); err != nil {
		f.t.Fatal(err)
	}
	ext := path.Ext(p)[1:]
	id, _ := f.exec(`INSERT INTO location (root_id, path, ext, category, size, mtime, file_id, first_seen_at)
		VALUES (?, ?, ?, ?, 1, 0, ?, 0)`, root, p, ext, cat(p), fid).LastInsertId()
	return id
}

func (f *fixture) apply(root int64) Result {
	f.t.Helper()
	r, err := Apply(context.Background(), f.db, root, defaultRules())
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fixture) modelOf(locID int64) int64 {
	var id sql.NullInt64
	f.db.QueryRow(`SELECT model_id FROM location WHERE id = ?`, locID).Scan(&id)
	return id.Int64
}

func (f *fixture) count(q string, args ...any) int {
	var n int
	if err := f.db.QueryRow(q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestApplyStableAcrossRuns(t *testing.T) {
	f := newFixture(t)
	r := f.root(`C:\models`)
	a := f.file(r, "Creator/Collection/Orc/S_Orc/orc.stl", "orc-s")
	b := f.file(r, "Creator/Collection/Orc/M_Orc/orc.stl", "orc-m")
	f.file(r, "Creator/Collection/Elf/elf.stl", "elf") // second model, so Orc is not a single subfolder
	g := f.file(r, "Creator/Goblin/goblin.stl", "goblin")

	res := f.apply(r)
	if res.Models != 3 || res.Created != 3 {
		t.Fatalf("first run: %+v", res)
	}
	orc := f.modelOf(a)
	if orc == 0 || f.modelOf(b) != orc || f.modelOf(g) == orc {
		t.Fatalf("assignment wrong: %d %d %d", orc, f.modelOf(b), f.modelOf(g))
	}
	if n := f.count(`SELECT count(*) FROM variant WHERE model_id = ?`, orc); n != 2 {
		t.Errorf("variants = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM tag_suggestion WHERE model_id = ? AND name = 'Collection'`, orc); n != 1 {
		t.Errorf("tag suggestion missing")
	}
	if n := f.count(`SELECT count(*) FROM model m JOIN creator c ON c.id = m.creator_id WHERE c.name = 'Creator'`); n != 3 {
		t.Errorf("creator models = %d", n)
	}

	// User edits survive; the model ID is stable.
	f.exec(`UPDATE model SET name = 'My Orc', name_manual = 1 WHERE id = ?`, orc)
	f.exec(`UPDATE tag_suggestion SET state = 'dismissed' WHERE model_id = ?`, orc)
	res = f.apply(r)
	if res.Created != 0 || f.modelOf(a) != orc {
		t.Fatalf("second run: %+v, model %d -> %d", res, orc, f.modelOf(a))
	}
	var name, state string
	f.db.QueryRow(`SELECT name FROM model WHERE id = ?`, orc).Scan(&name)
	f.db.QueryRow(`SELECT state FROM tag_suggestion WHERE model_id = ?`, orc).Scan(&state)
	if name != "My Orc" || state != "dismissed" {
		t.Errorf("name=%q state=%q", name, state)
	}

	// Search index.
	if n := f.count(`SELECT count(*) FROM model_fts WHERE model_fts MATCH 'gob*'`); n != 1 {
		t.Errorf("fts gob* = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM model_fts WHERE model_fts MATCH 'my orc'`); n != 1 {
		t.Errorf("fts 'my orc' = %d", n)
	}
}

func TestApplyRecognizesMovedFolder(t *testing.T) {
	f := newFixture(t)
	r := f.root(`C:\models`)
	old := f.file(r, "Creator/Orc/orc.stl", "orc")
	f.file(r, "Creator/Orc/orc_base.stl", "orc-base")
	f.apply(r)
	orc := f.modelOf(old)
	f.exec(`UPDATE model SET notes = 'printed twice' WHERE id = ?`, orc)

	// Folder renamed: old locations missing, same content at a new path.
	f.exec(`UPDATE location SET present = 0 WHERE root_id = ?`, r)
	n1 := f.file(r, "Creator/Orc Warrior/orc.stl", "orc")
	f.file(r, "Creator/Orc Warrior/orc_base.stl", "orc-base")

	res := f.apply(r)
	if res.Moved != 1 || res.Created != 0 || f.modelOf(n1) != orc {
		t.Fatalf("moved: %+v model %d want %d", res, f.modelOf(n1), orc)
	}
	var anchor, name string
	f.db.QueryRow(`SELECT path FROM model_anchor WHERE model_id = ?`, orc).Scan(&anchor)
	f.db.QueryRow(`SELECT name FROM model WHERE id = ?`, orc).Scan(&name)
	if anchor != "Creator/Orc Warrior" || name != "Orc Warrior" {
		t.Errorf("anchor=%q name=%q", anchor, name)
	}
	if f.modelOf(old) != 0 {
		t.Errorf("old missing location still assigned")
	}
}

func TestApplyLinksSameModelOnOtherRoot(t *testing.T) {
	f := newFixture(t)
	local := f.root(`C:\models`)
	nas := f.root(`\nas\models`)
	l1 := f.file(local, "Creator/Orc/orc.stl", "orc")
	f.file(local, "Creator/Orc/orc_base.stl", "orc-base")
	f.file(local, "Creator/Shared/base_32mm.stl", "base") // shared base must not link models
	f.apply(local)

	n1 := f.file(nas, "Creator/Orc (copy)/orc.stl", "orc")
	f.file(nas, "Creator/Orc (copy)/orc_base.stl", "orc-base")
	n3 := f.file(nas, "Creator/Elf/elf.stl", "elf")
	f.file(nas, "Creator/Elf/base_32mm.stl", "base")
	res := f.apply(nas)
	if res.Linked != 1 || res.Created != 1 {
		t.Fatalf("nas run: %+v", res)
	}
	if f.modelOf(n1) != f.modelOf(l1) {
		t.Errorf("same content not linked")
	}
	if f.modelOf(n3) == f.modelOf(l1) {
		t.Errorf("elf wrongly linked")
	}
	if n := f.count(`SELECT count(*) FROM model_anchor WHERE model_id = ?`, f.modelOf(l1)); n != 2 {
		t.Errorf("anchors = %d", n)
	}
	// Rerunning the local root keeps everything stable.
	if res := f.apply(local); res.Created != 0 || res.Linked != 0 {
		t.Errorf("rerun local: %+v", res)
	}
	if f.modelOf(n1) != f.modelOf(l1) {
		t.Errorf("link lost after rerun")
	}
}

func TestApplyGarbageCollects(t *testing.T) {
	f := newFixture(t)
	r := f.root(`C:\models`)
	a := f.file(r, "Creator/Orc/orc.stl", "orc")
	f.apply(r)
	f.exec(`DELETE FROM location WHERE id = ?`, a) // e.g. root removed
	f.file(r, "Creator/Elf/elf.stl", "elf")
	f.apply(r)
	if n := f.count(`SELECT count(*) FROM model`); n != 1 {
		t.Errorf("models = %d, want 1", n)
	}
	if n := f.count(`SELECT count(*) FROM model_fts`); n != 1 {
		t.Errorf("fts rows = %d, want 1", n)
	}
}
