package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/db"
	"modelkeeper/internal/scanner"
	"modelkeeper/internal/settings"
)

// A file copied into a watched root ends up in the catalog without a manual scan.
func TestE2EWithScanner(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "models")
	os.MkdirAll(filepath.Join(rootDir, "Creator", "Orc"), 0o755)
	os.WriteFile(filepath.Join(rootDir, "Creator", "Orc", "orc.stl"), []byte("solid orc"), 0o644)

	conn, err := db.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	st := settings.Defaults()
	mgr, err := scanner.NewManager(conn, func() settings.Settings { return st }, nil, scanner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	root, err := catalog.AddRoot(context.Background(), conn, rootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(root.ID); err != nil { // the initial scan stays manual
		t.Fatal(err)
	}
	mgr.Wait()

	w, err := New(conn, mgr, Options{Quiet: 200 * time.Millisecond, Relevant: func(n string) bool {
		return st.ExtCategory(filepath.Ext(n)) != ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !w.Live(root.ID) {
		t.Fatal("root not live")
	}

	os.MkdirAll(filepath.Join(rootDir, "Creator", "Elf"), 0o755)
	os.WriteFile(filepath.Join(rootDir, "Creator", "Elf", "elf.stl"), []byte("solid elf"), 0o644)
	os.Remove(filepath.Join(rootDir, "Creator", "Orc", "orc.stl"))

	count := func(q string) int {
		var n int
		conn.QueryRow(q).Scan(&n)
		return n
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if count(`SELECT count(*) FROM location WHERE path = 'Creator/Elf/elf.stl' AND present = 1`) == 1 &&
			count(`SELECT count(*) FROM location WHERE path = 'Creator/Orc/orc.stl' AND present = 0`) == 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("catalog not updated: elf present=%d, orc missing=%d",
		count(`SELECT count(*) FROM location WHERE path = 'Creator/Elf/elf.stl' AND present = 1`),
		count(`SELECT count(*) FROM location WHERE path = 'Creator/Orc/orc.stl' AND present = 0`))
}
