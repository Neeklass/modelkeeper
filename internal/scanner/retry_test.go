package scanner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithRetry(t *testing.T) {
	old := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryDelays = old }()

	calls := 0
	err := withRetry(context.Background(), func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}

	calls = 0
	err = withRetry(context.Background(), func(context.Context) error { calls++; return errors.New("disk I/O error") })
	if err == nil || calls != 4 {
		t.Fatalf("permanent failure: err=%v calls=%d", err, calls)
	}
}
