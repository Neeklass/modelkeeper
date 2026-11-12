package watch

import "time"

// clock is the watcher's time source; tests use a fake one to run quiet
// periods and schedules in virtual time.
type clock interface {
	Now() time.Time
	// NewTicker returns a channel that ticks every d, and a stop function.
	NewTicker(d time.Duration) (<-chan time.Time, func())
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}
