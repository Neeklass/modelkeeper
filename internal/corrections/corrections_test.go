package corrections

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"modelkeeper/internal/db"
	"modelkeeper/internal/detect"
	"modelkeeper/internal/settings"
)

type fx struct {
	t    *testing.T
	db   *sql.DB
	root int64
}

func newFx(t *testing.T) *fx {
	conn, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	f := &fx{t: t, db: conn}
	r, _ := conn.Exec(`INSERT INTO root (path, kind, created_at) VALUES ('C:\models', 'local', 0)`)
	f.root, _ = r.LastInsertId()
	return f
}

func (f *fx) file(p string) {
	sum := sha256.Sum256([]byte(p))
	var fid int64
	if err := f.db.QueryRow(`INSERT INTO file (sha256, size) VALUES (?, 1) RETURNING id`, sum[:]).Scan(&fid); err != nil {
		f.t.Fatal(err)
	}
	cat := "model"
	if strings.HasSuffix(p, ".zip") {
		cat = "archive"
	}
	if _, err := f.db.Exec(`INSERT INTO location (root_id, path, ext, category, size, mtime, file_id, first_seen_at)
		VALUES (?, ?, ?, ?, 1, 0, ?, 0)`, f.root, p, path.Ext(p)[1:], cat, fid); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) detect() {
	f.t.Helper()
	d := settings.Defaults()
	if _, err := detect.Apply(context.Background(), f.db, f.root, detect.Rules{PartNames: d.PartNames, SizeTokens: d.SizeTokens}); err != nil {
		f.t.Fatal(err)
	}
}

// model returns the id of the model anchored at path (0 if none).
func (f *fx) model(p string) int64 {
	var id int64
	f.db.QueryRow(`SELECT model_id FROM model_anchor WHERE path = ?`, p).Scan(&id)
	return id
}

func (f *fx) modelOfFile(p string) int64 {
	var id sql.NullInt64
	f.db.QueryRow(`SELECT model_id FROM location WHERE path = ?`, p).Scan(&id)
	return id.Int64
}

