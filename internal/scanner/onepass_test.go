package scanner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"modelkeeper/internal/settings"
)

// randomBytes returns incompressible data, so archives have realistic sizes.
func randomBytes(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// archiveFixtures writes zips (and 7z files, if 7-Zip is installed) covering
// intact, damaged, encrypted, truncated and nested archives into dir.
func archiveFixtures(t *testing.T, dir string) {
	t.Helper()
	write := func(name string, data []byte) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("good.zip", makeZip(t,
		zipEntry{name: "Orc/"},
		zipEntry{name: "Orc/body.stl", data: randomBytes(1, 300_000)},
		zipEntry{name: "Orc/base.stl", data: bytes.Repeat([]byte("base "), 50_000)},
		zipEntry{name: "Orc/stored.stl", data: randomBytes(2, 200_000), store: true},
		zipEntry{name: "Orc/readme.txt", data: []byte("hello")},
		zipEntry{name: "Orc/inner.zip", data: makeZip(t, zipEntry{name: "x.stl", data: stlB})},
	))
	bad := makeZip(t, zipEntry{name: "a.stl", data: stlA}, zipEntry{name: "broken.stl", data: bytes.Repeat([]byte("A"), 70_000), store: true},
		zipEntry{name: "c.stl", data: stlB})
	bad[bytes.Index(bad, []byte("AAAA"))+5000] = 'B'
	write("bad.zip", bad)
	write("enc.zip", makeZip(t, zipEntry{name: "a.stl", data: stlA}, zipEntry{name: "secret.stl", data: stlB, flags: 0x1}))
	full := makeZip(t, zipEntry{name: "a.stl", data: randomBytes(3, 100_000)})
	write("truncated.zip", full[:len(full)-30])
	write("notazip.zip", randomBytes(4, 5000))

	bin := ""
	for _, p := range []string{"7z", "7zz"} {
		if path, err := exec.LookPath(p); err == nil {
			bin = path
		}
	}
	if bin == "" {
		t.Log("7-Zip not installed: 7z fixtures skipped")
		return
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.stl"), randomBytes(5, 250_000), 0o644)
	os.WriteFile(filepath.Join(src, "big.stl"), bytes.Repeat([]byte("B"), 90_000), 0o644)
	run := func(args ...string) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("7z %v: %v\n%s", args, err, out)
		}
	}
	run("a", "-bd", filepath.Join(dir, "good.7z"), "a.stl", "big.stl")
	run("a", "-bd", "-ms=off", filepath.Join(dir, "nonsolid.7z"), "a.stl", "big.stl")
	run("a", "-bd", "-mx0", filepath.Join(dir, "bad.7z"), "big.stl")
	run("a", "-bd", "-pSecret", "-mhe=on", filepath.Join(dir, "enc.7z"), "a.stl")
	b, _ := os.ReadFile(filepath.Join(dir, "bad.7z"))
	b[bytes.Index(b, []byte("BBBB"))+10] = 'C'
	os.WriteFile(filepath.Join(dir, "bad.7z"), b, 0o644)
}

