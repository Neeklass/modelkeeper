package scanner

import (
	"context"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/settings"
)

// After adding a file type, the next incremental scan catalogs files of that
// type on disk and inside unchanged archives (F10).
func TestAddedFileTypeIsPickedUp(t *testing.T) {
	e := newEnv(t)
	st := settings.Defaults()
	mgr, err := NewManager(e.db, func() settings.Settings { return st }, nil, Options{EmitInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	e.mgr = mgr
	e.write("Orc/orc.stl", stlA)
	e.write("Orc/orc.lychee", []byte("lychee project"))
	e.write("Orc/pack.zip", makeZip(t, zipEntry{name: "elf.stl", data: stlB}, zipEntry{name: "elf.lychee", data: []byte("x")}))
	e.scan()
	expect(t, "unknown type ignored", e.int(`SELECT count(*) FROM location WHERE ext = 'lychee'`), 0)

	prev := st.FileTypes
	st.FileTypes.Slicer = append(append([]string{}, st.FileTypes.Slicer...), "lychee")
	ch, err := catalog.ApplyFileTypes(context.Background(), e.db, st.ExtCategory, len(st.FileTypes.Added(prev)) > 0)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "archives pending", ch.ArchivesPending, 1)

	p := e.scan()
	expect(t, "status", p.Status, StatusDone)
	expect(t, "archive read again", p.ArchivesTotal, 1)
	expect(t, "on disk and in archive", e.int(`SELECT count(*) FROM location WHERE ext = 'lychee' AND category = 'slicer'`), 2)
	expect(t, "archive ok", e.str(`SELECT archive_status FROM location WHERE path = 'Orc/pack.zip'`), "ok")
}
