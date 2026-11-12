package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionFrom(t *testing.T) {
	setting := func(kv ...string) []debug.BuildSetting {
		var out []debug.BuildSetting
		for i := 0; i+1 < len(kv); i += 2 {
			out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return out
	}
	for _, c := range []struct {
		bi   debug.BuildInfo
		want string
	}{
		{debug.BuildInfo{Main: debug.Module{Version: "v1.2.0"}}, "v1.2.0"},
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"},
			Settings: setting("vcs.revision", "7735c94d0123456789abcdef", "vcs.modified", "false")}, "dev-7735c94d0123"},
		{debug.BuildInfo{Settings: setting("vcs.revision", "abc", "vcs.modified", "true")}, "dev-abc-dirty"},
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
	} {
		if got := versionFrom(&c.bi); got != c.want {
			t.Errorf("versionFrom(%+v) = %q, want %q", c.bi, got, c.want)
		}
	}
}
