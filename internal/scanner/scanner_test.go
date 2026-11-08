package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/db"
	"modelkeeper/internal/settings"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	root string
	mgr  *Manager
	id   int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	root := filepath.Join(t.TempDir(), "models")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := catalog.AddRoot(context.Background(), conn, root)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(conn, settings.Defaults, nil, Options{EmitInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, db: conn, root: root, mgr: mgr, id: r.ID}
}

func (e *env) write(rel string, data []byte) {
	e.t.Helper()
	p := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) scan() Progress {
	e.t.Helper()
	if err := e.mgr.Start(e.id); err != nil {
		e.t.Fatal(err)
	}
	e.mgr.Wait()
	p := e.mgr.Progress()
	if p == nil || p.Running {
		e.t.Fatalf("scan did not finish: %+v", p)
	}
	return *p
}

func (e *env) int(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *env) str(q string, args ...any) string {
	e.t.Helper()
	var s sql.NullString
	if err := e.db.QueryRow(q, args...).Scan(&s); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

type zipEntry struct {
	name  string
	data  []byte
	store bool
	flags uint16
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, en := range entries {
		h := &zip.FileHeader{Name: en.name, Method: zip.Deflate, Flags: en.flags}
		if en.store {
			h.Method = zip.Store
		}
		h.Modified = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(en.data)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func expect[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

var (
	stlA = []byte("solid a\nendsolid a\n")
	stlB = []byte("solid b -- a different model\nendsolid b\n")
)

func TestScanAndRescan(t *testing.T) {
	e := newEnv(t)
	e.write("Creator/Orc/orc.stl", stlA)
	e.write("Creator/Orc/Supported/orc_copy.stl", stlA) // duplicate content
	e.write("Creator/Orc/notes.xyz", []byte("not a recognized type"))
	e.write("Creator/Orc/preview.PNG", []byte("png"))
	e.write("Creator/pack.zip", makeZip(t,
		zipEntry{name: "pack/"},
		zipEntry{name: "pack/orc.stl", data: stlA}, // same content as on disk
		zipEntry{name: "pack/b.stl", data: stlB},
		zipEntry{name: "pack/readme.xyz", data: []byte("x")},
		zipEntry{name: "pack/inner.zip", data: makeZip(t, zipEntry{name: "x.stl", data: stlB})},
	))
	bad := makeZip(t, zipEntry{name: "broken.stl", data: bytes.Repeat([]byte("A"), 4096), store: true})
	bad[bytes.Index(bad, []byte("AAAA"))+100] = 'B' // corrupt data, CRC no longer matches
	e.write("Creator/bad.zip", bad)
	e.write("Creator/enc.zip", makeZip(t, zipEntry{name: "secret.stl", data: stlA, flags: 0x1}))
	e.write("$RECYCLE.BIN/deleted.stl", stlB)

	p := e.scan()
	expect(t, "status", p.Status, StatusDone)
	expect(t, "files new", p.FilesNew, 6) // orc, orc_copy, preview, pack, bad, enc + nothing from $RECYCLE.BIN
	expect(t, "hashed", p.HashDone, 3)
	expect(t, "archives", p.ArchivesTotal, 3)
	expect(t, "archives broken", p.ArchivesBroken, 1)
	expect(t, "archives encrypted", p.ArchivesEncrypted, 1)
	expect(t, "issues", p.Issues, 2)
	expect(t, "bytes", p.BytesDone, p.BytesTotal)

	expect(t, "ext lowercased", e.str(`SELECT ext FROM location WHERE path = 'Creator/Orc/preview.PNG'`), "png")
	expect(t, "pack status", e.str(`SELECT archive_status FROM location WHERE path = 'Creator/pack.zip'`), "ok")
	expect(t, "bad status", e.str(`SELECT archive_status FROM location WHERE path = 'Creator/bad.zip'`), "broken")
	expect(t, "bad error", e.str(`SELECT archive_error FROM location WHERE path = 'Creator/bad.zip'`), "scan.archive.broken")
	expect(t, "enc status", e.str(`SELECT archive_status FROM location WHERE path = 'Creator/enc.zip'`), "encrypted")
	expect(t, "all disk files hashed", e.int(`SELECT count(*) FROM location WHERE archive_id IS NULL AND file_id IS NULL`), 0)

	// Members: orc.stl, b.stl, inner.zip (readme.xyz is not a recognized type, dirs are skipped).
	expect(t, "pack members", e.int(`SELECT count(*) FROM location m JOIN location a ON m.archive_id = a.id
		WHERE a.path = 'Creator/pack.zip'`), 3)
	expect(t, "nested archive not opened", e.str(`SELECT archive_status FROM location WHERE path = 'pack/inner.zip'`), "unsupported")
	expect(t, "member mtime", e.int(`SELECT count(*) FROM location WHERE path = 'pack/orc.stl' AND mtime = ?`,
		time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano()), 1)

	// D4: same content on disk twice and inside the archive = one file, three locations.
	expect(t, "orc locations", e.int(`SELECT count(*) FROM location WHERE file_id =
		(SELECT file_id FROM location WHERE path = 'Creator/Orc/orc.stl')`), 3)

	// F2/F3: unchanged rescan does nothing.
	p = e.scan()
	expect(t, "rescan new", p.FilesNew, 0)
	expect(t, "rescan changed", p.FilesChanged, 0)
	expect(t, "rescan to hash", p.HashTotal, 0)
	expect(t, "rescan archives", p.ArchivesTotal, 0)
	expect(t, "rescan bytes", p.BytesDone, int64(0))

	// Change one file, delete another.
	copyPath := filepath.Join(e.root, "Creator", "Orc", "Supported", "orc_copy.stl")
	fi, _ := os.Stat(copyPath)
	origMtime := fi.ModTime()
	e.write("Creator/Orc/orc.stl", stlB)
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(e.root, "Creator", "Orc", "orc.stl"), later, later)
	os.Remove(copyPath)

	p = e.scan()
	expect(t, "changed", p.FilesChanged, 1)
	expect(t, "missing", p.FilesMissing, 1)
	expect(t, "rehashed", p.HashDone, 1)
	expect(t, "copy present", e.int(`SELECT present FROM location WHERE path = 'Creator/Orc/Supported/orc_copy.stl'`), 0)
	expect(t, "orc now same as b.stl", e.int(`SELECT count(*) FROM location WHERE file_id =
		(SELECT file_id FROM location WHERE path = 'pack/b.stl') AND archive_id IS NULL`), 1)

	// The file comes back unchanged: present again, no rehash (hash kept, F6 basis).
	e.write("Creator/Orc/Supported/orc_copy.stl", stlA)
	os.Chtimes(copyPath, origMtime, origMtime)
	p = e.scan()
	expect(t, "restored present", e.int(`SELECT present FROM location WHERE path = 'Creator/Orc/Supported/orc_copy.stl'`), 1)
	expect(t, "restored not rehashed", p.HashTotal, 0)
	expect(t, "restored not missing", p.FilesMissing, 0)
}

func TestArchiveChangeReplacesMembers(t *testing.T) {
	e := newEnv(t)
	e.write("a.zip", makeZip(t, zipEntry{name: "one.stl", data: stlA}))
	e.scan()
	e.write("a.zip", makeZip(t, zipEntry{name: "two.stl", data: stlB}, zipEntry{name: "three.stl", data: stlA}))
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(e.root, "a.zip"), later, later)

	p := e.scan()
	expect(t, "changed", p.FilesChanged, 1)
	expect(t, "archives", p.ArchivesTotal, 1)
	expect(t, "members", e.int(`SELECT count(*) FROM location WHERE archive_id IS NOT NULL`), 2)
	expect(t, "old member gone", e.int(`SELECT count(*) FROM location WHERE path = 'one.stl'`), 0)
}

func TestOfflineRootKeepsEntries(t *testing.T) {
	e := newEnv(t)
	e.write("x/model.stl", stlA)
	e.scan()

	moved := e.root + "-away"
	if err := os.Rename(e.root, moved); err != nil {
		t.Fatal(err)
	}
	p := e.scan()
	expect(t, "status", p.Status, StatusOffline)
	expect(t, "error key", p.Error.Key, "scan.error.offline")
	expect(t, "root status", e.str(`SELECT status FROM root`), "offline")
	expect(t, "still present", e.int(`SELECT present FROM location WHERE path = 'x/model.stl'`), 1)

	os.Rename(moved, e.root)
	p = e.scan()
	expect(t, "back online", p.Status, StatusDone)
	expect(t, "root status", e.str(`SELECT status FROM root`), "online")
}

func TestCancelKeepsProgress(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 50; i++ {
		e.write(filepath.ToSlash(filepath.Join("m", string(rune('a'+i%26))+string(rune('a'+i/26))+".stl")), bytes.Repeat([]byte{byte(i)}, 1<<20))
	}
	if err := e.mgr.Start(e.id); err != nil {
		t.Fatal(err)
	}
	e.mgr.Cancel()
	e.mgr.Wait()
	p := e.mgr.Progress()
	if p.Status != StatusCancelled && p.Status != StatusDone {
		t.Fatalf("status = %s", p.Status)
	}
	expect(t, "nothing missing", e.int(`SELECT count(*) FROM location WHERE present = 0`), 0)
	// A following scan completes the work.
	p2 := e.scan()
	expect(t, "done", p2.Status, StatusDone)
	expect(t, "all hashed", e.int(`SELECT count(*) FROM location WHERE file_id IS NULL`), 0)
	expect(t, "run rows", e.int(`SELECT count(*) FROM scan_run WHERE status IN ('cancelled','done')`), 2)
}

func TestAlreadyRunning(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 20; i++ {
		e.write(filepath.Join("m", string(rune('a'+i))+".stl"), bytes.Repeat([]byte{byte(i)}, 1<<20))
	}
	if err := e.mgr.Start(e.id); err != nil {
		t.Fatal(err)
	}
	defer e.mgr.Wait()
	if err := e.mgr.Start(e.id); err == nil {
		// The first scan may have finished already on a very fast machine.
		if p := e.mgr.Progress(); p.Running {
			t.Fatal("second Start succeeded while running")
		}
	}
}

// 7z fixtures need the 7-Zip CLI.
func sevenZip(t *testing.T) string {
	for _, p := range []string{`C:\Program Files\7-Zip\7z.exe`, "7z", "7zz"} {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	t.Skip("7-Zip not installed")
	return ""
}

func TestSevenZip(t *testing.T) {
	bin := sevenZip(t)
	e := newEnv(t)
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.stl"), stlA, 0o644)
	os.WriteFile(filepath.Join(src, "big.stl"), bytes.Repeat([]byte("B"), 8192), 0o644)
	run := func(args ...string) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("7z %v: %v\n%s", args, err, out)
		}
	}
	dst := func(n string) string { return filepath.Join(e.root, n) }
	run("a", "-bd", dst("good.7z"), "a.stl", "big.stl")
	run("a", "-bd", "-mx0", dst("bad.7z"), "big.stl") // stored, so corruption is only visible via CRC
	run("a", "-bd", "-pSecret", "-mhe=on", dst("enc.7z"), "a.stl")

	b, _ := os.ReadFile(dst("bad.7z"))
	i := bytes.Index(b, []byte("BBBB"))
	if i < 0 {
		t.Fatal("stored data not found")
	}
	b[i+10] = 'C'
	os.WriteFile(dst("bad.7z"), b, 0o644)

	p := e.scan()
	expect(t, "archives", p.ArchivesTotal, 3)
	expect(t, "good", e.str(`SELECT archive_status FROM location WHERE path = 'good.7z'`), "ok")
	expect(t, "good members", e.int(`SELECT count(*) FROM location m JOIN location a ON m.archive_id = a.id WHERE a.path = 'good.7z'`), 2)
	expect(t, "bad", e.str(`SELECT archive_status FROM location WHERE path = 'bad.7z'`), "broken")
	expect(t, "enc", e.str(`SELECT archive_status FROM location WHERE path = 'enc.7z'`), "encrypted")
}

func TestRootStatsAndIssues(t *testing.T) {
	e := newEnv(t)
	e.write("a.stl", stlA)
	e.write("dup/a.stl", stlA)
	bad := makeZip(t, zipEntry{name: "x.stl", data: bytes.Repeat([]byte("A"), 4096), store: true})
	bad[bytes.Index(bad, []byte("AAAA"))+100] = 'B'
	e.write("bad.zip", bad)
	e.write("ok.zip", makeZip(t, zipEntry{name: "m.stl", data: stlB}))
	e.scan()

	ctx := context.Background()
	s, err := catalog.GetRootStats(ctx, e.db, e.id)
	if err != nil {
		t.Fatal(err)
	}
	want := catalog.RootStats{Files: 4, Bytes: s.Bytes, Archives: 2, ArchivesBroken: 1, ArchiveMembers: 1, DuplicateFiles: 1}
	if s != want {
		t.Errorf("stats = %+v, want %+v", s, want)
	}
	issues, err := catalog.ListLastScanIssues(ctx, e.db, e.id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Kind != "archive_broken" || issues[0].Msg.Params["path"] != "bad.zip" {
		t.Errorf("issues = %+v", issues)
	}
}
