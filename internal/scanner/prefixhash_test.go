package scanner

import (
	"context"
	"crypto/sha256"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// Whatever order an archive library reads in, finish yields the file's hash.
func TestPrefixHasherAnyReadOrder(t *testing.T) {
	data := make([]byte, 5*prefixWindow+12345)
	rand.New(rand.NewSource(1)).Read(data)
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, data, 0o644)
	want := sha256.Sum256(data)

	patterns := map[string]func(ph *prefixHasher, r *rand.Rand){
		"none": func(*prefixHasher, *rand.Rand) {},
		"sequential small reads": func(ph *prefixHasher, _ *rand.Rand) {
			b := make([]byte, 4096)
			for off := int64(0); off < int64(len(data)); off += 4096 {
				ph.ReadAt(b, off)
			}
		},
		"tail first, then sequential (zip)": func(ph *prefixHasher, _ *rand.Rand) {
			b := make([]byte, 3000)
			ph.ReadAt(b, int64(len(data)-3000))
			for off := int64(0); off < int64(len(data))-3000; off += 3000 {
				ph.ReadAt(b, off)
			}
		},
		"random": func(ph *prefixHasher, r *rand.Rand) {
			b := make([]byte, 70000)
			for i := 0; i < 200; i++ {
				ph.ReadAt(b[:r.Intn(len(b))+1], r.Int63n(int64(len(data))))
			}
		},
	}
	for name, read := range patterns {
		f, _ := os.Open(p)
		var counted int64
		ph := newPrefixHasher(f, int64(len(data)), func(n int64) { counted += n })
		read(ph, rand.New(rand.NewSource(2)))
		got, err := ph.finish(context.Background())
		f.Close()
		if err != nil || got != want {
			t.Errorf("%s: hash mismatch (err %v)", name, err)
		}
		if name == "sequential small reads" && counted != int64(len(data)) {
			t.Errorf("%s: read %d bytes from the file, want %d (one pass)", name, counted, len(data))
		}
	}

	// Checkpoint and resume in a new hasher (as after a restart).
	f, _ := os.Open(p)
	ph := newPrefixHasher(f, int64(len(data)), nil)
	b := make([]byte, 2*prefixWindow)
	ph.ReadAt(b, 0)
	state, pos, err := ph.checkpoint()
	f.Close()
	if err != nil || pos != 2*prefixWindow {
		t.Fatalf("checkpoint: pos %d err %v", pos, err)
	}
	f, _ = os.Open(p)
	defer f.Close()
	var counted int64
	ph = newPrefixHasher(f, int64(len(data)), func(n int64) { counted += n })
	if err := ph.resume(state, pos); err != nil {
		t.Fatal(err)
	}
	got, err := ph.finish(context.Background())
	if err != nil || got != want {
		t.Fatalf("resumed hash mismatch (err %v)", err)
	}
	if counted != int64(len(data))-pos {
		t.Errorf("resume read %d bytes, want %d", counted, int64(len(data))-pos)
	}
}
