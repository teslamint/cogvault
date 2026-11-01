package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{"ldflags value wins", "v1.2.3", &debug.BuildInfo{Main: debug.Module{Version: "v0.9.0"}}, true, "v1.2.3"},
		{"go install module version", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.9.0"}}, true, "v0.9.0"},
		{"local build is devel", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, "dev"},
		{"no build info", "", nil, false, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.injected, func() (*debug.BuildInfo, bool) { return tt.info, tt.ok })
			if got != tt.want {
				t.Fatalf("resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}
