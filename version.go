package main

import "runtime/debug"

// version can be set at build time: -ldflags "-X main.version=1.2.3".
// Without it, appVersion takes the version Go records from git.
var version = ""

// appVersion returns the version written into backup manifests: the build
// flag, else the module version from a git tag, else "dev-<commit>" (with
// "-dirty" for uncommitted changes), else "dev".
func appVersion() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return versionFrom(bi)
}

func versionFrom(bi *debug.BuildInfo) string {
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	return "dev-" + rev[:min(12, len(rev))] + dirty
}
