package hashshare_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
	"modelkeeper/internal/db"
	"modelkeeper/internal/hashshare"
	"modelkeeper/internal/scanner"
	"modelkeeper/internal/settings"
)

func rnd(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func makeZip(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, _ := w.Create(name)
		f.Write(data)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// nas creates the shared folder both users scan.
func nas(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "3D Models")
	write := func(rel string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o644)
	}
	write("Atlas/Orc/orc.stl", rnd(1, 200_000))
	write("Atlas/Orc/orc_base.stl", rnd(2, 50_000))
	write("Atlas/Orc/render.jpg", rnd(3, 10_000))
	write("Atlas/Elf/elf.lys", rnd(4, 80_000))
	write("Bastet/Goblin.zip", makeZip(t, map[string][]byte{"goblin.stl": rnd(5, 100_000), "readme.txt": []byte("hi")}))
	bad := makeZip(t, map[string][]byte{"x.stl": bytes.Repeat([]byte("A"), 50_000)})
	bad[len(bad)/3] ^= 0xff
	write("Bastet/Broken.zip", bad)
	return dir
}

type user struct {
	t    *testing.T
	db   *sql.DB
	mgr  *scanner.Manager
	root int64
	st   settings.Settings
}

func newUser(t *testing.T, rootPath string) *user {
	conn, err := db.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	u := &user{t: t, db: conn, st: settings.Defaults()}
	u.mgr, err = scanner.NewManager(conn, func() settings.Settings { return u.st }, nil, scanner.Options{EmitInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	r, err := catalog.AddRoot(context.Background(), conn, rootPath, "")
	if err != nil {
		t.Fatal(err)
	}
	u.root = r.ID
	return u
}

func (u *user) exts() []string {
	ft := u.st.FileTypes
	return append(append(append(append([]string{}, ft.Model...), ft.Slicer...), ft.Archive...), ft.Supplementary...)
}

func (u *user) scan(full bool) scanner.Progress {
	u.t.Helper()
	start := u.mgr.Start
	if full {
		start = u.mgr.StartFull
	}
	if err := start(u.root); err != nil {
		u.t.Fatal(err)
	}
	u.mgr.Wait()
	return *u.mgr.Progress()
}

func (u *user) export(file string) hashshare.ExportResult {
	u.t.Helper()
	res, err := hashshare.ExportFile(context.Background(), u.db, u.root, u.exts(), file)
	if err != nil {
		u.t.Fatal(err)
	}
	return res
}

func (u *user) importFile(rootPath, file string) (hashshare.ImportResult, error) {
	return hashshare.ImportFile(context.Background(), u.db, u.root, rootPath, u.exts(), file)
}

// state lists every location below the root with hash and archive status.
func (u *user) state(prefix string) string {
	rows, err := u.db.Query(`SELECT coalesce(a.path || ' › ', '') || l.path, l.size, coalesce(hex(f.sha256), '-'),
			coalesce(l.archive_status, ''), coalesce(l.archive_error, '')
		FROM location l LEFT JOIN location a ON a.id = l.archive_id LEFT JOIN file f ON f.id = l.file_id
		WHERE l.root_id = ? ORDER BY 1`, u.root)
	if err != nil {
		u.t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var p, sum, st, ek string
		var size int64
		rows.Scan(&p, &size, &sum, &st, &ek)
		fmt.Fprintf(&b, "%s %d %s %s %s\n", strings.TrimPrefix(p, prefix), size, sum, st, ek)
	}
	return b.String()
}

func TestShareHashesSameNAS(t *testing.T) {
	dir := nas(t)
	a := newUser(t, dir)
	a.scan(false)
	catalog.AddTags(context.Background(), a.db, []int64{1}, []string{"painted"})
	file := filepath.Join(t.TempDir(), "nas.mkhashes")
	ex := a.export(file)
	if ex.Files != 6 || ex.Archives != 2 || ex.Unhashed != 0 {
		t.Fatalf("export: %+v", ex)
	}

	b := newUser(t, dir)
	im, err := b.importFile(dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if im.Files != 6 || im.Archives != 2 || im.Prefix != "" || im.Matching == 0 {
		t.Fatalf("import: %+v", im)
	}
	p := b.scan(false)
	if p.Status != scanner.StatusDone || p.HashDone != 0 || p.ArchivesTotal != 0 || p.Imported != 6 || p.BytesDone != 0 {
		t.Fatalf("scan with imported hashes read files: %+v", p)
	}
	if p.ArchivesBroken != 1 {
		t.Errorf("broken archive not reported in the importer's scan")
	}
	if got, want := b.state(""), a.state(""); got != want {
		t.Errorf("catalog differs from the exporter's:\n got\n%s\n want\n%s", got, want)
	}
	var tags int
	b.db.QueryRow(`SELECT count(*) FROM model_tag`).Scan(&tags)
	if tags != 0 {
		t.Error("user data (tags) was shared")
	}
	var left int
	b.db.QueryRow(`SELECT count(*) FROM hash_import`).Scan(&left)
	if left != 0 {
		t.Error("imported hashes not removed after the scan")
	}
	// The importer's full check reads everything and finds the same.
	if p := b.scan(true); p.HashChanged != 0 || p.Imported != 0 || p.HashDone != 4 {
		t.Errorf("full check after import: %+v", p)
	}
}

// Files changed after the export are hashed by the importer itself.
func TestChangedAfterExportIsHashed(t *testing.T) {
	dir := nas(t)
	a := newUser(t, dir)
	a.scan(false)
	file := filepath.Join(t.TempDir(), "nas.mkhashes")
	a.export(file)

	orc := filepath.Join(dir, "Atlas", "Orc", "orc.stl")
	os.WriteFile(orc, rnd(99, 200_001), 0o644) // changed: size and mtime
	b := newUser(t, dir)
	if _, err := b.importFile(dir, file); err != nil {
		t.Fatal(err)
	}
	p := b.scan(false)
	if p.HashDone != 1 || p.Imported != 5 {
		t.Fatalf("hashed %d, imported %d; want 1 and 5", p.HashDone, p.Imported)
	}
	ref := newUser(t, dir)
	ref.scan(false)
	if b.state("") != ref.state("") {
		t.Error("result differs from a scan without import")
	}
}

// The other user's root may sit one level above or below.
func TestFolderMapping(t *testing.T) {
	dir := nas(t)
	a := newUser(t, dir)
	a.scan(false)
	file := filepath.Join(t.TempDir(), "nas.mkhashes")
	a.export(file)

	parent := filepath.Dir(dir) // B scans the folder above
	b := newUser(t, parent)
	im, err := b.importFile(parent, file)
	if err != nil {
		t.Fatal(err)
	}
	if im.Prefix != "+3D Models" {
		t.Fatalf("mapping %q", im.Prefix)
	}
	if p := b.scan(false); p.HashDone != 0 || p.Imported != 6 {
		t.Fatalf("parent root: %+v", p)
	}

	sub := filepath.Join(dir, "Atlas") // C scans a subfolder
	c := newUser(t, sub)
	im, err = c.importFile(sub, file)
	if err != nil {
		t.Fatal(err)
	}
	if im.Prefix != "-Atlas" || im.Files != 4 {
		t.Fatalf("subfolder: %+v", im)
	}
	if p := c.scan(false); p.HashDone != 0 || p.Imported != 4 {
		t.Fatalf("subfolder root: %+v", p)
	}
}

// If the importer recognizes file types the exporter did not, archives are
// read again (their members of those types are not in the file).
func TestMoreFileTypesRereadArchives(t *testing.T) {
	dir := nas(t)
	a := newUser(t, dir)
	a.scan(false)
	file := filepath.Join(t.TempDir(), "nas.mkhashes")
	a.export(file)
	b := newUser(t, dir)
	b.st.FileTypes.Supplementary = append(b.st.FileTypes.Supplementary, "nfo")
	im, err := b.importFile(dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if im.ArchivesReread != 2 || im.Archives != 0 {
		t.Fatalf("import: %+v", im)
	}
	if p := b.scan(false); p.ArchivesTotal != 2 || p.HashDone != 0 {
		t.Fatalf("scan: %+v", p)
	}
}

func TestRejectsWrongFiles(t *testing.T) {
	dir := nas(t)
	a := newUser(t, dir)
	a.scan(false)
	good := filepath.Join(t.TempDir(), "nas.mkhashes")
	a.export(good)
	b := newUser(t, dir)

	junk := filepath.Join(t.TempDir(), "junk.mkhashes")
	os.WriteFile(junk, []byte("not gzip"), 0o644)
	if _, err := b.importFile(dir, junk); !errors.Is(err, hashshare.ErrNotAnExport) {
		t.Errorf("junk: %v", err)
	}

	// A tampered entry that escapes the root is refused, nothing is stored.
	evil := filepath.Join(t.TempDir(), "evil.mkhashes")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	fmt.Fprintf(zw, `{"format":"%s","version":1,"exts":["stl"]}`+"\n", hashshare.Format)
	fmt.Fprintf(zw, `{"p":"../../etc/passwd","s":1,"m":1,"h":"%s"}`+"\n", strings.Repeat("ab", 32))
	zw.Close()
	os.WriteFile(evil, buf.Bytes(), 0o644)
	if _, err := b.importFile(dir, evil); !errors.Is(err, hashshare.ErrInvalid) {
		t.Errorf("tampered: %v", err)
	}
	var n int
	b.db.QueryRow(`SELECT count(*) FROM hash_import`).Scan(&n)
	if n != 0 {
		t.Error("entries stored from an invalid file")
	}

	// A file of another folder does not match.
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "x.stl"), []byte("x"), 0o644)
	c := newUser(t, other)
	if _, err := c.importFile(other, good); !errors.Is(err, hashshare.ErrNoMatch) {
		t.Errorf("other folder: %v", err)
	}

	// Newer format.
	newer := filepath.Join(t.TempDir(), "newer.mkhashes")
	buf.Reset()
	zw = gzip.NewWriter(&buf)
	fmt.Fprintf(zw, `{"format":"%s","version":99}`+"\n", hashshare.Format)
	zw.Close()
	os.WriteFile(newer, buf.Bytes(), 0o644)
	if _, err := b.importFile(dir, newer); !errors.Is(err, hashshare.ErrNewer) {
		t.Errorf("newer: %v", err)
	}
}
