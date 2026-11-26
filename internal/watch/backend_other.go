//go:build !windows

package watch

func newBackend() (backend, error) { return newNotifyBackend() }
