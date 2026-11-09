package detect

import "testing"

// tag gives a model a user tag (user data that must survive moves, F6).
func (f *fixture) tag(model int64, name string) {
	f.t.Helper()
	res := f.exec(`INSERT INTO tag (name) VALUES (?) ON CONFLICT (name) DO UPDATE SET name = excluded.name`, name)
	tid, _ := res.LastInsertId()
	if tid == 0 {
		f.db.QueryRow(`SELECT id FROM tag WHERE name = ?`, name).Scan(&tid)
	}
	f.exec(`INSERT INTO model_tag (model_id, tag_id) VALUES (?, ?)`, model, tid)
}

func (f *fixture) tags(model int64) int {
	return f.count(`SELECT count(*) FROM model_tag WHERE model_id = ?`, model)
}

// Moved by copying first and deleting the original later (e.g. Explorer
// across drives, or a scan between the two steps): the copy is detected as a
// new model while the original still exists. Once the original is gone, the
// copy takes over the original's identity and user data (F6, D6).
func TestCopyThenDeleteKeepsUserData(t *testing.T) {
	f := newFixture(t)
	r := f.root(`C:\models`)
	old := f.file(r, "Creator/Orc/orc.stl", "orc")
	f.file(r, "Creator/Orc/orc_base.stl", "orc-base")
	f.file(r, "Creator/Elf/elf.stl", "elf")
	f.apply(r)
	orc := f.modelOf(old)
	f.tag(orc, "painted")
	f.exec(`UPDATE model SET notes = 'printed twice', print_status = 'printed' WHERE id = ?`, orc)

	// Copy: a second model with the same content (a duplicate, for now).
	c1 := f.file(r, "Other/Orc/orc.stl", "orc")
	f.file(r, "Other/Orc/orc_base.stl", "orc-base")
	f.apply(r)
	if f.modelOf(c1) == orc || f.modelOf(c1) == 0 {
		t.Fatalf("copy should be its own model while the original exists: %d", f.modelOf(c1))
	}
	copyModel := f.modelOf(c1)

	// Original deleted.
	f.exec(`UPDATE location SET present = 0 WHERE path LIKE 'Creator/Orc/%'`)
	res := f.apply(r)
	if got := f.modelOf(c1); got != orc {
		t.Fatalf("copy not adopted by the original model: %d, want %d (res %+v)", got, orc, res)
	}
	if res.Moved != 1 {
		t.Errorf("moved = %d", res.Moved)
	}
	if f.tags(orc) != 1 || f.count(`SELECT count(*) FROM model WHERE id = ? AND notes = 'printed twice' AND print_status = 'printed'`, orc) != 1 {
		t.Error("user data lost")
	}
	if f.count(`SELECT count(*) FROM model WHERE id = ?`, copyModel) != 0 {
		t.Error("the copy's model still exists")
	}
	if f.count(`SELECT count(*) FROM model_anchor WHERE model_id = ? AND path = 'Other/Orc'`, orc) != 1 ||
		f.count(`SELECT count(*) FROM model_anchor WHERE path = 'Creator/Orc'`) != 0 {
		t.Error("anchors not moved")
	}
	if f.count(`SELECT count(*) FROM location WHERE model_id = ? AND present = 0`, orc) != 0 {
		t.Error("old locations still listed as missing files of the model")
	}
	if n := f.count(`SELECT count(*) FROM model_fts WHERE model_fts MATCH 'orc*'`); n != 1 {
		t.Errorf("search finds %d Orc models, want 1", n)
	}
	// Stable afterwards.
	if res := f.apply(r); res.Created != 0 || f.modelOf(c1) != orc {
		t.Fatalf("not stable: %+v", res)
	}
}

// If the copy has user data of its own, nothing is merged automatically:
// two models with data are the user's decision (merge in the UI).
func TestCopyWithOwnDataIsNotMerged(t *testing.T) {
	f := newFixture(t)
	r := f.root(`C:\models`)
	old := f.file(r, "Creator/Orc/orc.stl", "orc")
	f.file(r, "Creator/Orc/orc_base.stl", "orc-base")
	f.apply(r)
	orc := f.modelOf(old)
	f.tag(orc, "painted")
	c1 := f.file(r, "Other/Orc/orc.stl", "orc")
	f.file(r, "Other/Orc/orc_base.stl", "orc-base")
	f.apply(r)
	copyModel := f.modelOf(c1)
	f.tag(copyModel, "to print")

	f.exec(`UPDATE location SET present = 0 WHERE path LIKE 'Creator/Orc/%'`)
	f.apply(r)
	if f.modelOf(c1) != copyModel || f.tags(orc) != 1 || f.tags(copyModel) != 1 {
		t.Fatal("models with own data were merged")
	}
}

