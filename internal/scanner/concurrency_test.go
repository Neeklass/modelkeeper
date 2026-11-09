package scanner

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"modelkeeper/internal/catalog"
)

// The UI reads (stats, roots, progress) while a scan writes. Neither side may fail.
func TestScanWithConcurrentReads(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 6000; i++ {
		e.write(fmt.Sprintf("C/M%03d/p%d.stl", i/10, i), []byte(fmt.Sprintf("solid %d", i)))
	}
	var readErrs atomic.Int64
	var firstErr atomic.Value
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := catalog.GetRootStats(context.Background(), e.db, e.id); err != nil {
					readErrs.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
				}
				if _, err := catalog.ListRoots(context.Background(), e.db); err != nil {
					readErrs.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
				}
			}
		}()
	}
	p := e.scan()
	close(stop)
	wg.Wait()
	if p.Status != StatusDone {
		t.Errorf("scan status = %s, error = %+v", p.Status, p.Error)
	}
	if n := readErrs.Load(); n > 0 {
		t.Errorf("%d read errors, first: %v", n, firstErr.Load())
	}
}