// archiveState describes what the catalog knows about each archive.
func (e *env) archiveState() map[string]string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT a.path, coalesce(a.archive_status, ''), coalesce(a.archive_error, ''), hex(f.sha256),
		coalesce((SELECT group_concat(m.path || ':' || m.size || ':' || hex(mf.sha256), ',') FROM
			(SELECT * FROM location WHERE archive_id = a.id ORDER BY path) m JOIN file mf ON mf.id = m.file_id), '')
		FROM location a LEFT JOIN file f ON f.id = a.file_id WHERE a.category = 'archive' AND a.archive_id IS NULL`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, st, ek, sum, members string
		rows.Scan(&p, &st, &ek, &sum, &members)
		out[p] = fmt.Sprintf("%s %s hash=%s members=[%s]", st, ek, sum, members)
	}
	return out
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(from, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(to, rel), b, 0o644)
	})
}

// The one-pass reader gives exactly the same catalog as reading into memory,
// and the archive hash is the SHA-256 of the file.
func TestOnePassMatchesMemoryPath(t *testing.T) {
	fixtures := t.TempDir()
	archiveFixtures(t, fixtures)

	mem := newEnv(t)
	copyTree(t, fixtures, mem.root)
	mem.scan()

	one := newEnv(t)
	copyTree(t, fixtures, one.root)
	one.mgr.opts.ArchiveMemLimit, one.mgr.opts.ResumeMin = 1, 1 // every archive takes the one-pass path
	one.scan()

	a, b := mem.archiveState(), one.archiveState()
	var names []string
	for n := range a {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(a) < 5 || len(a) != len(b) {
		t.Fatalf("archives: memory %d, one-pass %d", len(a), len(b))
	}
	for _, n := range names {
		if a[n] != b[n] {
			t.Errorf("%s:\n memory:   %s\n one-pass: %s", n, a[n], b[n])
		}
		data, _ := os.ReadFile(filepath.Join(fixtures, n))
		want := sha256.Sum256(data)
		if !strings.Contains(b[n], "hash="+strings.ToUpper(hex.EncodeToString(want[:]))) {
			t.Errorf("%s: archive hash is not the file's SHA-256: %s", n, b[n])
		}
	}
	t.Logf("compared %d archives: %v", len(names), names)
}

// bigZip returns a zip with many incompressible entries (realistic for model packs).
func bigZip(t *testing.T, entries, size int) []byte {
	var list []zipEntry
	for i := 0; i < entries; i++ {
		list = append(list, zipEntry{name: fmt.Sprintf("Pack/part%03d.stl", i), data: randomBytes(int64(100+i), size)})
	}
	return makeZip(t, list...)
}

// resumeEnv cancels the scan at the n-th checkpoint.
func resumeEnv(t *testing.T, cancelAt int) (*env, *int) {
	e := newEnv(t)
	saved := 0
	mgr, err := NewManager(e.db, settings.Defaults, nil, Options{
		EmitInterval: time.Hour, ResumeMin: 1, CheckpointEvery: time.Nanosecond, HashWorkers: 1,
		onCheckpoint: func() {
			saved++
			if saved == cancelAt {
				e.mgr.Cancel()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.mgr = mgr
	return e, &saved
}

func TestZipResumesAfterCancel(t *testing.T) {
	zipData := bigZip(t, 30, 200_000)
	e, saved := resumeEnv(t, 12)
	e.write("Pack.zip", zipData)
	p := e.scan()
	expect(t, "first scan cancelled", p.Status, StatusCancelled)
	offset := e.int(`SELECT offset FROM hash_progress`)
	if offset <= 0 || offset >= len(zipData) {
		t.Fatalf("checkpoint offset %d of %d", offset, len(zipData))
	}

	*saved = -1000 // no more cancelling
	p = e.scan()
	expect(t, "resumed", p.Status, StatusDone)
	if p.BytesDone >= int64(len(zipData)) {
		t.Errorf("resume read %d bytes, the whole archive is %d", p.BytesDone, len(zipData))
	}
	expect(t, "checkpoint removed", e.int(`SELECT count(*) FROM hash_progress`), 0)

	// Same catalog as an uninterrupted scan.
	ref := newEnv(t)
	ref.write("Pack.zip", zipData)
	ref.scan()
	if got, want := e.archiveState()["Pack.zip"], ref.archiveState()["Pack.zip"]; got != want {
		t.Errorf("resumed result differs:\n got  %s\n want %s", got, want)
	}
	t.Logf("cancelled at %d of %d bytes; resume read %d bytes", offset, len(zipData), p.BytesDone)
}

func TestLargeFileResumesAfterCancel(t *testing.T) {
	data := randomBytes(7, 12<<20)
	e, saved := resumeEnv(t, 5)
	e.write("Orc/orc.stl", data)
	p := e.scan()
	expect(t, "first scan cancelled", p.Status, StatusCancelled)
	offset := e.int(`SELECT offset FROM hash_progress`)
	if offset <= 0 || offset >= len(data) {
		t.Fatalf("checkpoint offset %d of %d", offset, len(data))
	}
	*saved = -1000
	p = e.scan()
	expect(t, "resumed", p.Status, StatusDone)
	expect(t, "read only the rest", p.BytesDone, int64(len(data)-offset))
	want := sha256.Sum256(data)
	expect(t, "hash", e.str(`SELECT hex(f.sha256) FROM location l JOIN file f ON f.id = l.file_id WHERE l.path = 'Orc/orc.stl'`),
		strings.ToUpper(hex.EncodeToString(want[:])))
	expect(t, "checkpoint removed", e.int(`SELECT count(*) FROM hash_progress`), 0)
}

// A file changed after the interruption is read from the start.
func TestChangedFileIgnoresCheckpoint(t *testing.T) {
	data := randomBytes(8, 12<<20)
	e, saved := resumeEnv(t, 5)
	e.write("Orc/orc.stl", data)
	e.scan()
	if e.int(`SELECT count(*) FROM hash_progress`) != 1 {
		t.Fatal("no checkpoint")
	}
	changed := randomBytes(9, 12<<20) // same size, new content and mtime
	e.write("Orc/orc.stl", changed)
	future := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(e.root, "Orc", "orc.stl"), future, future)
	*saved = -1000
	p := e.scan()
	expect(t, "read completely", p.BytesDone, int64(len(changed)))
	want := sha256.Sum256(changed)
	expect(t, "hash of the new content", e.str(`SELECT hex(f.sha256) FROM location l JOIN file f ON f.id = l.file_id
		WHERE l.path = 'Orc/orc.stl'`), strings.ToUpper(hex.EncodeToString(want[:])))
}

// A full check of an unchanged large zip hashes it once and skips the members.
func TestFullCheckLargeZip(t *testing.T) {
	zipData := bigZip(t, 10, 100_000)
	e := newEnv(t)
	e.mgr.opts.ArchiveMemLimit, e.mgr.opts.ResumeMin = 1, 1
	e.write("Pack.zip", zipData)
	e.scan()
	before := e.archiveState()["Pack.zip"]
	p := e.fullCheck()
	expect(t, "nothing changed", p.HashChanged, 0)
	if p.BytesDone != int64(len(zipData)) {
		t.Errorf("full check read %d bytes, want one pass of %d", p.BytesDone, len(zipData))
	}
	expect(t, "same state", e.archiveState()["Pack.zip"], before)
}
