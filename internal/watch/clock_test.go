package watch

import (
	"sync"
	"testing"
	"time"
)

// fakeClock runs the watcher in virtual time: Advance delivers every tick
// that falls into the advanced span, in order, each one handed to the
// watcher's loop before the next.
type fakeClock struct {
	t       *testing.T
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

type fakeTicker struct {
	c       chan time.Time
	every   time.Duration
	next    time.Time
	stopped bool
}

func newFakeClock(t *testing.T) *fakeClock { return &fakeClock{t: t, now: time.Now()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTicker{c: make(chan time.Time), every: d, next: c.now.Add(d)}
	c.tickers = append(c.tickers, ft)
	return ft.c, func() {
		c.mu.Lock()
		ft.stopped = true
		c.mu.Unlock()
	}
}

// Advance moves the clock forward by d.
func (c *fakeClock) Advance(d time.Duration) {
	c.t.Helper()
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTicker
		for _, ft := range c.tickers {
			if !ft.stopped && !ft.next.After(target) && (next == nil || ft.next.Before(next.next)) {
				next = ft
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		at := next.next
		c.now = at
		next.next = at.Add(next.every)
		c.mu.Unlock()
		select {
		case next.c <- at:
		case <-time.After(5 * time.Second):
			c.t.Fatal("the watcher's loop did not take a tick")
		}
	}
}

// flush waits until the loop has handled everything it received so far
// (the ticks Advance handed over).
func (w *Watcher) flush() {
	done := make(chan struct{})
	w.flushReq <- done
	<-done
}

// waitFor polls cond in real time (file system events arrive on their own).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
