package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// While a root is watched, a model folder with subfolders can still be renamed
// and moved (found in the release test on Windows: one watch per folder
// blocked that), and the change is reported.
func TestWatchedFoldersCanBeRenamed(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "Creator", "Orc")
	for _, d := range []string{"Supported", "Unsupported"} {
		if err := os.MkdirAll(filepath.Join(model, d), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(model, d, "orc.stl"), []byte("solid"), 0o644)
	}
	b, err := newBackend()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Recursive() {
		if err := b.Add(root); err != nil {
			t.Fatal(err)
		}
	} else {
		filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.IsDir() {
				b.Add(p)
			}
			return nil
		})
	}
	time.Sleep(100 * time.Millisecond)
	renamed := filepath.Join(root, "Creator", "Orc (2024)")
	if err := os.Rename(model, renamed); err != nil {
		t.Fatalf("rename while watched: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(renamed, filepath.Join(root, "Other", "Orc")); err != nil {
		t.Fatalf("move while watched: %v", err)
	}
	select {
	case ev := <-b.Events():
		t.Logf("event %v", ev)
	case err := <-b.Errors():
		t.Fatalf("error %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	// Closing returns (no hang on a pending read).
	done := make(chan struct{})
	go func() { b.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hangs")
	}
}
