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
