package scanner

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestScale is opt-in: MODELKEEPER_SCALE=10000 go test -run TestScale ./internal/scanner -v
func TestScale(t *testing.T) {
	var n int
	if _, err := fmt.Sscan(os.Getenv("MODELKEEPER_SCALE"), &n); err != nil || n <= 0 {
		t.Skip("set MODELKEEPER_SCALE to run")
	}
	e := newEnv(t)
	for i := 0; i < n; i++ {
		e.write(fmt.Sprintf("Creator%02d/Collection%02d/Model%04d/part_%d.stl", i%20, i%7, i/5, i), []byte(fmt.Sprintf("solid %d\n%0512d\n", i, i)))
	}
	start := time.Now()
	p := e.scan()
	t.Logf("initial: %v, new=%d hashed=%d", time.Since(start), p.FilesNew, p.HashDone)
	start = time.Now()
	p = e.scan()
	t.Logf("rescan:  %v, new=%d changed=%d hashed=%d", time.Since(start), p.FilesNew, p.FilesChanged, p.HashDone)

	// Re-hash the same (no longer freshly written) files.
	e.db.Exec(`UPDATE location SET file_id = NULL`)
	start = time.Now()
	p = e.scan()
	t.Logf("rehash:  %v, hashed=%d", time.Since(start), p.HashDone)
}
