package scanner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modelkeeper/internal/catalog"
)

func (e *env) fullCheck() Progress {
	e.t.Helper()
	if err := e.mgr.StartFull(e.id); err != nil {
		e.t.Fatal(err)
	}
	e.mgr.Wait()
	p := e.mgr.Progress()
	if p == nil || p.Running {
		e.t.Fatalf("full check did not finish: %+v", p)
	}
	return *p
}

// overwrite replaces a file's content but keeps its size and modification
// time, like silent corruption on disk.
func (e *env) overwrite(rel string, data []byte) {
	e.t.Helper()
	p := filepath.Join(e.root, filepath.FromSlash(rel))
	fi, err := os.Stat(p)
	if err != nil {
		e.t.Fatal(err)
	}
	if int64(len(data)) != fi.Size() {
		e.t.Fatalf("overwrite %s: size %d, want %d", rel, len(data), fi.Size())
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		e.t.Fatal(err)
	}
}

func TestFullCheckFindsSilentChanges(t *testing.T) {
	e := newEnv(t)
	body := bytes.Repeat([]byte("A"), 4096)
	e.write("Orc/orc.stl", stlA)
	e.write("Orc/other.stl", stlB)
	e.write("Orc/pack.zip", makeZip(t, zipEntry{name: "body.stl", data: body, store: true}))
	e.scan()
	packMember := e.int(`SELECT id FROM location WHERE path = 'body.stl'`)
	orcFile := e.int(`SELECT file_id FROM location WHERE path = 'Orc/orc.stl'`)

	// Unchanged: the full check reads everything but reports nothing and keeps the members.
	p := e.fullCheck()
	expect(t, "status", p.Status, StatusDone)
	expect(t, "mode", p.Mode, ModeFull)
	expect(t, "rehashed", p.HashDone, 2)
	expect(t, "archives read", p.ArchivesTotal, 1)
	expect(t, "nothing changed", p.HashChanged, 0)
	expect(t, "no issues", p.Issues, 0)
	expect(t, "run mode", e.str(`SELECT mode FROM scan_run ORDER BY id DESC LIMIT 1`), ModeFull)
	expect(t, "members kept", e.int(`SELECT id FROM location WHERE path = 'body.stl'`), packMember)

	// Silent changes at unchanged size and mtime.
	bad := bytes.Clone(stlA)
	bad[len(bad)-1] ^= 0xff
	e.overwrite("Orc/orc.stl", bad)
	zipData, _ := os.ReadFile(filepath.Join(e.root, "Orc", "pack.zip"))
	zipData[bytes.Index(zipData, []byte("AAAA"))+100] = 'B' // member CRC no longer matches
	e.overwrite("Orc/pack.zip", zipData)

	// An incremental scan cannot see them (F2)...
	p = e.scan()
	expect(t, "incremental: nothing hashed", p.HashDone, 0)
	expect(t, "incremental: nothing changed", p.HashChanged, 0)

	// ...the full check does (F8, F9).
	p = e.fullCheck()
	expect(t, "hash changed", p.HashChanged, 2)
	expect(t, "issues", p.Issues, 3) // 2x hash_changed + archive broken
	expect(t, "hash_changed issues", e.int(`SELECT count(*) FROM scan_issue WHERE kind = 'hash_changed'
		AND scan_run_id = (SELECT max(id) FROM scan_run)`), 2)
	expect(t, "issue key", e.str(`SELECT msg_key FROM scan_issue WHERE kind = 'hash_changed' LIMIT 1`), "scan.issue.hashChanged")
	expect(t, "archive now broken", e.str(`SELECT archive_status FROM location WHERE path = 'Orc/pack.zip'`), "broken")
	if e.int(`SELECT file_id FROM location WHERE path = 'Orc/orc.stl'`) == orcFile {
		t.Error("catalog still has the old content of orc.stl")
	}

	// The report stays listed after later incremental scans (e.g. automatic ones).
	e.scan()
	issues, err := catalog.ListLastScanIssues(context.Background(), e.db, e.id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Kind != "hash_changed" || issues[1].Kind != "hash_changed" {
		t.Errorf("issues after incremental scan: %+v", issues)
	}

	// Reported once: the next full check finds the stored state, and the list is
	// clear. (The archive, unchanged since, is not read again; it stays counted
	// as broken in the root statistics.)
	p = e.fullCheck()
	expect(t, "reported once", p.HashChanged, 0)
	expect(t, "still broken", e.str(`SELECT archive_status FROM location WHERE path = 'Orc/pack.zip'`), "broken")
	issues, _ = catalog.ListLastScanIssues(context.Background(), e.db, e.id, 10)
	if len(issues) != 0 {
		t.Errorf("issues after clean full check: %+v", issues)
	}
}

func TestFullCheckCancelKeepsCatalog(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 50; i++ {
		e.write(filepath.Join("Orc", string(rune('a'+i%26))+string(rune('a'+i/26))+".stl"), bytes.Repeat([]byte{byte(i)}, 1<<16))
	}
	e.scan()
	hashed := e.int(`SELECT count(*) FROM location WHERE file_id IS NOT NULL`)
	if err := e.mgr.StartFull(e.id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	e.mgr.Cancel()
	e.mgr.Wait()
	expect(t, "still hashed", e.int(`SELECT count(*) FROM location WHERE file_id IS NOT NULL`), hashed)
}
