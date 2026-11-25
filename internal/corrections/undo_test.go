package corrections

import (
	"context"
	"strings"
	"testing"

	"modelkeeper/internal/catalog"
)

func (f *fx) str(q string, args ...any) string {
	f.t.Helper()
	var s string
	if err := f.db.QueryRow(q, args...).Scan(&s); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	return s
}

func (f *fx) tags(id int64) string {
	f.t.Helper()
	d, err := catalog.GetModel(context.Background(), f.db, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Join(d.Tags, ",")
}

// correct applies a correction like the app does: detection, then the
// journal gets the models' states after it.
func (f *fx) correct(_ []int64, err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
	f.detect()
	if err := catalog.FinishCorrection(context.Background(), f.db); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) undo(want string) catalog.UndoResult {
	f.t.Helper()
	r, err := catalog.Undo(context.Background(), f.db, func(context.Context, []int64) error {
		f.detect()
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if r.Action != catalog.CorrectionPrefix+want {
		f.t.Fatalf("undid %s, want %s", r.Action, want)
	}
	return r
}

func orcs(t *testing.T) (f *fx, body, weapons int64, weaponsUID string) {
	f = newFx(t)
	f.file("Creator/Orc Body/body.stl")
	f.file("Creator/Orc Weapons/axe.stl")
	f.file("Creator/Orc Weapons/sword.stl")
	f.detect()
	body, weapons = f.model("Creator/Orc Body"), f.model("Creator/Orc Weapons")
	ctx := context.Background()
	if err := catalog.AddTags(ctx, f.db, []int64{weapons}, []string{"weapons"}); err != nil {
		t.Fatal(err)
	}
	notes := "axes!"
	catalog.UpdateModel(ctx, f.db, weapons, catalog.ModelUpdate{Notes: &notes})
	notes = "body notes"
	catalog.UpdateModel(ctx, f.db, body, catalog.ModelUpdate{Notes: &notes})
	return f, body, weapons, f.str(`SELECT uid FROM model WHERE id = ?`, weapons)
}

// A merge is undone completely: the folder is a model of its own again, with
// its old ID, tags and notes; the target has its own data back.
func TestUndoMerge(t *testing.T) {
	f, body, weapons, weaponsUID := orcs(t)
	f.correct(Merge(context.Background(), f.db, body, []int64{weapons}))
	if f.modelOfFile("Creator/Orc Weapons/axe.stl") != body || f.n(`SELECT count(*) FROM model WHERE id = ?`, weapons) != 0 {
		t.Fatal("not merged")
	}

	r := f.undo("merge")
	if r.Restored != 2 || r.Skipped != 0 {
		t.Errorf("undo: %+v", r)
	}
	w := f.modelOfFile("Creator/Orc Weapons/axe.stl")
	if w == 0 || w == body || f.modelOfFile("Creator/Orc Weapons/sword.stl") != w {
		t.Fatalf("weapons not separate again: %d (body %d)", w, body)
	}
	if got := f.str(`SELECT uid FROM model WHERE id = ?`, w); got != weaponsUID {
		t.Errorf("weapons got uid %s, want its old %s", got, weaponsUID)
	}
	if got := f.tags(w); got != "weapons" {
		t.Errorf("weapons tags %q", got)
	}
	if got := f.str(`SELECT notes FROM model WHERE id = ?`, w); got != "axes!" {
		t.Errorf("weapons notes %q", got)
	}
	if got, notes := f.tags(body), f.str(`SELECT notes FROM model WHERE id = ?`, body); got != "" || notes != "body notes" {
		t.Errorf("body after undo: tags %q notes %q", got, notes)
	}
	// Stable on the next detection.
	f.detect()
	if f.modelOfFile("Creator/Orc Weapons/axe.stl") != w {
		t.Error("undo not stable")
	}
}

// Reset of the corrections after a merge separates the folders again (the
// merged place no longer keeps the target, migration 0011).
func TestResetUnmerges(t *testing.T) {
	f, body, weapons, _ := orcs(t)
	f.correct(Merge(context.Background(), f.db, body, []int64{weapons}))
	f.correct(Reset(context.Background(), f.db, body))
	w := f.modelOfFile("Creator/Orc Weapons/axe.stl")
	if w == 0 || w == body {
		t.Fatalf("reset did not separate the merged folder: %d", w)
	}
	if f.modelOfFile("Creator/Orc Body/body.stl") != body {
		t.Error("the target lost its own folder")
	}
}

// "Is a model" on a pack folder, undone: the subfolders are models again.
func TestUndoMarkModel(t *testing.T) {
	f := newFx(t)
	f.file("Creator/Pack/Orc/orc.stl")
	f.file("Creator/Pack/Elf/elf.stl")
	f.detect()
	orc := f.model("Creator/Pack/Orc")
	uid := f.str(`SELECT uid FROM model WHERE id = ?`, orc)
	catalog.AddTags(context.Background(), f.db, []int64{orc}, []string{"green"})

	f.correct(MarkModel(context.Background(), f.db, f.root, "Creator/Pack"))
	if f.modelOfFile("Creator/Pack/Orc/orc.stl") != f.modelOfFile("Creator/Pack/Elf/elf.stl") {
		t.Fatal("not one model")
	}
	f.undo("markModel")
	o, e := f.modelOfFile("Creator/Pack/Orc/orc.stl"), f.modelOfFile("Creator/Pack/Elf/elf.stl")
	if o == 0 || o == e {
		t.Fatalf("not separate again: %d %d", o, e)
	}
	if got := f.str(`SELECT uid FROM model WHERE id = ?`, o); got != uid {
		t.Errorf("orc uid %s, want %s", got, uid)
	}
	if got := f.tags(o); got != "green" {
		t.Errorf("orc tags %q", got)
	}
	if f.n(`SELECT count(*) FROM override`) != 0 {
		t.Error("override left")
	}
}

// A model changed in another way after the correction is left as it is.
func TestUndoCorrectionKeepsLaterChanges(t *testing.T) {
	f, body, weapons, _ := orcs(t)
	f.correct(Merge(context.Background(), f.db, body, []int64{weapons}))
	f.db.Exec(`UPDATE model SET notes = 'written after the merge' WHERE id = ?`, body) // not in the journal
	r := f.undo("merge")
	if r.Skipped != 1 || r.Restored != 1 {
		t.Errorf("undo: %+v", r)
	}
	if got := f.str(`SELECT notes FROM model WHERE id = ?`, body); got != "written after the merge" {
		t.Errorf("later change lost: %q", got)
	}
	if w := f.modelOfFile("Creator/Orc Weapons/axe.stl"); w == body || f.tags(w) != "weapons" {
		t.Errorf("weapons not restored")
	}
}
