package version

import (
	"runtime/debug"
	"testing"
)

func TestRuntimeUnderDevelIsVersion(t *testing.T) {
	// A test binary's build info reads "(devel)" (or carries no weft
	// dependency at all): Runtime falls back to the source's tag.
	if got := Runtime(); got != Version {
		t.Errorf("Runtime() = %q in the workspace, want Version %q", got, Version)
	}
	devel := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}
	if got := fromBuildInfo(devel); got != Version {
		t.Errorf("main (devel): got %q, want %q", got, Version)
	}
}

func TestRuntimeReadsBuildInfo(t *testing.T) {
	for _, tc := range []struct {
		name string
		bi   debug.BuildInfo
		want string
	}{
		{"main module tagged", debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.3"}}, "v1.2.3"},
		{"consumer dependency", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app", Version: "(devel)"},
			Deps: []*debug.Module{{Path: "github.com/weftgo/weft/core", Version: "v9.9.9"}, {Path: modulePath, Version: "v1.2.4"}},
		}, "v1.2.4"},
		{"replaced by a version", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{{Path: modulePath, Version: "v1.2.4", Replace: &debug.Module{Path: "example.com/fork", Version: "v1.2.5"}}},
		}, "v1.2.5"},
		{"replaced by a directory", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{{Path: modulePath, Version: "v1.2.4", Replace: &debug.Module{Path: "../weft"}}},
		}, Version},
		{"no weft at all", debug.BuildInfo{Main: debug.Module{Path: "example.com/app"}}, Version},
	} {
		if got := fromBuildInfo(&tc.bi); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
