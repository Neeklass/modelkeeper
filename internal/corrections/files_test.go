package corrections

import (
	"context"
	"testing"

	"modelkeeper/internal/msg"
)

func (f *fx) loc(p string) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(`SELECT id FROM location WHERE path = ?`, p).Scan(&id); err != nil {
		f.t.Fatalf("location %s: %v", p, err)
	}
	return id
}

func (f *fx) locs(ps ...string) []int64 {
	var out []int64
	for _, p := range ps {
		out = append(out, f.loc(p))
	}
	return out
}

// image adds a supplementary file (not content).
func (f *fx) image(p string) {
	if _, err := f.db.Exec(`INSERT INTO location (root_id, path, ext, category, size, mtime, first_seen_at)
		VALUES (?, ?, 'png', 'image', 1, 0, 0)`, f.root, p); err != nil {
		f.t.Fatal(err)
	}
}

func wantKey(t *testing.T, err error, key string) {
	t.Helper()
	if err == nil || msg.From(err).Key != key {
		t.Fatalf("got %v, want %s", err, key)
	}
}

// Files from another folder join a model and stay there on every rescan.
func TestMoveFiles(t *testing.T) {
	f, body, weapons, _ := orcs(t)
	ctx := context.Background()
	f.correct(MoveFiles(ctx, f.db, weapons, body, f.locs("Creator/Orc Weapons/axe.stl")))
	if f.modelOfFile("Creator/Orc Weapons/axe.stl") != body {
		t.Fatalf("axe not moved into body")
	}
	if f.modelOfFile("Creator/Orc Weapons/sword.stl") != weapons {
		t.Fatalf("sword must stay in weapons")
	}
	f.detect()
	f.detect()
	if f.modelOfFile("Creator/Orc Weapons/axe.stl") != body || f.modelOfFile("Creator/Orc Weapons/sword.stl") != weapons {
		t.Fatalf("file correction lost on rescan")
	}
	if f.str(`SELECT name FROM model WHERE id = ?`, body) != "Orc Body" {
		t.Errorf("moving a file must not rename the target")
	}

	// Undo puts the axe back.
	f.undo("moveFiles")
	if f.modelOfFile("Creator/Orc Weapons/axe.stl") != weapons {
		t.Fatalf("undo did not move the axe back")
	}
	if f.n(`SELECT count(*) FROM override`) != 0 {
		t.Errorf("undo left overrides behind")
	}
}

// Moving every content file of a model is a merge: its notes move along.
func TestMoveAllFilesMerges(t *testing.T) {
	f, body, weapons, _ := orcs(t)
	ctx := context.Background()
	f.correct(MoveFiles(ctx, f.db, weapons, body, f.locs("Creator/Orc Weapons/axe.stl", "Creator/Orc Weapons/sword.stl")))
	if f.modelOfFile("Creator/Orc Weapons/sword.stl") != body {
		t.Fatalf("sword not merged into body")
	}
	if f.n(`SELECT count(*) FROM model WHERE id = ?`, weapons) != 0 {
		t.Errorf("weapons model should be merged away")
	}
	if f.tags(body) != "weapons" {
		t.Errorf("tags of the merged model lost: %q", f.tags(body))
	}
}

// Selected files become a model of their own; the rest stays.
func TestDetachFiles(t *testing.T) {
	f := newFx(t)
	f.file("Creator/Pack/a.stl")
	f.file("Creator/Pack/b.stl")
	f.file("Creator/Pack/c.stl")
	f.image("Creator/Pack/c.png")
	f.detect()
	pack := f.model("Creator/Pack")
	ctx := context.Background()

	_, err := DetachFiles(ctx, f.db, pack, f.locs("Creator/Pack/a.stl", "Creator/Pack/b.stl", "Creator/Pack/c.stl"))
	wantKey(t, err, "corrections.detachAll")
	_, err = DetachFiles(ctx, f.db, pack, f.locs("Creator/Pack/c.png"))
	wantKey(t, err, "corrections.detachNeedsContent")

	f.correct(DetachFiles(ctx, f.db, pack, f.locs("Creator/Pack/c.stl", "Creator/Pack/b.stl", "Creator/Pack/c.png")))
	b, c := f.modelOfFile("Creator/Pack/b.stl"), f.modelOfFile("Creator/Pack/c.stl")
	if b == 0 || b == pack || b != c || f.modelOfFile("Creator/Pack/c.png") != b {
		t.Fatalf("b, c and the picture should form one new model: pack=%d b=%d c=%d", pack, b, c)
	}
	if f.modelOfFile("Creator/Pack/a.stl") != pack {
		t.Fatalf("a must stay in the pack")
	}
	f.detect()
	if f.modelOfFile("Creator/Pack/b.stl") != b || f.modelOfFile("Creator/Pack/a.stl") != pack {
		t.Fatalf("detached model not stable on rescan")
	}

	// Resetting the pack's corrections brings the files back.
	f.correct(Reset(ctx, f.db, pack))
	if f.modelOfFile("Creator/Pack/b.stl") != pack || f.n(`SELECT count(*) FROM override`) != 0 {
		t.Fatalf("reset did not bring the files back")
	}
}

// Moving a file away from a group keeps the rest of the group together, even
// when the moved file named the group.
func TestRegroupKeepsTheRest(t *testing.T) {
	f := newFx(t)
	f.file("Creator/Pack/a.stl")
	f.file("Creator/Pack/b.stl")
	f.file("Creator/Pack/c.stl")
	f.file("Creator/Other/x.stl")
	f.detect()
	pack, other := f.model("Creator/Pack"), f.model("Creator/Other")
	ctx := context.Background()

	f.correct(DetachFiles(ctx, f.db, pack, f.locs("Creator/Pack/b.stl", "Creator/Pack/c.stl")))
	detached := f.modelOfFile("Creator/Pack/b.stl")
	// b names the group; move b alone into Other.
	f.correct(MoveFiles(ctx, f.db, detached, other, f.locs("Creator/Pack/b.stl")))
	if f.modelOfFile("Creator/Pack/b.stl") != other {
		t.Fatalf("b not moved into other")
	}
	cm := f.modelOfFile("Creator/Pack/c.stl")
	if cm == other || cm == pack || cm == 0 {
		t.Fatalf("c must stay split off on its own: c=%d pack=%d other=%d", cm, pack, other)
	}
	f.detect()
	if f.modelOfFile("Creator/Pack/c.stl") != cm || f.modelOfFile("Creator/Pack/b.stl") != other {
		t.Fatalf("not stable on rescan")
	}
}

func TestMoveFilesRejectsForeignFiles(t *testing.T) {
	f, body, weapons, _ := orcs(t)
	ctx := context.Background()
	_, err := MoveFiles(ctx, f.db, weapons, body, f.locs("Creator/Orc Body/body.stl"))
	wantKey(t, err, "corrections.invalidFile")
	_, err = MoveFiles(ctx, f.db, weapons, weapons, f.locs("Creator/Orc Weapons/axe.stl"))
	wantKey(t, err, "corrections.sameModel")
}