func (f *fx) n(q string, args ...any) int {
	var n int
	if err := f.db.QueryRow(q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fx) must(_ []int64, err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
	f.detect()
}

func TestMarkModelAndSplit(t *testing.T) {
	f := newFx(t)
	f.file("Creator/Pack/Orc/orc.stl")
	f.file("Creator/Pack/Elf/elf.stl")
	f.detect()
	orc := f.model("Creator/Pack/Orc")
	if orc == 0 || f.model("Creator/Pack/Elf") == 0 {
		t.Fatal("baseline models missing")
	}
	f.db.Exec(`UPDATE model SET notes = 'keep me' WHERE id = ?`, orc)

	// The pack is really one model.
	f.must(MarkModel(context.Background(), f.db, f.root, "Creator/Pack"))
	pack := f.model("Creator/Pack")
	if pack == 0 || f.modelOfFile("Creator/Pack/Orc/orc.stl") != pack || f.modelOfFile("Creator/Pack/Elf/elf.stl") != pack {
		t.Fatalf("pack not one model: pack=%d", pack)
	}
	// Orc had notes, so it is kept (empty) instead of being deleted; Elf is gone.
	if f.n(`SELECT count(*) FROM model WHERE id = ?`, orc) != 1 && f.n(`SELECT count(*) FROM model WHERE notes = 'keep me'`) != 1 {
		t.Errorf("model with user data was deleted")
	}

	// Rescan: correction survives (F4e).
	f.detect()
	if f.modelOfFile("Creator/Pack/Elf/elf.stl") != pack {
		t.Errorf("correction lost on rescan")
	}

	// Split it again into its subfolders.
	if _, err := Reset(context.Background(), f.db, pack); err != nil {
		t.Fatal(err)
	}
	f.must(MarkModel(context.Background(), f.db, f.root, "Creator/Pack"))
	pack = f.model("Creator/Pack")
	f.must(Split(context.Background(), f.db, pack))
	if f.modelOfFile("Creator/Pack/Orc/orc.stl") == f.modelOfFile("Creator/Pack/Elf/elf.stl") {
		t.Errorf("split did not separate subfolders")
	}
}

func TestNotModel(t *testing.T) {
	f := newFx(t)
	f.file("Creator/Chibis/Jinx/a.stl")
	f.file("Creator/Chibis/Vi/b.stl")
	f.detect()
	f.must(MarkNotModel(context.Background(), f.db, f.root, "Creator/Chibis/Jinx"))
	if f.model("Creator/Chibis") == 0 || f.modelOfFile("Creator/Chibis/Jinx/a.stl") != f.model("Creator/Chibis") {
		t.Errorf("jinx not merged into parent")
	}
	if f.model("Creator/Chibis/Jinx") != 0 {
		t.Errorf("stale anchor for jinx")
	}
}

func TestMergeAndDetach(t *testing.T) {
	f := newFx(t)
	f.file("Creator/Orc Body/body.stl")
	f.file("Creator/Orc Weapons/axe.stl")
	f.file("Creator/Orc Weapons/sword.stl")
	f.detect()
	body, weapons := f.model("Creator/Orc Body"), f.model("Creator/Orc Weapons")
	f.db.Exec(`INSERT INTO tag (name) VALUES ('orc')`)
	f.db.Exec(`INSERT INTO model_tag (model_id, tag_id) SELECT ?, id FROM tag`, weapons)
	f.db.Exec(`UPDATE model SET notes = 'axes!' WHERE id = ?`, weapons)

	f.must(Merge(context.Background(), f.db, body, []int64{weapons}))
	if f.modelOfFile("Creator/Orc Weapons/axe.stl") != body {
		t.Fatalf("not merged")
	}
	if f.n(`SELECT count(*) FROM model WHERE id = ?`, weapons) != 0 {
		t.Errorf("emptied source model not removed")
	}
	if f.n(`SELECT count(*) FROM model_tag WHERE model_id = ?`, body) != 1 {
		t.Errorf("tag not moved")
	}
	var name, notes string
	f.db.QueryRow(`SELECT name, notes FROM model WHERE id = ?`, body).Scan(&name, &notes)
	if name != "Orc Body" || notes != "axes!" {
		t.Errorf("name=%q notes=%q", name, notes)
	}
	f.detect() // survives rescans
	if f.modelOfFile("Creator/Orc Weapons/sword.stl") != body {
		t.Errorf("merge lost on rescan")
	}
	if ok, _ := HasCorrections(context.Background(), f.db, body); !ok {
		t.Errorf("HasCorrections = false")
	}

	// Detach the weapons folder again.
	f.must(Detach(context.Background(), f.db, body, f.root, "Creator/Orc Weapons"))
	w := f.modelOfFile("Creator/Orc Weapons/axe.stl")
	if w == 0 || w == body {
		t.Fatalf("detach failed: %d", w)
	}
	f.detect()
	if f.modelOfFile("Creator/Orc Weapons/axe.stl") != w {
		t.Errorf("detached model not stable")
	}
}

func TestDetachSplitTree(t *testing.T) {
	f := newFx(t)
	f.file("Creator/Line/Pre-Supported/Knight/k_sup.stl")
	f.file("Creator/Line/Knight/k.stl")
	f.file("Creator/Line/Mage/m.stl")
	f.detect()
	knight := f.model("Creator/Line/Knight")
	if knight == 0 || f.model("Creator/Line/Pre-Supported/Knight") != knight {
		t.Fatalf("split trees not merged into one model with two anchors")
	}
	f.must(Detach(context.Background(), f.db, knight, f.root, "Creator/Line/Pre-Supported/Knight"))
	if f.modelOfFile("Creator/Line/Pre-Supported/Knight/k_sup.stl") == knight {
		t.Errorf("tree not detached")
	}
}
