package scanner

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/settings"
	"modelkeeper/internal/thumbs"
)

func triangleSTL() []byte {
	var buf bytes.Buffer
	buf.Write(make([]byte, 80))
	binary.Write(&buf, binary.LittleEndian, uint32(1))
	binary.Write(&buf, binary.LittleEndian, [12]float32{0, 0, 1, 0, 0, 0, 10, 0, 0, 0, 10, 10})
	buf.Write([]byte{0, 0})
	return buf.Bytes()
}

func TestScanCreatesThumbnails(t *testing.T) {
	e := newEnv(t)
	thumbDir := t.TempDir()
	mgr, err := NewManager(e.db, settings.Defaults, nil, Options{EmitInterval: time.Hour, ThumbDir: thumbDir})
	if err != nil {
		t.Fatal(err)
	}
	e.mgr = mgr
	e.write("Creator/Orc/orc.stl", triangleSTL())
	e.write("Creator/Elf/readme.txt", []byte("no model files")) // not a model at all
	p := e.scan()
	expect(t, "status", p.Status, StatusDone)
	expect(t, "thumbs total", p.ThumbsTotal, 1)
	expect(t, "thumbs done", p.ThumbsDone, 1)

	page, err := catalog.SearchModels(context.Background(), e.db, catalog.ModelQuery{Text: "orc"}, 0, 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("search: %v %+v", err, page)
	}
	var file string
	e.db.QueryRow(`SELECT thumb_file FROM model WHERE id = ?`, page.Items[0].ID).Scan(&file)
	if _, err := os.Stat(filepath.Join(thumbDir, file)); err != nil {
		t.Errorf("thumbnail file: %v", err)
	}
	// Rescan: nothing left to do.
	p = e.scan()
	expect(t, "rescan thumbs", p.ThumbsTotal, 0)
}

// After restoring a snapshot (which holds only the manual thumbnails, J2),
// generated thumbnails whose file is gone are generated again; a manual
// one that is gone stays manual.
func TestMissingGeneratedThumbnailsAreRegenerated(t *testing.T) {
	e := newEnv(t)
	thumbDir := t.TempDir()
	mgr, err := NewManager(e.db, settings.Defaults, nil, Options{EmitInterval: time.Hour, ThumbDir: thumbDir})
	if err != nil {
		t.Fatal(err)
	}
	e.mgr = mgr
	e.write("Creator/Orc/orc.stl", triangleSTL())
	e.write("Creator/Elf/elf.stl", triangleSTL())
	e.scan()
	orc := e.int(`SELECT model_id FROM location WHERE path = 'Creator/Orc/orc.stl'`)
	elf := e.int(`SELECT model_id FROM location WHERE path = 'Creator/Elf/elf.stl'`)
	e.db.Exec(`UPDATE model SET thumb_source = 'manual' WHERE id = ?`, elf)
	for _, f := range []string{e.str(`SELECT thumb_file FROM model WHERE id = ?`, orc), e.str(`SELECT thumb_file FROM model WHERE id = ?`, elf)} {
		if err := os.Remove(filepath.Join(thumbDir, f)); err != nil {
			t.Fatal(err)
		}
	}

	reset, lost, err := thumbs.ForgetMissing(context.Background(), e.db, thumbDir)
	if err != nil || reset != 1 || lost != 1 {
		t.Fatalf("ForgetMissing = %d, %d, %v; want 1 reset, 1 manual lost", reset, lost, err)
	}
	expect(t, "manual kept", e.str(`SELECT thumb_source FROM model WHERE id = ?`, elf), "manual")

	if err := e.mgr.Redetect(e.id); err != nil { // what the app runs at start (detectExisting)
		t.Fatal(err)
	}
	e.mgr.Wait()
	file := e.str(`SELECT thumb_file FROM model WHERE id = ?`, orc)
	if _, err := os.Stat(filepath.Join(thumbDir, file)); file == "" || err != nil {
		t.Errorf("thumbnail not generated again: %q, %v", file, err)
	}
	if reset, _, _ := thumbs.ForgetMissing(context.Background(), e.db, thumbDir); reset != 0 {
		t.Errorf("second ForgetMissing reset %d", reset)
	}
}
