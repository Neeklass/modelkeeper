//go:build !windows

package watch

// sameFold compares paths; outside Windows they are case-sensitive.
func sameFold(a, b string) bool { return a == b }
