package scanner

import (
	"path/filepath"
	"testing"
	"time"
)

// The app's data folder is never catalogued, even when it lies below a root
// (J1, M): its thumbnails and backups would otherwise show up as models.
func TestScanSkipsDataFolder(t *testing.T) {
	e := newEnv(t)
	data := filepath.Join(e.root, "Tools", "modelkeeper", "data")
	mgr, err := NewManager(e.db, e.mgr.settings, nil, Options{EmitInterval: time.Hour, Exclude: data})
	if err != nil {
		t.Fatal(err)
	}
	e.mgr = mgr
	e.write("Creator/Orc/orc.stl", stlA)
	e.write("Tools/modelkeeper/data/thumbnails/x.jpg", []byte("jpg"))
	e.write("Tools/modelkeeper/data/backups/2026-11-12_100000_daily/thumbnails/y.png", []byte("png"))
	e.write("Tools/modelkeeper/other.stl", stlB) // next to the data folder: catalogued

	e.scan()
	expect(t, "locations", e.int(`SELECT count(*) FROM location`), 2)
	expect(t, "locations in data folder", e.int(`SELECT count(*) FROM location WHERE path LIKE 'Tools/modelkeeper/data/%'`), 0)
}