// Moved to another root folder (e.g. from the PC to the NAS): the model is
// linked on the new root; once the old root is rescanned, its stale anchor
// and missing files no longer belong to the model.
func TestMoveToOtherRoot(t *testing.T) {
	f := newFixture(t)
	pc := f.root(`C:\models`)
	nas := f.root(`\\fritz.nas\models`)
	old := f.file(pc, "Creator/Orc/orc.stl", "orc")
	f.file(pc, "Creator/Orc/orc_base.stl", "orc-base")
	f.apply(pc)
	orc := f.modelOf(old)
	f.tag(orc, "painted")

	// Copied to the NAS and the NAS scanned first: same model, two locations.
	n1 := f.file(nas, "Archive/Orc/orc.stl", "orc")
	f.file(nas, "Archive/Orc/orc_base.stl", "orc-base")
	f.apply(nas)
	if f.modelOf(n1) != orc {
		t.Fatalf("NAS copy not linked: %d", f.modelOf(n1))
	}

	// Deleted on the PC, PC rescanned.
	f.exec(`UPDATE location SET present = 0 WHERE root_id = ?`, pc)
	f.apply(pc)
	if f.tags(orc) != 1 || f.modelOf(n1) != orc {
		t.Fatal("model lost")
	}
	if f.count(`SELECT count(*) FROM model_anchor WHERE model_id = ? AND root_id = ?`, orc, pc) != 0 {
		t.Error("stale anchor on the old root")
	}
	if f.count(`SELECT count(*) FROM location WHERE model_id = ? AND root_id = ?`, orc, pc) != 0 {
		t.Error("missing files on the old root still belong to the model")
	}
}

// The other order: deleted on the PC and the PC rescanned before the NAS
// copy was scanned. The model keeps its missing files meanwhile, then the
// NAS scan links the copy and the next PC scan cleans up.
func TestMoveToOtherRootDeletedFirst(t *testing.T) {
	f := newFixture(t)
	pc := f.root(`C:\models`)
	nas := f.root(`\\fritz.nas\models`)
	old := f.file(pc, "Creator/Orc/orc.stl", "orc")
	f.file(pc, "Creator/Orc/orc_base.stl", "orc-base")
	f.apply(pc)
	orc := f.modelOf(old)
	f.tag(orc, "painted")

	f.exec(`UPDATE location SET present = 0 WHERE root_id = ?`, pc)
	f.apply(pc)
	if f.count(`SELECT count(*) FROM location WHERE model_id = ? AND present = 0`, orc) != 2 {
		t.Fatal("missing files should stay visible while no copy is known")
	}
	n1 := f.file(nas, "Archive/Orc/orc.stl", "orc")
	f.file(nas, "Archive/Orc/orc_base.stl", "orc-base")
	f.apply(nas)
	if f.modelOf(n1) != orc {
		t.Fatalf("NAS copy not linked to the tagged model: %d", f.modelOf(n1))
	}
	f.apply(pc)
	if f.count(`SELECT count(*) FROM location WHERE model_id = ? AND root_id = ?`, orc, pc) != 0 {
		t.Error("missing files on the old root still belong to the model")
	}
}

// Files that are simply gone (no copy anywhere) stay listed as missing, so
// the loss is visible.
func TestDeletedWithoutCopyStaysMissing(t *testing.T) {
	f := newFixture(t)
	r := f.root(`C:\models`)
	old := f.file(r, "Creator/Orc/orc.stl", "orc")
	f.file(r, "Creator/Orc/orc_base.stl", "orc-base")
	f.apply(r)
	orc := f.modelOf(old)
	f.tag(orc, "painted")
	f.exec(`UPDATE location SET present = 0`)
	f.apply(r)
	if f.count(`SELECT count(*) FROM location WHERE model_id = ? AND present = 0`, orc) != 2 ||
		f.count(`SELECT count(*) FROM model_anchor WHERE model_id = ?`, orc) != 1 {
		t.Error("lost model no longer shows its missing files")
	}
}
