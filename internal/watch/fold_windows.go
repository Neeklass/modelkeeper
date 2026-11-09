package watch

import "strings"

// sameFold compares paths; Windows paths are case-insensitive.
func sameFold(a, b string) bool { return strings.EqualFold(a, b) }
